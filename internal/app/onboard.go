package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var runCodexOAuthLogin = defaultRunCodexOAuthLogin

func newOnboardCommand(app *App) *cobra.Command {
	var authChoice string

	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Set up local Codex authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			choice := strings.ToLower(strings.TrimSpace(authChoice))
			if choice == "" {
				choice = "openai-codex"
			}
			switch choice {
			case "openai-codex":
				logger := loggerFromContext(cmd.Context())
				logger.Info("starting codex oauth onboarding")
				fmt.Fprintln(cmd.OutOrStdout(), "starting Codex OAuth login...")
				if err := runCodexOAuthLogin(cmd.Context(), cmd.OutOrStdout()); err != nil {
					return wrapUserFacingError(
						"onboard failed",
						err,
						"the Codex CLI is missing or the browser login did not complete",
						"install or update the Codex CLI, then run `openclaw onboard --auth-choice openai-codex` again",
					)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Codex authentication configured")
				return nil
			default:
				return fmt.Errorf("unsupported auth choice %q", authChoice)
			}
		},
	}

	cmd.Flags().StringVar(&authChoice, "auth-choice", "openai-codex", "authentication method to configure")
	return cmd
}

func defaultRunCodexOAuthLogin(ctx context.Context, out io.Writer) error {
	if _, err := exec.LookPath("codex"); err != nil {
		return errors.New("codex CLI is required")
	}

	if err := runCommand(ctx, out, "codex", "--login"); err != nil {
		return err
	}
	return nil
}

func runCommand(ctx context.Context, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
