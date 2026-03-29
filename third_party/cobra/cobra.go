package cobra

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type PositionalArgs func(cmd *Command, args []string) error

type CompletionOptions struct {
	DisableDefaultCmd bool
}

type Command struct {
	Use               string
	Short             string
	Args              PositionalArgs
	RunE              func(cmd *Command, args []string) error
	SilenceUsage      bool
	SilenceErrors     bool
	CompletionOptions CompletionOptions

	parent   *Command
	children []*Command
	flagSet  *flag.FlagSet
	pflagSet *flag.FlagSet
	args     []string
	in       io.Reader
	out      io.Writer
	err      io.Writer
}

func (c *Command) AddCommand(children ...*Command) {
	for _, child := range children {
		if child == nil {
			continue
		}
		child.parent = c
		if child.in == nil {
			child.in = c.InOrStdin()
		}
		if child.out == nil {
			child.out = c.OutOrStdout()
		}
		if child.err == nil {
			child.err = c.ErrOrStderr()
		}
		c.children = append(c.children, child)
	}
}

func (c *Command) SetArgs(args []string) {
	c.args = append([]string(nil), args...)
}

func (c *Command) SetIn(r io.Reader) {
	c.in = r
}

func (c *Command) SetOut(w io.Writer) {
	c.out = w
}

func (c *Command) SetErr(w io.Writer) {
	c.err = w
}

func (c *Command) InOrStdin() io.Reader {
	if c.in != nil {
		return c.in
	}
	if c.parent != nil {
		return c.parent.InOrStdin()
	}
	return os.Stdin
}

func (c *Command) OutOrStdout() io.Writer {
	if c.out != nil {
		return c.out
	}
	if c.parent != nil {
		return c.parent.OutOrStdout()
	}
	return os.Stdout
}

func (c *Command) ErrOrStderr() io.Writer {
	if c.err != nil {
		return c.err
	}
	if c.parent != nil {
		return c.parent.ErrOrStderr()
	}
	return os.Stderr
}

func (c *Command) Flags() *flag.FlagSet {
	if c.flagSet == nil {
		c.flagSet = flag.NewFlagSet(c.Name(), flag.ContinueOnError)
		c.flagSet.SetOutput(io.Discard)
	}
	return c.flagSet
}

func (c *Command) PersistentFlags() *flag.FlagSet {
	if c.pflagSet == nil {
		c.pflagSet = flag.NewFlagSet(c.Name(), flag.ContinueOnError)
		c.pflagSet.SetOutput(io.Discard)
	}
	return c.pflagSet
}

func (c *Command) Name() string {
	name := strings.TrimSpace(c.Use)
	if name == "" {
		return ""
	}
	return strings.Fields(name)[0]
}

func (c *Command) CommandPath() string {
	if c.parent == nil {
		return c.Name()
	}
	parentPath := c.parent.CommandPath()
	if parentPath == "" {
		return c.Name()
	}
	if c.Name() == "" {
		return parentPath
	}
	return parentPath + " " + c.Name()
}

func (c *Command) Execute() error {
	return c.execute(c.args)
}

func (c *Command) Help() error {
	return c.printHelp()
}

func (c *Command) execute(args []string) error {
	persistentArgs, err := c.parsePersistentFlags(args)
	if err != nil {
		return err
	}

	if len(persistentArgs) > 0 {
		switch persistentArgs[0] {
		case "help":
			return c.helpFor(persistentArgs[1:])
		case "-h", "--help":
			return c.printHelp()
		}
	}

	if child := c.findChild(persistentArgs); child != nil {
		return child.execute(persistentArgs[1:])
	}

	parsedArgs, err := c.parseFlags(persistentArgs)
	if err != nil {
		return err
	}

	for _, arg := range parsedArgs {
		if arg == "-h" || arg == "--help" {
			return c.printHelp()
		}
	}

	if len(c.children) > 0 && len(parsedArgs) > 0 && !strings.HasPrefix(parsedArgs[0], "-") {
		return fmt.Errorf("unknown command %q for %q", parsedArgs[0], c.CommandPath())
	}

	if len(c.children) > 0 && len(parsedArgs) == 0 && c.RunE == nil {
		return c.printHelp()
	}

	if c.Args != nil {
		if err := c.Args(c, parsedArgs); err != nil {
			return err
		}
	}
	if c.RunE != nil {
		return c.RunE(c, parsedArgs)
	}
	return c.printHelp()
}

