variable "region" {
  type = string
}

variable "compute_class" {
  type = string
}

variable "instance_type" {
  type = string
}

variable "disk_size_gb" {
  type = number
}

variable "network_mode" {
  type = string
}

variable "image_id" {
  type = string
}

variable "ssh_key_name" {
  type    = string
  default = ""
}

variable "ssh_public_key" {
  type    = string
  default = ""
}

variable "ssh_cidr" {
  type    = string
  default = ""
}

variable "ssh_user" {
  type    = string
  default = "ubuntu"
}

variable "name_prefix" {
  type    = string
  default = "openclaw"
}

variable "use_nemoclaw" {
  type    = bool
  default = false
}

variable "nim_endpoint" {
  type    = string
  default = ""
}

variable "model" {
  type    = string
  default = ""
}
