# Manual Bootstrap Commands

This guide lists the commands you can run before relying on the Go CLI for automation.
It is written for the AWS path used by this repository.

## 1. Sign in to AWS

Use your AWS profile first:

```bash
aws sso login --profile sso-dev
aws sts get-caller-identity --profile sso-dev
```

If the identity call succeeds, your AWS credentials are ready.

## 2. Generate Terraform variables from YAML

The Terraform module lives in `infra/aws/ec2`.

Generate a `terraform.tfvars` file from your OpenClaw config:

```bash
openclaw infra tfvars --config openclaw.yaml --output infra/aws/ec2/terraform.tfvars
```

If you want to pin the AWS profile explicitly, pass `--profile sso-dev`.
If you omit it and run interactively, the CLI will prompt you to choose a profile or type one in.

This command reads the YAML config, resolves the SSH public key, stages the current working tree as a bootstrap archive, and writes Terraform-compatible `terraform.tfvars` variables.
It also carries the configured GitHub SSH private key path onto the EC2 bootstrap so the host can clone private repositories after startup.
The generated file includes deploy-time values such as `aws_profile`, `runtime_port`, `runtime_cidr`, and `source_archive_url`, so Terraform can create the EC2 instance and leave runtime installation to the SSH-based `install` stage.
Treat it as a deploy helper rather than a pure formatter: it depends on a usable SSH private key path, a resolvable AWS profile, the current git worktree state, and a GitHub SSH key if you want the host to clone private repos.

## 3. Create the Terraform infrastructure

```bash
terraform -chdir=infra/aws/ec2 init
terraform -chdir=infra/aws/ec2 plan -var-file=terraform.tfvars
terraform -chdir=infra/aws/ec2 apply -var-file=terraform.tfvars
```

### Recreate from scratch

If you want to tear everything down and rebuild it:

```bash
terraform -chdir=infra/aws/ec2 destroy -var-file=terraform.tfvars
```

## 4. Connect to the instance

After Terraform finishes, use the printed connection info or the EC2 public IP.

```bash
ssh -i ~/.ssh/id_ed25519 ubuntu@<public-ip>
```

If the instance is private, connect from a bastion or SSM session instead.

## 5. Wait for bootstrap

The EC2 user-data script prepares the host, writes the runtime config, and marks bootstrap complete.
You can watch the bootstrap log over SSH if you want to inspect what happened:

```bash
sudo tail -f /var/log/openclaw-bootstrap.log
```

When bootstrap completes, the marker file appears:

```bash
test -f /opt/openclaw/bootstrap.done
```

## 6. Authenticate Codex locally

If you want to use the Codex CLI on your workstation, run:

```bash
openclaw onboard --auth-choice openai-codex
```

This opens the browser-based sign-in flow and stores the local Codex credential cache.
If you prefer to invoke the CLI directly, `codex --login` is equivalent.
You do not need to provide an OpenAI API key for this path.

If you need to troubleshoot the OAuth flow, see:

- https://note.com/akira_papa_ai/n/ne3a82fe5205f
- https://zenn.dev/aria3/articles/openclaw-oauth-troubleshooting

## 7. Verify the host

Once bootstrap is ready, verify the machine:

```bash
docker info
docker ps --filter name='^/openclaw$'
curl -fsS http://127.0.0.1:8080/healthz
```

For a GPU host, also check:

```bash
nvidia-smi
```

## Notes

- This is the manual path. The Go CLI automates most of these steps.
- The Terraform commands above can work with `image.name` only, but `plan` and `apply` still need AWS access to resolve the AMI.
- If you regenerate `terraform.tfvars` with a different AWS profile, the `aws_profile` value in the file changes too.
- If you are rebuilding often, keep the generated `terraform.tfvars` file around and regenerate it only when the YAML changes.
- `runtime_cidr` defaults to `0.0.0.0/0`, which keeps the runtime health endpoint publicly reachable for external verification.
