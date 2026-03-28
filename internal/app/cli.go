package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/linlay/cli-httpx/internal/buildinfo"
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
	if isVersionCommand(args) {
		fmt.Fprintln(stdout, buildinfo.Summary())
		return ExitSuccess
	}

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
		StateDir:  defaultStateDir(),
	}
	rest, formatSet, err := parseGlobalArgs(args, &opts)
	if err != nil {
		return commandRequest{}, err
	}
	if len(rest) == 0 {
		return commandRequest{}, flag.ErrHelp
	}

	cmd := commandKind(rest[0])
	switch cmd {
	case commandRun:
		if len(rest) != 3 {
			return commandRequest{}, usageError()
		}
		req := commandRequest{Command: cmd, Site: rest[1], Action: rest[2], Options: opts}
		if err := validateSiteName(req.Site); err != nil {
			return commandRequest{}, err
		}
		if !formatSet {
			req.Options.Format = formatBody
		}
		if err := validateCommandOptions(&req, formatSet); err != nil {
			return commandRequest{}, err
		}
		return req, nil
	case commandInspect:
		if len(rest) != 3 {
			return commandRequest{}, usageError()
		}
		req := commandRequest{Command: cmd, Site: rest[1], Action: rest[2], Options: opts}
		if err := validateSiteName(req.Site); err != nil {
			return commandRequest{}, err
		}
		if !formatSet {
			req.Options.Format = formatJSON
		}
		if err := validateCommandOptions(&req, formatSet); err != nil {
			return commandRequest{}, err
		}
		return req, nil
	case commandLogin:
		if len(rest) != 2 {
			return commandRequest{}, usageError()
		}
		req := commandRequest{Command: cmd, Site: rest[1], Options: opts}
		if err := validateSiteName(req.Site); err != nil {
			return commandRequest{}, err
		}
		if !formatSet {
			req.Options.Format = formatBody
		}
		if err := validateCommandOptions(&req, formatSet); err != nil {
			return commandRequest{}, err
		}
		return req, nil
	case commandSites:
		if len(rest) != 1 {
			return commandRequest{}, usageError()
		}
		req := commandRequest{Command: cmd, Options: opts}
		if !formatSet {
			req.Options.Format = formatText
		}
		if err := validateCommandOptions(&req, formatSet); err != nil {
			return commandRequest{}, err
		}
		return req, nil
	case commandSite, commandActions, commandState:
		if len(rest) != 2 {
			return commandRequest{}, usageError()
		}
		req := commandRequest{Command: cmd, Site: rest[1], Options: opts}
		if err := validateSiteName(req.Site); err != nil {
			return commandRequest{}, err
		}
		if !formatSet {
			req.Options.Format = formatText
		}
		if err := validateCommandOptions(&req, formatSet); err != nil {
			return commandRequest{}, err
		}
		return req, nil
	case "help":
		if len(rest) != 1 {
			return commandRequest{}, usageError()
		}
		return commandRequest{}, flag.ErrHelp
	default:
		return commandRequest{}, usageError()
	}
}

func isVersionCommand(args []string) bool {
	return len(args) == 1 && (args[0] == "version" || args[0] == "--version")
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
		case arg == "--state":
			value, next, err := requireFlagValue(args, i, "--state")
			if err != nil {
				return nil, false, err
			}
			opts.StateDir = value
			i = next
		case strings.HasPrefix(arg, "--state="):
			opts.StateDir = strings.TrimPrefix(arg, "--state=")
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
	case formatText, formatJSON, formatBody:
		opts.Format = outputFormat(value)
		return nil
	default:
		return fmt.Errorf("unsupported format %q", value)
	}
}

func validateCommandOptions(req *commandRequest, formatSet bool) error {
	switch req.Command {
	case commandRun, commandLogin:
		if req.Options.Format != formatBody && req.Options.Format != formatJSON {
			return fmt.Errorf("--format %s is not supported with %s", req.Options.Format, req.Command)
		}
	case commandInspect:
		if req.Options.Format != formatJSON {
			return fmt.Errorf("--format %s is not supported with inspect", req.Options.Format)
		}
	case commandSites, commandSite, commandActions, commandState:
		if req.Options.Format != formatText && req.Options.Format != formatJSON {
			return fmt.Errorf("--format %s is not supported with %s", req.Options.Format, req.Command)
		}
		if req.Options.Timeout > 0 {
			return fmt.Errorf("--timeout is not supported with %s", req.Command)
		}
		if len(req.Options.Params) > 0 {
			return fmt.Errorf("--param is not supported with %s", req.Command)
		}
		if req.Options.Reveal {
			return fmt.Errorf("--reveal is not supported with %s", req.Command)
		}
	}

	if req.Command != commandInspect && req.Options.Reveal {
		return fmt.Errorf("--reveal is only supported with inspect")
	}
	if !formatSet && req.Options.Format == "" {
		return fmt.Errorf("internal error: format not set")
	}
	return nil
}

func usageError() error {
	return fmt.Errorf("usage: httpx <subcommand> [args]")
}

func validateSiteName(site string) error {
	site = strings.TrimSpace(site)
	if site == "" {
		return fmt.Errorf("%w: site is required", ErrConfig)
	}
	if isReservedWord(site) {
		return fmt.Errorf("%w: site name %q is reserved", ErrConfig, site)
	}
	return nil
}

func isReservedWord(value string) bool {
	switch strings.TrimSpace(value) {
	case "run", "inspect", "login", "sites", "site", "actions", "state", "version", "help":
		return true
	default:
		return false
	}
}

func usageText() string {
	lines := []string{
		"Usage:",
		"  httpx run <site> <action>",
		"  httpx inspect <site> <action>",
		"  httpx login <site>",
		"  httpx sites",
		"  httpx site <site>",
		"  httpx actions <site>",
		"  httpx state <site>",
		"  httpx version",
		"  httpx --version",
		"  httpx help",
		"",
		"Global flags (can appear before or after the subcommand):",
		"  --config <dir>",
		"  --state <path>",
		"  --format text|json|body",
		"  --timeout <duration>",
		"  --param key=value",
		"  --reveal",
		"",
		"Format defaults:",
		"  run/login = body",
		"  inspect = json",
		"  sites/site/actions/state = text",
		"",
		"Notes:",
		"  version = 1 inside TOML site files is the config schema version, not the CLI release version.",
	}
	return strings.Join(lines, "\n") + "\n"
}
