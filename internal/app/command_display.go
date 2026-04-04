package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

const (
	commandColorBlue  = "\x1b[34m"
	commandColorReset = "\x1b[0m"
)

type commandFormatter struct {
	color bool
}

func newCommandFormatter(w io.Writer) commandFormatter {
	return commandFormatter{color: isTerminalWriter(w)}
}

func commandRef(out io.Writer, parts ...string) string {
	return newCommandFormatter(out).Ref(parts...)
}

func (f commandFormatter) Ref(parts ...string) string {
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text == "" || !f.color {
		return text
	}
	return commandColorBlue + text + commandColorReset
}

func (f commandFormatter) PaddedName(name string, width int) string {
	name = strings.TrimSpace(name)
	if width < len(name) {
		width = len(name)
	}
	padding := strings.Repeat(" ", width-len(name))
	if !f.color {
		return name + padding
	}
	return commandColorBlue + name + commandColorReset + padding
}

func (f commandFormatter) GroupTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" || !f.color {
		return title
	}
	return commandColorBlue + title + commandColorReset
}

func (f commandFormatter) Example(parts ...string) string {
	return f.Ref(parts...)
}

var rootCommandGroups = []*cobra.Group{
	{ID: "setup", Title: "Setup"},
	{ID: "provision", Title: "Provision"},
	{ID: "runtime", Title: "Runtime"},
	{ID: "integrations", Title: "Integrations"},
	{ID: "inspect", Title: "Inspect"},
	{ID: "support", Title: "Support"},
}

func configureRootCommandDisplay(rootCmd *cobra.Command) {
	for _, group := range rootCommandGroups {
		rootCmd.AddGroup(group)
	}
	rootCmd.SetHelpCommandGroupID("support")
	rootCmd.SetCompletionCommandGroupID("support")
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printRootHelp(cmd.OutOrStdout(), cmd)
	})
}

func printRootHelp(out io.Writer, cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	format := newCommandFormatter(out)

	if cmd.Short != "" {
		fmt.Fprintln(out, cmd.Short)
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, "Usage:")
	if cmd.Runnable() {
		fmt.Fprintf(out, "  %s\n", cmd.UseLine())
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(out, "  %s [command]\n", cmd.CommandPath())
	}

	if cmd.HasAvailableSubCommands() {
		cmds := cmd.Commands()
		width := 0
		for _, subcmd := range cmds {
			if subcmd.IsAvailableCommand() || subcmd.Name() == "help" {
				if len(subcmd.Name()) > width {
					width = len(subcmd.Name())
				}
			}
		}

		fmt.Fprintln(out)
		fmt.Fprintln(out, "Commands:")
		printed := false
		for _, group := range cmd.Groups() {
			groupPrinted := false
			for _, subcmd := range cmds {
				if subcmd.GroupID == group.ID && (subcmd.IsAvailableCommand() || subcmd.Name() == "help") {
					if !groupPrinted {
						fmt.Fprintf(out, "\n%s\n", format.GroupTitle(group.Title))
						groupPrinted = true
						printed = true
					}
					fmt.Fprintf(out, "  %s %s\n", format.PaddedName(subcmd.Name(), width), subcmd.Short)
				}
			}
		}
		if !cmd.AllChildCommandsHaveGroup() {
			additionalPrinted := false
			for _, subcmd := range cmds {
				if subcmd.GroupID == "" && (subcmd.IsAvailableCommand() || subcmd.Name() == "help") {
					if !additionalPrinted {
						fmt.Fprintln(out)
						fmt.Fprintln(out, "Additional Commands:")
						additionalPrinted = true
						printed = true
					}
					fmt.Fprintf(out, "  %s %s\n", format.PaddedName(subcmd.Name(), width), subcmd.Short)
				}
			}
		}
		if !printed {
			fmt.Fprintln(out, "  (no commands available)")
		}
	}

	if cmd.HasAvailableLocalFlags() {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		fmt.Fprint(out, trimRightSpace(cmd.LocalFlags().FlagUsages()))
	}
	if cmd.HasAvailableInheritedFlags() {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Global Flags:")
		fmt.Fprint(out, trimRightSpace(cmd.InheritedFlags().FlagUsages()))
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(out, "\nUse %s for more information about a command.\n", format.Ref(cmd.CommandPath(), "[command]", "--help"))
	}
}

func trimRightSpace(s string) string {
	return strings.TrimRight(s, " \t\r\n")
}
