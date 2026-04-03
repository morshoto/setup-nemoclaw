# Codex Onboarding Flow

This document describes the local authentication step used before any AWS provisioning work.
It matches the `openai-codex` path shown in the reference articles.

If you want the full command sequence for AWS, Terraform, Docker, and Codex setup, see [Manual Bootstrap Commands](./manual-bootstrap-commands.md).

## Goal

Set up Codex authentication on your local machine first, then move on to the CLI workflow that provisions or configures OpenClaw.

## Prerequisites

- The `codex` CLI is installed and available on `PATH`
- A browser is available on the machine where you run the command
- You are signed in to ChatGPT in that browser

## Command

```bash
openclaw onboard --auth-choice openai-codex
```

## What Happens

1. `openclaw` starts the Codex onboarding flow.
2. The CLI launches the Codex login flow.
3. Your browser opens and you sign in with ChatGPT.
4. After the browser callback completes, the CLI records that Codex authentication is configured.

## Expected Result

When the command succeeds, you should see a confirmation message such as:

```text
Codex authentication configured
```

At that point, Codex is authenticated locally on your machine.

## What This Does Not Do

- It does not provision AWS infrastructure.
- It does not create EC2 instances.
- It does not install the runtime on a remote host.

Those steps are handled separately by the AWS provisioning commands.

## Recommended Next Step

After Codex authentication is ready, run the AWS provisioning workflow you need for your environment.
For example, you can continue with the interactive setup flow or the non-interactive create flow depending on your config.

## Notes

- If the browser does not open automatically, copy the local callback URL from the terminal and complete the login manually.
- If you previously authenticated with a different method, re-run the onboarding command to refresh the local Codex login.

## References

- https://note.com/akira_papa_ai/n/ne3a82fe5205f
- https://zenn.dev/aria3/articles/openclaw-oauth-troubleshooting
