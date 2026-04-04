package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
	"openclaw/internal/slackbot"
)

var runSlackAdapter = slackbot.Run

func newSlackServeCommand(app *App) *cobra.Command {
	var runtimeURL string
	var botToken string
	var appToken string
	var botUserID string
	var allowedChannels []string
	var requestTimeout time.Duration
	var agentsDir string
	var debug bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Slack Socket Mode adapter",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := strings.TrimSpace(app.opts.ConfigPath)
			if configPath == "" {
				session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
				selectedConfigPath, err := selectAgentConfigPath(session, agentsDir)
				if err != nil {
					return err
				}
				configPath = selectedConfigPath
			}
			agentCfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			agentEnvPath := agentEnvPathFromConfigPath(configPath)
			agentEnv, err := loadAgentEnvFile(agentEnvPath)
			if err != nil {
				return err
			}

			fileValue := func(key string) string {
				if agentEnv == nil {
					return ""
				}
				return strings.TrimSpace(agentEnv[key])
			}
			slackRuntimeURL := strings.TrimSpace(agentCfg.Slack.RuntimeURL)
			slackBotUserID := strings.TrimSpace(agentCfg.Slack.BotUserID)
			slackAllowedChannels := append([]string(nil), agentCfg.Slack.AllowedChannels...)

			cfg := slackbot.Config{
				RuntimeURL:      firstNonEmpty(runtimeURL, slackRuntimeURL, os.Getenv("OPENCLAW_RUNTIME_URL")),
				BotToken:        firstNonEmpty(botToken, fileValue("SLACK_BOT_TOKEN"), os.Getenv("SLACK_BOT_TOKEN")),
				AppToken:        firstNonEmpty(appToken, fileValue("SLACK_APP_TOKEN"), os.Getenv("SLACK_APP_TOKEN")),
				BotUserID:       firstNonEmpty(botUserID, slackBotUserID, fileValue("SLACK_BOT_USER_ID"), os.Getenv("SLACK_BOT_USER_ID")),
				AllowedChannels: firstNonEmptyStrings(allowedChannels, slackAllowedChannels, splitCommaList(fileValue("SLACK_ALLOWED_CHANNELS")), splitCommaList(os.Getenv("SLACK_ALLOWED_CHANNELS"))),
				RequestTimeout:  requestTimeout,
				Debug:           debug || app.opts.Debug,
			}
			if err := runSlackAdapter(cmd.Context(), cfg, cmd.OutOrStdout()); err != nil {
				return wrapUserFacingError(
					"slack adapter failed",
					err,
					"the Slack app token, bot token, or runtime URL is misconfigured",
					fmt.Sprintf("check %s for tokens or edit %s (slack.runtime_url) for the runtime URL", envPathForMessage(agentEnvPath), configPath),
					"confirm the runtime server is reachable from the adapter host",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&runtimeURL, "runtime-url", "", "URL of the OpenClaw runtime server")
	cmd.Flags().StringVar(&botToken, "bot-token", "", "Slack bot token; defaults to SLACK_BOT_TOKEN")
	cmd.Flags().StringVar(&appToken, "app-token", "", "Slack app-level token; defaults to SLACK_APP_TOKEN")
	cmd.Flags().StringVar(&botUserID, "bot-user-id", "", "Slack bot user id; defaults to SLACK_BOT_USER_ID or AuthTest")
	cmd.Flags().StringSliceVar(&allowedChannels, "allowed-channel", nil, "channel ID allowed to trigger the bot; repeatable")
	cmd.Flags().DurationVar(&requestTimeout, "request-timeout", 30*time.Second, "timeout for runtime requests")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().BoolVar(&debug, "debug", false, "enable Slack adapter debug logging")

	return cmd
}

func envPathForMessage(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "the selected agent .env file"
	}
	return path
}

func firstNonEmptyStrings(groups ...[]string) []string {
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		out := make([]string, 0, len(group))
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			out = append(out, value)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func splitCommaList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
