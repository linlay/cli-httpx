module github.com/linlay/cli-httpx

go 1.26

require (
	github.com/itchyny/gojq v0.12.17
	github.com/pelletier/go-toml/v2 v2.2.4
	github.com/spf13/cobra v1.10.1
)

require github.com/itchyny/timefmt-go v0.1.6 // indirect

replace github.com/spf13/cobra => ./third_party/cobra
