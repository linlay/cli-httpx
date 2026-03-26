# httpx

`httpx` 是一个面向智能体和自动化脚本的 HTTP CLI，用来把“登录、带状态请求、提取响应字段”固化进 profile 配置。

用户安装、测试、发版看这个 README；设计、状态模型和内部机制见 [CLAUDE.md](./CLAUDE.md)。

## 安装

优先从 GitHub Releases 下载对应平台压缩包：

- macOS Apple Silicon：`httpx_vX.Y.Z_darwin_arm64.tar.gz`
- macOS Intel：`httpx_vX.Y.Z_darwin_amd64.tar.gz`
- Linux ARM64：`httpx_vX.Y.Z_linux_arm64.tar.gz`
- Linux AMD64：`httpx_vX.Y.Z_linux_amd64.tar.gz`

解压后可以先确认版本信息：

```bash
tar -xzf httpx_v0.1.0_darwin_arm64.tar.gz
./httpx version
./httpx --version
```

如果从源码构建：

```bash
go build -o ./httpx ./cmd/httpx
go install github.com/linlay/cli-httpx/cmd/httpx@latest
```

## 快速上手

最常见的调用形式是：

```bash
httpx <profile> <action>
httpx --param key=value <profile> <action>
```

最小 profile 示例：

```toml
version = 1
base_url = "https://api.example.com"
login_action = "login"

[actions.login]
method = "POST"
path = "/session"
form = { username = { from = "env", key = "DEMO_USER" }, password = { from = "env", key = "DEMO_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[actions.me]
path = "/me"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
extract = ".body"
```

使用步骤：

1. 写一个 `demo.toml`
2. 设置环境变量
3. 执行 `httpx --config . demo login`
4. 执行 `httpx --config . demo me`

示例配置见 [examples/config.toml](./examples/config.toml)。

## 常用命令

```bash
httpx <profile> login
httpx <profile> me
httpx --inspect <profile> me
httpx version
httpx --version
```

全局参数可以放在命令前，也可以放在命令后，例如：

```bash
httpx --config ./examples --format json demo me
httpx demo --format json --config ./examples me
```

常用全局参数：

- `--config <dir>`：配置目录，读取 `<dir>/<profile>.toml`
- `--format json|body`：输出格式，默认 `body`
- `--param key=value`：传入运行时参数，可重复
- `--timeout <duration>`：覆盖配置超时
- `--state-dir <path>`：覆盖默认状态目录
- `--inspect`：只编译请求，不发请求
- `--reveal`：仅 `--inspect` 下显示真实敏感值

## 测试

运行测试：

```bash
go test ./...
```

如果网络不稳定，可以显式带上 Go 代理：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn go test ./...
```

## 手工发布

推荐把 Git tag 作为正式版本号来源，例如 `v0.1.0`。

1. 跑测试：

```bash
go test ./...
```

2. 创建并推送 tag：

```bash
git tag v0.1.0
git push origin v0.1.0
```

3. 在 tag 对应提交上本地打包：

```bash
scripts/release/build.sh v0.1.0
```

如果网络不稳定，可以显式带上代理环境变量：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn scripts/release/build.sh v0.1.0
```

打包完成后会生成：

- `dist/v0.1.0/httpx_v0.1.0_darwin_amd64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_darwin_arm64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_linux_amd64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_linux_arm64.tar.gz`
- `dist/v0.1.0/httpx_v0.1.0_checksums.txt`

4. 校验摘要：

```bash
cd dist/v0.1.0
shasum -a 256 -c httpx_v0.1.0_checksums.txt
```

5. 在 GitHub 创建同名 Release，并手动上传这 5 个文件。

## 常见问题

### 登录后的 cookie 或 access token 存在哪里？

默认保存在本地 state 文件里，不保存在配置文件里：

- 默认目录：`$XDG_STATE_HOME/httpx` 或 `~/.local/state/httpx`
- 文件名：`<profile>.json`
- `values`：保存 `save = { ... }` 提取出来的字符串值，例如 access token
- `cookies`：保存登录态 cookie
- `last_login`：记录最近一次登录时间

state 文件是本地明文 JSON。更完整的状态模型、写回时机和安全约束见 [CLAUDE.md](./CLAUDE.md)。

### `version = 1` 是发布版本号吗？

不是。它表示配置 schema 版本。CLI 的发布版本来自 Git tag，并在构建时写入二进制。

### 看哪里了解内部设计？

设计、状态模型、cookie 复用、输出模型、发布约定和仓库职责边界见 [CLAUDE.md](./CLAUDE.md)。