func (c *Command) helpFor(args []string) error {
	target := c
	for _, arg := range args {
		next := target.findChild([]string{arg})
		if next == nil {
			return fmt.Errorf("unknown help topic %q for %q", arg, target.CommandPath())
		}
		target = next
	}
	return target.printHelp()
}

func (c *Command) findChild(args []string) *Command {
	if len(args) == 0 {
		return nil
	}
	name := args[0]
	for _, child := range c.children {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func (c *Command) parseFlags(args []string) ([]string, error) {
	flags := c.localAndInheritedFlags()
	if len(flags) == 0 {
		return args, nil
	}
	return parseFlags(args, flags)
}

func (c *Command) parsePersistentFlags(args []string) ([]string, error) {
	flags := c.inheritedPersistentFlags()
	if len(flags) == 0 {
		return args, nil
	}
	return parseFlags(args, flags)
}

func (c *Command) printHelp() error {
	_, err := fmt.Fprint(c.OutOrStdout(), c.helpText())
	return err
}

func (c *Command) helpText() string {
	var b strings.Builder
	if c.CommandPath() != "" {
		fmt.Fprintf(&b, "Usage:\n  %s", c.CommandPath())
		if c.Name() != "" && c.Use != "" && len(strings.Fields(c.Use)) > 1 {
			fmt.Fprintf(&b, " %s", strings.Join(strings.Fields(c.Use)[1:], " "))
		}
		b.WriteString("\n")
	}
	if c.Short != "" {
		fmt.Fprintf(&b, "\n%s\n", c.Short)
	}
	if len(c.children) > 0 {
		b.WriteString("\nAvailable Commands:\n")
		for _, child := range c.children {
			fmt.Fprintf(&b, "  %-10s %s\n", child.Name(), child.Short)
		}
		if !c.CompletionOptions.DisableDefaultCmd {
			b.WriteString("  completion Generate the autocompletion script\n")
		}
		b.WriteString("  help       Help about any command\n")
	}
	flags := c.helpFlags()
	if len(flags) > 0 {
		hasFlags := false
		seen := map[string]bool{}
		for _, flagSet := range flags {
			flagSet.VisitAll(func(f *flag.Flag) {
				if seen[f.Name] {
					return
				}
				if !hasFlags {
					b.WriteString("\nFlags:\n")
					hasFlags = true
				}
				fmt.Fprintf(&b, "      --%s %s\n", f.Name, f.Usage)
				seen[f.Name] = true
			})
		}
		if hasFlags {
			b.WriteString("  -h, --help help for this command\n")
		}
	}
	return b.String()
}

func NoArgs(cmd *Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("accepts 0 arg(s), received %d", len(args))
	}
	return nil
}

func ExactArgs(n int) PositionalArgs {
	return func(cmd *Command, args []string) error {
		if len(args) != n {
			return fmt.Errorf("accepts %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func MinimumNArgs(n int) PositionalArgs {
	return func(cmd *Command, args []string) error {
		if len(args) < n {
			return fmt.Errorf("requires at least %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func (c *Command) inheritedPersistentFlags() []*flag.FlagSet {
	var sets []*flag.FlagSet
	for current := c; current != nil; current = current.parent {
		if current.pflagSet != nil {
			sets = append([]*flag.FlagSet{current.pflagSet}, sets...)
		}
	}
	return sets
}

func (c *Command) localAndInheritedFlags() []*flag.FlagSet {
	sets := c.inheritedPersistentFlags()
	if c.flagSet != nil {
		sets = append(sets, c.flagSet)
	}
	return sets
}

func (c *Command) helpFlags() []*flag.FlagSet {
	return c.localAndInheritedFlags()
}

func parseFlags(args []string, flagSets []*flag.FlagSet) ([]string, error) {
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") || arg == "--help" {
			positionals = append(positionals, arg)
			continue
		}

		nameValue := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		f := lookupFlag(flagSets, name)
		if f == nil {
			return nil, fmt.Errorf("unknown flag: --%s", name)
		}
		if !hasValue {
			if flagValue, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && flagValue.IsBoolFlag() {
				value = "true"
			} else {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: --%s", name)
				}
				value = args[i]
			}
		}
		if err := f.Value.Set(value); err != nil {
			return nil, err
		}
	}
	return positionals, nil
}

func lookupFlag(flagSets []*flag.FlagSet, name string) *flag.Flag {
	for _, flagSet := range flagSets {
		if flagSet == nil {
			continue
		}
		if f := flagSet.Lookup(name); f != nil {
			return f
		}
	}
	return nil
}
