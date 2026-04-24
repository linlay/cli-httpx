package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/linlay/cli-httpx/internal/buildinfo"
	"github.com/spf13/cobra"
)

type requestRunner func(commandRequest) int

type cliOptions struct {
	global    globalOptions
	formatSet bool
	version   bool
}

type paramValues map[string]string

type formatValue struct {
	options *cliOptions
}

type extractValue struct {
	value *map[string]any
	set   bool
}

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

func (v *formatValue) String() string {
	if v == nil || v.options == nil {
		return ""
	}
	return string(v.options.global.Format)
}

func (v *formatValue) Set(raw string) error {
	if v == nil || v.options == nil {
		return fmt.Errorf("internal error: missing format target")
	}
	if err := setFormat(&v.options.global, raw); err != nil {
		return err
	}
	v.options.formatSet = true
	return nil
}

func (v *extractValue) String() string {
	if v == nil || v.value == nil || *v.value == nil {
		return ""
	}
	data, err := json.Marshal(*v.value)
	if err != nil {
		return ""
	}
	return string(data)
}

func (v *extractValue) Set(raw string) error {
	if v.set {
		return fmt.Errorf("--extract may only be provided once")
	}
	extractInput, err := parseExtractInput(raw)
	if err != nil {
		return err
	}
	*v.value = extractInput
	v.set = true
	return nil
}

func parseExtractInput(raw string) (map[string]any, error) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("invalid --extract JSON: %v", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid --extract %q, expected a JSON object", raw)
	}
	return object, nil
}

func newRootCommand(stdin io.Reader, stdout, stderr io.Writer, run requestRunner) *cobra.Command {
	options := &cliOptions{
		global: globalOptions{
			ConfigDir: defaultConfigDir(),
			SecretDir: defaultSecretDir(),
			StateDir:  defaultStateDir(),
		},
	}

	root := &cobra.Command{
		Use:           "httpx",
		Short:         "HTTP CLI for scripted, stateful site actions",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.version {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Summary())
				return err
			}
			return cmd.Help()
		},
	}
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true

	flags := root.PersistentFlags()
	flags.StringVar(&options.global.ConfigDir, "config", options.global.ConfigDir, "Configuration directory")
	flags.StringVar(&options.global.StateDir, "state", options.global.StateDir, "State directory")
	flags.DurationVar(&options.global.Timeout, "timeout", 0, "Request timeout override")
	flags.BoolVar(&options.global.Reveal, "reveal", false, "Reveal sensitive values in inspect output")
	flags.BoolVar(&options.version, "version", false, "Print version information")
	flags.Var(&formatValue{options: options}, "format", "Output format: text or json")
	flags.Var((*paramValues)(&options.global.Params), "param", "Runtime request parameter in key=value form")
	flags.Var(&extractValue{value: &options.global.ExtractInput}, "extract", "Runtime extractor input as a JSON object")

	root.AddCommand(
		newActionRequestCommand(commandRun, "run <site> <action>", "Execute an action request", cobra.ExactArgs(2), options, run),
		newActionRequestCommand(commandInspect, "inspect <site> <action>", "Inspect a compiled action request without executing it", cobra.ExactArgs(2), options, run),
		newActionRequestCommand(commandLogin, "login <site>", "Execute the site's configured login action", cobra.ExactArgs(1), options, run),
		newActionRequestCommand(commandSites, "sites", "List available sites", cobra.NoArgs, options, run),
		newActionRequestCommand(commandSite, "site <site>", "Show site details", cobra.ExactArgs(1), options, run),
		newActionRequestCommand(commandAction, "action <site> <action>", "Show action usage and input contract", cobra.ExactArgs(2), options, run),
		newActionRequestCommand(commandActions, "actions <site>", "List actions for a site", cobra.ExactArgs(1), options, run),
		newActionRequestCommand(commandState, "state <site>", "Show stored state summary for a site", cobra.ExactArgs(1), options, run),
		newVersionCommand(),
		newLoadCommand(options),
	)

	return root
}

