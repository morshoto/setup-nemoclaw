package cobra

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type Command struct {
	Use   string
	Short string

	Run  func(cmd *Command, args []string)
	RunE func(cmd *Command, args []string) error

	PersistentPreRunE func(cmd *Command, args []string) error

	SilenceErrors bool
	SilenceUsage  bool

	parent   *Command
	children []*Command

	ctx context.Context

	in     io.Reader
	out    io.Writer
	errOut io.Writer

	persistentFlags *FlagSet
	localFlags      *FlagSet
}

func (c *Command) Execute() error {
	return c.ExecuteContext(context.Background())
}

func (c *Command) ExecuteContext(ctx context.Context) error {
	c.SetContext(ctx)
	return c.execute(os.Args[1:])
}

func (c *Command) execute(args []string) error {
	if c.ctx == nil {
		if c.parent != nil {
			c.ctx = c.parent.Context()
		} else {
			c.ctx = context.Background()
		}
	}

	if helpRequested(args) {
		c.printHelp()
		return nil
	}

	remaining, err := c.PersistentFlags().parseArgs(args)
	if err != nil {
		return err
	}
	remaining, err = c.Flags().parseArgs(remaining)
	if err != nil {
		return err
	}

	if len(remaining) > 0 {
		if child := c.findChild(remaining[0]); child != nil {
			if c.PersistentPreRunE != nil {
				if err := c.PersistentPreRunE(child, remaining[1:]); err != nil {
					return err
				}
			}
			return child.execute(remaining[1:])
		}
	}

	if c.PersistentPreRunE != nil {
		if err := c.PersistentPreRunE(c, remaining); err != nil {
			return err
		}
	}

	if c.RunE != nil {
		return c.RunE(c, remaining)
	}
	if c.Run != nil {
		c.Run(c, remaining)
		return nil
	}

	c.printHelp()
	return nil
}

func (c *Command) findChild(name string) *Command {
	for _, child := range c.children {
		if child.Use == name {
			return child
		}
	}
	return nil
}

func (c *Command) AddCommand(cmds ...*Command) {
	for _, cmd := range cmds {
		cmd.parent = c
		c.children = append(c.children, cmd)
	}
}

func (c *Command) PersistentFlags() *FlagSet {
	if c.persistentFlags == nil {
		c.persistentFlags = newFlagSet(c.Use, c.errWriter())
	}
	return c.persistentFlags
}

func (c *Command) Flags() *FlagSet {
	if c.localFlags == nil {
		c.localFlags = newFlagSet(c.Use, c.errWriter())
	}
	return c.localFlags
}

func (c *Command) SetContext(ctx context.Context) {
	c.ctx = ctx
}

func (c *Command) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	if c.parent != nil {
		return c.parent.Context()
	}
	return context.Background()
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

func (c *Command) InOrStdin() io.Reader {
	if c.in != nil {
		return c.in
	}
	if c.parent != nil {
		return c.parent.InOrStdin()
	}
	return os.Stdin
}

func (c *Command) OutOrStderr() io.Writer {
	if c.errOut != nil {
		return c.errOut
	}
	if c.parent != nil {
		return c.parent.OutOrStderr()
	}
	return os.Stderr
}

func (c *Command) SetOut(w io.Writer) {
	c.out = w
}

func (c *Command) SetIn(r io.Reader) {
	c.in = r
}

func (c *Command) SetErr(w io.Writer) {
	c.errOut = w
}

func (c *Command) Name() string {
	return strings.Fields(c.Use)[0]
}

func (c *Command) printHelp() {
	fmt.Fprintln(c.OutOrStdout(), c.usageString())
}

func (c *Command) usageString() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n\n", c.Use)
	if c.Short != "" {
		fmt.Fprintf(&b, "%s\n\n", c.Short)
	}
	if len(c.children) > 0 {
		b.WriteString("Commands:\n")
		for _, child := range c.children {
			fmt.Fprintf(&b, "  %s\t%s\n", child.Name(), child.Short)
		}
		b.WriteString("\n")
	}
	if c.persistentFlags != nil {
		b.WriteString("Global Flags:\n")
		c.persistentFlags.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(&b, "  --%s\t%s (default %q)\n", f.Name, f.Usage, f.DefValue)
		})
	}
	if c.localFlags != nil {
		b.WriteString("Flags:\n")
		c.localFlags.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(&b, "  --%s\t%s (default %q)\n", f.Name, f.Usage, f.DefValue)
		})
	}
	return b.String()
}

func (c *Command) errWriter() io.Writer {
	if c.errOut != nil {
		return c.errOut
	}
	return os.Stderr
}

func newFlagSet(use string, out io.Writer) *FlagSet {
	fs := flag.NewFlagSet(use, flag.ContinueOnError)
	fs.SetOutput(out)
	return &FlagSet{FlagSet: fs}
}

func helpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

type FlagSet struct {
	*flag.FlagSet
	specs map[string]*flagSpec
}

func (f *FlagSet) parseArgs(args []string) ([]string, error) {
	if f == nil || f.FlagSet == nil {
		return args, nil
	}
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			remaining = append(remaining, arg)
			continue
		}
		name, value, hasValue := splitFlag(arg)
		if name == "" {
			remaining = append(remaining, arg)
			continue
		}
		spec, ok := f.specs[name]
		if !ok {
			remaining = append(remaining, arg)
			continue
		}
		switch spec.kind {
		case "bool":
			if hasValue {
				parsed := value == "true"
				if value != "true" && value != "false" {
					return nil, fmt.Errorf("invalid value for --%s: %s", name, value)
				}
				*spec.boolPtr = parsed
				continue
			}
			if i+1 < len(args) {
				next := args[i+1]
				if next == "true" || next == "false" {
					*spec.boolPtr = next == "true"
					i++
					continue
				}
			}
			*spec.boolPtr = true
		case "string":
			if hasValue {
				*spec.stringPtr = value
				continue
			}
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag needs an argument: --%s", name)
			}
			*spec.stringPtr = args[i+1]
			i++
		default:
			remaining = append(remaining, arg)
		}
	}
	return remaining, nil
}

func (f *FlagSet) BoolVar(p *bool, name string, value bool, usage string) {
	f.ensureSpecs()
	f.FlagSet.BoolVar(p, name, value, usage)
	f.specs[name] = &flagSpec{kind: "bool", boolPtr: p}
}

func (f *FlagSet) StringVar(p *string, name, value, usage string) {
	f.ensureSpecs()
	f.FlagSet.StringVar(p, name, value, usage)
	f.specs[name] = &flagSpec{kind: "string", stringPtr: p}
}

func (f *FlagSet) ensureSpecs() {
	if f.specs == nil {
		f.specs = map[string]*flagSpec{}
	}
}

type flagSpec struct {
	kind      string
	boolPtr   *bool
	stringPtr *string
}

func splitFlag(arg string) (name string, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "--") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(arg, "--")
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "=", 2)
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return trimmed, "", false
}
