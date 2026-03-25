package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

type paramValues map[string]string

func (p *paramValues) String() string {
	if p == nil {
		return ""
	}
	parts := make([]string, 0, len(*p))
	for key, value := range *p {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func (p *paramValues) Set(raw string) error {
	key, value, ok := strings.Cut(raw, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("invalid --param %q, expected key=value", raw)
	}
	if *p == nil {
		*p = map[string]string{}
	}
	(*p)[key] = value
	return nil
}

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
		ConfigDir: defaultConfigDir(),
		Format:    formatBody,
		StateDir:  defaultStateDir(),
	}
	rest, formatSet, err := parseGlobalArgs(args, &opts)
	if err != nil {
		return commandRequest{}, err
	}
	if len(rest) == 0 {
		return commandRequest{}, flag.ErrHelp
	}
	if len(rest) != 2 {
		return commandRequest{}, fmt.Errorf("usage: httpx [global flags] [--inspect] <profile> <action>")
	}
	switch rest[0] {
	case "run", "login", "inspect":
		return commandRequest{}, fmt.Errorf("usage: httpx [global flags] [--inspect] <profile> <action>")
	}

	req := commandRequest{
		Profile: rest[0],
		Action:  rest[1],
		Options: opts,
	}

	if req.Options.Inspect && !formatSet {
		req.Options.Format = formatJSON
	}
	if req.Options.Inspect && formatSet && opts.Format == formatBody {
		return commandRequest{}, fmt.Errorf("--format body is not supported with inspect")
	}
	return req, nil
}

func parseGlobalArgs(args []string, opts *globalOptions) ([]string, bool, error) {
	rest := make([]string, 0, len(args))
	formatSet := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			return nil, false, flag.ErrHelp
		case arg == "--reveal":
			opts.Reveal = true
		case arg == "--inspect":
			opts.Inspect = true
		case arg == "--config":
			value, next, err := requireFlagValue(args, i, "--config")
			if err != nil {
				return nil, false, err
			}
			opts.ConfigDir = value
			i = next
		case strings.HasPrefix(arg, "--config="):
			opts.ConfigDir = strings.TrimPrefix(arg, "--config=")
		case arg == "--format":
			value, next, err := requireFlagValue(args, i, "--format")
			if err != nil {
				return nil, false, err
			}
			if err := setFormat(opts, value); err != nil {
				return nil, false, err
			}
			formatSet = true
			i = next
		case strings.HasPrefix(arg, "--format="):
			if err := setFormat(opts, strings.TrimPrefix(arg, "--format=")); err != nil {
				return nil, false, err
			}
			formatSet = true
		case arg == "--timeout":
			value, next, err := requireFlagValue(args, i, "--timeout")
			if err != nil {
				return nil, false, err
			}
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return nil, false, fmt.Errorf("invalid --timeout %q: %v", value, err)
			}
			opts.Timeout = timeout
			i = next
		case strings.HasPrefix(arg, "--timeout="):
			value := strings.TrimPrefix(arg, "--timeout=")
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return nil, false, fmt.Errorf("invalid --timeout %q: %v", value, err)
			}
			opts.Timeout = timeout
		case arg == "--state-dir":
			value, next, err := requireFlagValue(args, i, "--state-dir")
			if err != nil {
				return nil, false, err
			}
			opts.StateDir = value
			i = next
		case strings.HasPrefix(arg, "--state-dir="):
			opts.StateDir = strings.TrimPrefix(arg, "--state-dir=")
		case arg == "--param":
			value, next, err := requireFlagValue(args, i, "--param")
			if err != nil {
				return nil, false, err
			}
			if err := (*paramValues)(&opts.Params).Set(value); err != nil {
				return nil, false, err
			}
			i = next
		case strings.HasPrefix(arg, "--param="):
			if err := (*paramValues)(&opts.Params).Set(strings.TrimPrefix(arg, "--param=")); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(arg, "--"):
			return nil, false, fmt.Errorf("unknown flag %q", arg)
		default:
			rest = append(rest, arg)
		}
	}

	return rest, formatSet, nil
}

func requireFlagValue(args []string, index int, name string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("flag %s requires a value", name)
	}
	return args[next], next, nil
}

func setFormat(opts *globalOptions, value string) error {
	switch outputFormat(value) {
	case formatJSON, formatBody:
		opts.Format = outputFormat(value)
		return nil
	default:
		return fmt.Errorf("unsupported format %q", value)
	}
}

func usageText() string {
	lines := []string{
		"Usage:",
		"  httpx [global flags] [--inspect] <profile> <action>",
		"",
		"Global flags (can appear before or after the command):",
		"  --config <dir>",
		"  --format json|body",
		"  --timeout <duration>",
		"  --state-dir <path>",
		"  --param key=value",
		"  --inspect",
		"  --reveal",
	}
	return strings.Join(lines, "\n") + "\n"
}