func newActionRequestCommand(kind commandKind, use, short string, args cobra.PositionalArgs, options *cliOptions, run requestRunner) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := buildCommandRequest(kind, args, options)
			if err != nil {
				return &exitError{Code: ExitConfig, Err: err}
			}
			if code := run(req); code != ExitSuccess {
				return &exitError{Code: code}
			}
			return nil
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Summary())
			return err
		},
	}
}

func parseArgs(args []string) (commandRequest, error) {
	var (
		req      commandRequest
		captured bool
	)

	root := newRootCommand(nil, io.Discard, io.Discard, func(next commandRequest) int {
		req = next
		captured = true
		return ExitSuccess
	})
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		return commandRequest{}, err
	}
	if !captured {
		return commandRequest{}, flag.ErrHelp
	}
	return req, nil
}

func buildCommandRequest(kind commandKind, args []string, options *cliOptions) (commandRequest, error) {
	req := commandRequest{
		Command: kind,
		Options: options.snapshot(),
	}

	switch kind {
	case commandRun, commandInspect, commandAction:
		req.Site = args[0]
		req.Action = args[1]
	case commandLogin, commandSite, commandActions, commandState:
		req.Site = args[0]
	case commandSites:
	default:
		return commandRequest{}, fmt.Errorf("unsupported command %q", kind)
	}

	if req.Site != "" {
		if err := validateSiteName(req.Site); err != nil {
			return commandRequest{}, err
		}
	}

	if !options.formatSet {
		switch kind {
		case commandRun, commandLogin:
			req.Options.Format = formatText
		case commandInspect:
			req.Options.Format = formatJSON
		case commandSites, commandSite, commandAction, commandActions, commandState:
			req.Options.Format = formatText
		}
	}

	if err := validateCommandOptions(&req, options.formatSet); err != nil {
		return commandRequest{}, err
	}

	return req, nil
}

func (o *cliOptions) snapshot() globalOptions {
	result := o.global
	if len(o.global.Params) > 0 {
		result.Params = make(map[string]string, len(o.global.Params))
		for key, value := range o.global.Params {
			result.Params[key] = value
		}
	}
	if o.global.ExtractInput != nil {
		result.ExtractInput = make(map[string]any, len(o.global.ExtractInput))
		for key, value := range o.global.ExtractInput {
			result.ExtractInput[key] = value
		}
	}
	return result
}

func setFormat(opts *globalOptions, value string) error {
	switch outputFormat(value) {
	case formatText, formatJSON:
		opts.Format = outputFormat(value)
		return nil
	case "body":
		return fmt.Errorf("unsupported format %q; use %q instead", value, formatText)
	default:
		return fmt.Errorf("unsupported format %q", value)
	}
}

func validateCommandOptions(req *commandRequest, formatSet bool) error {
	switch req.Command {
	case commandRun, commandLogin:
		if req.Options.Format != formatText && req.Options.Format != formatJSON {
			return fmt.Errorf("--format %s is not supported with %s", req.Options.Format, req.Command)
		}
		if req.Command == commandLogin && req.Options.ExtractInput != nil {
			return fmt.Errorf("--extract is not supported with %s", req.Command)
		}
	case commandInspect:
		if req.Options.Format != formatJSON {
			return fmt.Errorf("--format %s is not supported with inspect", req.Options.Format)
		}
	case commandSites, commandSite, commandAction, commandActions, commandState:
		if req.Options.Format != formatText && req.Options.Format != formatJSON {
			return fmt.Errorf("--format %s is not supported with %s", req.Options.Format, req.Command)
		}
		if req.Options.Timeout > 0 {
			return fmt.Errorf("--timeout is not supported with %s", req.Command)
		}
		if len(req.Options.Params) > 0 {
			return fmt.Errorf("--param is not supported with %s", req.Command)
		}
		if req.Options.ExtractInput != nil {
			return fmt.Errorf("--extract is not supported with %s", req.Command)
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
	case "run", "inspect", "login", "sites", "site", "action", "actions", "state", "version", "help":
		return true
	default:
		return false
	}
}
