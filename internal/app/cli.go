package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

func Main(args []string, stdout io.Writer, stderr io.Writer) int {
	req, err := parseArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText())
			return ExitSuccess
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitConfig
	}

	runtime := NewRuntime(stdout, stderr)
	return runtime.Run(req)
}

func parseArgs(args []string) (commandRequest, error) {
	opts := globalOptions{
		ConfigPath: defaultConfigPath(),
		Format:     formatJSON,
		StateDir:   defaultStateDir(),
	}
	fs := flag.NewFlagSet("httpx", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.DurationVar(&opts.Timeout, "timeout", 0, "override request timeout")
	fs.StringVar(&opts.StateDir, "state-dir", opts.StateDir, "directory for persistent state")
	fs.BoolVar(&opts.Reveal, "reveal", false, "show secret values in inspect output")
	format := string(opts.Format)
	fs.StringVar(&format, "format", format, "output format: json|body")

	if err := fs.Parse(args); err != nil {
		return commandRequest{}, err
	}
	switch outputFormat(format) {
	case formatJSON, formatBody:
		opts.Format = outputFormat(format)
	default:
		return commandRequest{}, fmt.Errorf("unsupported format %q", format)
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return commandRequest{}, flag.ErrHelp
	}

	req := commandRequest{Options: opts}
	switch rest[0] {
	case string(commandRun):
		if len(rest) != 3 {
			return commandRequest{}, fmt.Errorf("usage: httpx run <profile> <action>")
		}
		req.Kind = commandRun
		req.Profile = rest[1]
		req.Action = rest[2]
	case string(commandLogin):
		if len(rest) != 2 {
			return commandRequest{}, fmt.Errorf("usage: httpx login <profile>")
		}
		req.Kind = commandLogin
		req.Profile = rest[1]
	case string(commandInspect):
		if len(rest) != 3 {
			return commandRequest{}, fmt.Errorf("usage: httpx inspect <profile> <action>")
		}
		req.Kind = commandInspect
		req.Profile = rest[1]
		req.Action = rest[2]
	default:
		return commandRequest{}, fmt.Errorf("unknown command %q", rest[0])
	}

	if req.Kind == commandInspect && opts.Format == formatBody {
		return commandRequest{}, fmt.Errorf("--format body is not supported with inspect")
	}
	return req, nil
}

func usageText() string {
	lines := []string{
		"Usage:",
		"  httpx [global flags] run <profile> <action>",
		"  httpx [global flags] login <profile>",
		"  httpx [global flags] inspect <profile> <action>",
		"",
		"Global flags:",
		"  --config <path>",
		"  --format json|body",
		"  --timeout <duration>",
		"  --state-dir <path>",
		"  --reveal",
	}
	return strings.Join(lines, "\n") + "\n"
}
