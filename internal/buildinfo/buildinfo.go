package buildinfo

import "fmt"

var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func Summary() string {
	return fmt.Sprintf("httpx %s (commit %s, built %s)", Version, Commit, BuildTime)
}
