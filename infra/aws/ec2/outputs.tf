output "instance_id" {
  value = aws_instance.this.id
}

output "public_ip" {
  value = aws_instance.this.public_ip
}

output "private_ip" {
  value = aws_instance.this.private_ip
}

output "connection_info" {
  value = var.network_mode == "public" ? "ssh -i <your-key>.pem ${var.ssh_user}@${aws_instance.this.public_ip}" : "private IP access: ${aws_instance.this.private_ip}"
}

output "security_group_id" {
  value = aws_security_group.this.id
}

output "security_group_rules" {
  value = local.security_group_rules
}

output "region" {
  value = var.region
}

output "network_mode" {
  value = var.network_mode
}
