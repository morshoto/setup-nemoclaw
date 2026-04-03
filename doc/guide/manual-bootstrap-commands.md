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

## 2. Create the Terraform infrastructure

The Terraform module lives in `infra/aws/ec2`.

### Initialize Terraform

```bash
terraform -chdir=infra/aws/ec2 init
```

### Plan the stack

Fill in the values you want for your environment:

```bash
terraform -chdir=infra/aws/ec2 plan \
  -var='region=ap-northeast-1' \
  -var='compute_class=gpu' \
  -var='instance_type=g5.xlarge' \
  -var='disk_size_gb=20' \
  -var='network_mode=public' \
  -var='image_id=ami-xxxxxxxxxxxxxxxxx' \
  -var='ssh_key_name=openclaw' \
  -var='ssh_public_key=ssh-ed25519 AAAA... your-key' \
  -var='ssh_cidr=203.0.113.0/32' \
  -var='ssh_user=ubuntu' \
  -var='name_prefix=openclaw' \
  -var='use_nemoclaw=true' \
  -var='nim_endpoint=http://localhost:11434' \
  -var='model=llama3.2'
```

### Apply the stack

```bash
terraform -chdir=infra/aws/ec2 apply \
  -var='region=ap-northeast-1' \
  -var='compute_class=gpu' \
  -var='instance_type=g5.xlarge' \
  -var='disk_size_gb=20' \
  -var='network_mode=public' \
  -var='image_id=ami-xxxxxxxxxxxxxxxxx' \
  -var='ssh_key_name=openclaw' \
  -var='ssh_public_key=ssh-ed25519 AAAA... your-key' \
  -var='ssh_cidr=203.0.113.0/32' \
  -var='ssh_user=ubuntu' \
  -var='name_prefix=openclaw' \
  -var='use_nemoclaw=true' \
  -var='nim_endpoint=http://localhost:11434' \
  -var='model=llama3.2'
```

### Recreate from scratch

If you want to tear everything down and rebuild it:

```bash
terraform -chdir=infra/aws/ec2 destroy \
  -var='region=ap-northeast-1' \
  -var='compute_class=gpu' \
  -var='instance_type=g5.xlarge' \
  -var='disk_size_gb=20' \
  -var='network_mode=public' \
  -var='image_id=ami-xxxxxxxxxxxxxxxxx' \
  -var='ssh_key_name=openclaw' \
  -var='ssh_public_key=ssh-ed25519 AAAA... your-key' \
  -var='ssh_cidr=203.0.113.0/32' \
  -var='ssh_user=ubuntu' \
  -var='name_prefix=openclaw' \
  -var='use_nemoclaw=true' \
  -var='nim_endpoint=http://localhost:11434' \
  -var='model=llama3.2'
```

## 3. Connect to the instance

After Terraform finishes, use the printed connection info or the EC2 public IP.

```bash
ssh -i ~/.ssh/id_ed25519 ubuntu@<public-ip>
```

If the instance is private, connect from a bastion or SSM session instead.

## 4. Install Docker on the host

The runtime checks expect Docker to be present.

```bash
sudo apt-get update
sudo apt-get install -y docker.io
sudo systemctl enable --now docker
sudo usermod -aG docker ubuntu
newgrp docker
docker info
```

If you are using a GPU instance, you can also check the NVIDIA driver path:

```bash
nvidia-smi -L
```

## 5. Authenticate Codex locally

If you want to use the Codex CLI on your workstation, run:

```bash
codex --login
```

This opens the browser-based sign-in flow and stores the local Codex credential cache.

If you need to troubleshoot the OAuth flow, see:

- https://note.com/akira_papa_ai/n/ne3a82fe5205f
- https://zenn.dev/aria3/articles/openclaw-oauth-troubleshooting

## 6. Verify the host

Once Docker is installed and the runtime is ready, verify the machine:

```bash
docker info
```

For a GPU host, also check:

```bash
nvidia-smi
```

## Notes

- This is the manual path. The Go CLI automates most of these steps.
- The Terraform commands above need a real AMI ID.
- If you are rebuilding often, keep the variable set in a `terraform.tfvars` file instead of pasting long command lines.
