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
  region = var.region
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
  vpc_id     = length(data.aws_vpcs.default.ids) > 0 ? data.aws_vpcs.default.ids[0] : ""
  subnet_ids = length(data.aws_subnets.default_for_az.ids) > 0 ? data.aws_subnets.default_for_az.ids : data.aws_subnets.any.ids
  subnet_id  = length(local.subnet_ids) > 0 ? local.subnet_ids[0] : ""

  security_group_rules = var.network_mode == "public" && trimspace(var.ssh_cidr) != "" ? [
    "allow tcp/22 from ${trimspace(var.ssh_cidr)}",
  ] : [
    "no inbound rules configured",
  ]
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

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "this" {
  ami                         = var.image_id
  instance_type               = var.instance_type
  key_name                    = trimspace(var.ssh_key_name)
  subnet_id                   = local.subnet_id
  associate_public_ip_address = var.network_mode == "public"
  vpc_security_group_ids      = [aws_security_group.this.id]

  root_block_device {
    volume_size           = var.disk_size_gb
    delete_on_termination = true
  }

  tags = {
    Name = "${var.name_prefix}-${random_id.suffix.hex}"
  }
}
