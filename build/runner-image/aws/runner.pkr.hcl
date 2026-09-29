# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

# An AMI for the aws provider's default user data: Debian 12 with the Actions
# runner in /home/runner, owned by the runner user, installed by
# ../install-runner.sh as for the gcp provider. See
# docs/content/docs/providers/aws/building-an-image.md.
#
#   packer init build/runner-image/aws
#   packer build -var region=eu-north-1 build/runner-image/aws

packer {
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = "~> 1.3"
    }
  }
}

variable "region" {
  type        = string
  description = "The region the AMI is made in, and built in."
}

variable "runner_version" {
  type        = string
  default     = "2.337.0"
  description = "The Actions runner release, without the v."
}

variable "arch" {
  type        = string
  default     = "x64"
  description = "x64, or arm64 for Graviton instance types such as t4g and m7g."

  validation {
    condition     = contains(["x64", "arm64"], var.arch)
    error_message = "The arch must be x64 or arm64."
  }
}

variable "sudo" {
  type        = bool
  default     = true
  description = "Gives the runner user sudo without a password, as GitHub's hosted runners have."
}

locals {
  arm = var.arch == "arm64"
}

source "amazon-ebs" "runner" {
  region        = var.region
  instance_type = local.arm ? "t4g.small" : "t3.small"
  ssh_username  = "admin"

  # Debian's own AMIs, published by its account.
  source_ami_filter {
    owners      = ["136693071363"]
    most_recent = true
    filters = {
      name                = local.arm ? "debian-12-arm64-*" : "debian-12-amd64-*"
      virtualization-type = "hvm"
    }
  }

  launch_block_device_mappings {
    device_name           = "/dev/xvda"
    volume_size           = 8
    volume_type           = "gp3"
    delete_on_termination = true
  }

  ami_name        = "actions-runner-${var.runner_version}-${var.arch}-${formatdate("YYYYMMDDhhmmss", timestamp())}"
  ami_description = "Actions runner ${var.runner_version} (${var.arch}) on Debian 12, for Rungar"
  tags = {
    actions-runner = var.runner_version
    arch           = var.arch
  }
}

build {
  sources = ["source.amazon-ebs.runner"]

  provisioner "shell" {
    script          = "${path.root}/../install-runner.sh"
    execute_command = "sudo env {{ .Vars }} bash '{{ .Path }}'"
    environment_vars = [
      "RUNNER_VERSION=${var.runner_version}",
      "RUNNER_ARCH=${var.arch}",
      "RUNNER_SUDO=${var.sudo}",
    ]
  }
}
