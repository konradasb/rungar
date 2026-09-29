# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

# A Compute Engine image for the gcp provider's default startup script: Debian
# 12 with the Actions runner in /home/runner, owned by the runner user. See
# docs/content/docs/providers/gcp/building-an-image.md.
#
#   packer init build/runner-image/gcp
#   packer build -var project=my-ci-123456 build/runner-image/gcp

packer {
  required_plugins {
    googlecompute = {
      source  = "github.com/hashicorp/googlecompute"
      version = "~> 1.1"
    }
  }
}

variable "project" {
  type        = string
  description = "The project the image is made in, and built in."
}

variable "zone" {
  type        = string
  default     = "europe-west4-a"
  description = "The zone the build's instance runs in."
}

variable "runner_version" {
  type        = string
  default     = "2.337.0"
  description = "The Actions runner release, without the v."
}

variable "arch" {
  type        = string
  default     = "x64"
  description = "x64, or arm64 for Arm machine types such as t2a and c4a."

  validation {
    condition     = contains(["x64", "arm64"], var.arch)
    error_message = "The arch must be x64 or arm64."
  }
}

variable "image_family" {
  type        = string
  default     = "actions-runner"
  description = "The image family: a runner block's image names the family, and gets its newest image."
}

variable "sudo" {
  type        = bool
  default     = true
  description = "Gives the runner user sudo without a password, as GitHub's hosted runners have."
}

locals {
  version_tag = replace(var.runner_version, ".", "-")
  arm         = var.arch == "arm64"
}

source "googlecompute" "runner" {
  project_id              = var.project
  zone                    = var.zone
  source_image_family     = local.arm ? "debian-12-arm64" : "debian-12"
  source_image_project_id = ["debian-cloud"]
  machine_type            = local.arm ? "t2a-standard-1" : "e2-standard-2"
  preemptible             = true
  disk_size               = 10
  disk_type               = "pd-balanced"
  ssh_username            = "packer"

  # The build needs no Google Cloud API, and so no service account.
  disable_default_service_account = true

  image_name        = "actions-runner-${local.version_tag}-${var.arch}-${formatdate("YYYYMMDDhhmmss", timestamp())}"
  image_family      = local.arm ? "${var.image_family}-arm64" : var.image_family
  image_description = "Actions runner ${var.runner_version} (${var.arch}) on Debian 12, for Rungar"
  image_labels = {
    actions-runner = local.version_tag
    arch           = var.arch
  }
}

build {
  sources = ["source.googlecompute.runner"]

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
