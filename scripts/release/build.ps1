#Requires -Version 5.1
param(
    [string]$TargetOS,
    [string]$TargetArch
)
<#
.SYNOPSIS
    Builds httpx release archives for all platform targets.
.DESCRIPTION
    Reads the release version from VERSION, builds Go binaries for all targets,
    and packages them into dist/<version>/.
#>
$ErrorActionPreference = "Stop"

$SCRIPT_DIR = Split-Path -Parent $MyInvocation.MyCommand.Path
$REPO_ROOT = (Resolve-Path (Join-Path $SCRIPT_DIR "..\..")).Path

$versionFile = Join-Path $REPO_ROOT "VERSION"
if (-not (Test-Path $versionFile)) {
    Write-Error "VERSION file not found: $versionFile"
}
$version = (Get-Content $versionFile -Raw).Trim()
if (-not $version) {
    Write-Error "VERSION file is empty: $versionFile"
}
if ($version -notmatch '^v\d+\.\d+\.\d+([.\-][0-9A-Za-z.\-]+)?$') {
    Write-Error "invalid VERSION value: $version (expected vX.Y.Z)"
}

$distDir = Join-Path $REPO_ROOT "dist\$version"
$stageDir = Join-Path $distDir ".stage"
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

Push-Location $REPO_ROOT
try {
    $commit = & git rev-parse --short HEAD
    if ($LASTEXITCODE -ne 0) { $commit = "unknown" }
} finally {
    Pop-Location
}

if (Test-Path $stageDir) { Remove-Item $stageDir -Recurse -Force }
New-Item -ItemType Directory -Path $distDir -Force | Out-Null
New-Item -ItemType Directory -Path $stageDir -Force | Out-Null

$includeLicense = Test-Path (Join-Path $REPO_ROOT "LICENSE")
if (-not $includeLicense) {
    Write-Warning "LICENSE not found; archives will not include it"
}

$targets = @(
    @{ OS = "darwin";  Arch = "amd64" },
    @{ OS = "darwin";  Arch = "arm64" },
    @{ OS = "linux";   Arch = "amd64" },
    @{ OS = "linux";   Arch = "arm64" },
    @{ OS = "windows"; Arch = "amd64" },
    @{ OS = "windows"; Arch = "arm64" }
)
if ($TargetOS -or $TargetArch) {
    if (-not $TargetOS -or -not $TargetArch) { Write-Error "TargetOS and TargetArch must be provided together" }
    $targets = @(@{ OS = $TargetOS; Arch = $TargetArch })
}

$archives = @()

foreach ($t in $targets) {
    $goos = $t.OS
    $goarch = $t.Arch
    $binaryName = "httpx"
    $archiveExt = "tar.gz"
    if ($goos -eq "windows") {
        $binaryName = "httpx.exe"
        $archiveExt = "zip"
    }
    $archiveName = "httpx_${version}_${goos}_${goarch}.${archiveExt}"
    $archives += $archiveName
    $packageDir = Join-Path $stageDir "httpx_${version}_${goos}_${goarch}"

    if (Test-Path $packageDir) { Remove-Item $packageDir -Recurse -Force }
    New-Item -ItemType Directory -Path $packageDir -Force | Out-Null

    Write-Host "building $goos/$goarch"
    $ldflags = "-s -w -X github.com/linlay/cli-httpx/internal/buildinfo.Version=$version -X github.com/linlay/cli-httpx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-httpx/internal/buildinfo.BuildTime=$buildTime"

    $oldCGO = $env:CGO_ENABLED; $oldOS = $env:GOOS; $oldArch = $env:GOARCH
    try {
        $env:CGO_ENABLED = "0"
        $env:GOOS = $goos
        $env:GOARCH = $goarch
        Push-Location $REPO_ROOT
        try {
            & go build -trimpath -ldflags $ldflags -o (Join-Path $packageDir $binaryName) ./cmd/httpx
            if ($LASTEXITCODE -ne 0) { Write-Error "go build failed for $goos/$goarch" }
        } finally {
            Pop-Location
        }
    } finally {
        if ($null -eq $oldCGO) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $oldCGO }
        if ($null -eq $oldOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldOS }
        if ($null -eq $oldArch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldArch }
    }

    Copy-Item (Join-Path $REPO_ROOT "README.md") (Join-Path $packageDir "README.md")
    if ($includeLicense) {
        Copy-Item (Join-Path $REPO_ROOT "LICENSE") (Join-Path $packageDir "LICENSE")
    }

    $archivePath = Join-Path $distDir $archiveName
    if ($archiveExt -eq "zip") {
        if (Test-Path $archivePath) { Remove-Item $archivePath -Force }
        Compress-Archive -Path (Join-Path $packageDir "*") -DestinationPath $archivePath -CompressionLevel Optimal
    } else {
        Push-Location $packageDir
        try {
            & tar -czf $archivePath .
            if ($LASTEXITCODE -ne 0) { Write-Error "tar failed for $goos/$goarch" }
        } finally {
            Pop-Location
        }
    }
}

# Generate checksums
$checksumFile = Join-Path $distDir "httpx_${version}_checksums.txt"
$checksumLines = @()
foreach ($a in $archives) {
    $aPath = Join-Path $distDir $a
    $hash = (Get-FileHash -Algorithm SHA256 $aPath).Hash.ToLowerInvariant()
    $checksumLines += "$hash  $a"
}
[IO.File]::WriteAllText($checksumFile, ($checksumLines -join "`n") + "`n", [Text.UTF8Encoding]::new($false))

# Cleanup stage
Remove-Item $stageDir -Recurse -Force -ErrorAction SilentlyContinue
Write-Host "release artifacts written to $distDir"
