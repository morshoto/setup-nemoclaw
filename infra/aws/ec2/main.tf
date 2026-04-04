terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.6"
    }
  }
}

provider "aws" {
  region  = var.region
  profile = trimspace(var.aws_profile) != "" ? trimspace(var.aws_profile) : null
}

data "aws_vpcs" "default" {
  filter {
    name   = "is-default"
    values = ["true"]
  }
}

data "aws_subnets" "default_for_az" {
  filter {
    name   = "vpc-id"
    values = [local.vpc_id]
  }

  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

data "aws_subnets" "any" {
  filter {
    name   = "vpc-id"
    values = [local.vpc_id]
  }
}

locals {
  vpc_id           = length(data.aws_vpcs.default.ids) > 0 ? data.aws_vpcs.default.ids[0] : ""
  subnet_ids       = length(data.aws_subnets.default_for_az.ids) > 0 ? data.aws_subnets.default_for_az.ids : data.aws_subnets.any.ids
  subnet_id        = length(local.subnet_ids) > 0 ? local.subnet_ids[0] : ""
  image_name       = trimspace(var.image_name)
  image_id         = trimspace(var.image_id)
  listen_port      = var.runtime_port > 0 ? var.runtime_port : 8080
  runtime_cidr     = trimspace(var.runtime_cidr) != "" ? trimspace(var.runtime_cidr) : trimspace(var.ssh_cidr)
  runtime_provider = trimspace(var.runtime_provider)
  runtime_config_yaml = yamlencode({
    use_nemoclaw = var.use_nemoclaw
    nim_endpoint = var.nim_endpoint
    model        = var.model
    port         = local.listen_port
    provider     = local.runtime_provider
    region       = var.region
    sandbox = {
      enabled          = true
      network_mode     = var.network_mode
      filesystem_allow = []
    }
  })
  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    container_name      = trimspace(var.container_name)
    listen_port         = local.listen_port
    runtime_provider    = local.runtime_provider
    runtime_config_yaml = local.runtime_config_yaml
    github_private_key  = trimspace(var.github_private_key)
    source_archive_url  = trimspace(var.source_archive_url)
  })

  security_group_rules = var.network_mode == "public" && trimspace(var.ssh_cidr) != "" ? [
    "allow tcp/22 from ${trimspace(var.ssh_cidr)}",
    "allow tcp/${local.listen_port} from ${local.runtime_cidr}",
    ] : [
    "no inbound rules configured",
  ]
}

data "aws_ssm_parameter" "ubuntu_2204" {
  count = local.image_id == "" && local.image_name == "Ubuntu 22.04 LTS" ? 1 : 0
  name  = "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id"
}

data "aws_ssm_parameter" "dlami_gpu_2204" {
  count = local.image_id == "" && local.image_name == "AWS Deep Learning AMI GPU Ubuntu 22.04" ? 1 : 0
  name  = "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id"
}

data "aws_iam_policy_document" "bedrock_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "bedrock" {
  count              = local.runtime_provider == "aws-bedrock" ? 1 : 0
  name               = "${var.name_prefix}-${random_id.suffix.hex}-bedrock"
  assume_role_policy = data.aws_iam_policy_document.bedrock_assume_role.json
}

resource "aws_iam_role_policy" "bedrock" {
  count = local.runtime_provider == "aws-bedrock" ? 1 : 0
  name  = "${var.name_prefix}-${random_id.suffix.hex}-bedrock"
  role  = aws_iam_role.bedrock[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "bedrock:Converse",
        "bedrock:InvokeModel",
        "bedrock:InvokeModelWithResponseStream",
      ]
      Resource = "*"
    }]
  })
}

resource "aws_iam_instance_profile" "bedrock" {
  count = local.runtime_provider == "aws-bedrock" ? 1 : 0
  name  = "${var.name_prefix}-${random_id.suffix.hex}-bedrock"
  role  = aws_iam_role.bedrock[0].name
}

locals {
  resolved_image_id = local.image_id != "" ? local.image_id : (
    local.image_name == "Ubuntu 22.04 LTS" ? data.aws_ssm_parameter.ubuntu_2204[0].value :
    local.image_name == "AWS Deep Learning AMI GPU Ubuntu 22.04" ? data.aws_ssm_parameter.dlami_gpu_2204[0].value :
    ""
  )
}

resource "aws_key_pair" "this" {
  key_name   = "${trimspace(var.ssh_key_name)}-${random_id.suffix.hex}"
  public_key = trimspace(var.ssh_public_key)
}

resource "random_id" "suffix" {
  byte_length = 4
}

resource "aws_security_group" "this" {
  name        = "${var.name_prefix}-${random_id.suffix.hex}"
  description = "OpenClaw instance security group"
  vpc_id      = local.vpc_id

  dynamic "ingress" {
    for_each = var.network_mode == "public" && trimspace(var.ssh_cidr) != "" ? [1] : []
    content {
      description = "SSH access for OpenClaw"
      from_port   = 22
      to_port     = 22
      protocol    = "tcp"
      cidr_blocks = [trimspace(var.ssh_cidr)]
    }
  }

  dynamic "ingress" {
    for_each = var.network_mode == "public" && trimspace(local.runtime_cidr) != "" ? [1] : []
    content {
      description = "OpenClaw runtime access"
      from_port   = local.listen_port
      to_port     = local.listen_port
      protocol    = "tcp"
      cidr_blocks = [local.runtime_cidr]
    }
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "this" {
  ami                         = local.resolved_image_id
  instance_type               = var.instance_type
  key_name                    = aws_key_pair.this.key_name
  subnet_id                   = local.subnet_id
  associate_public_ip_address = var.network_mode == "public"
  vpc_security_group_ids      = [aws_security_group.this.id]
  iam_instance_profile        = local.runtime_provider == "aws-bedrock" ? aws_iam_instance_profile.bedrock[0].name : null
  user_data                   = local.user_data

  root_block_device {
    volume_size           = var.disk_size_gb
    delete_on_termination = true
  }

  tags = {
    Name = "${var.name_prefix}-${random_id.suffix.hex}"
  }
}
