// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"github.com/konradasb/rungar/internal/provider/aws"
	"github.com/konradasb/rungar/internal/provider/dicer"
	"github.com/konradasb/rungar/internal/provider/gcp"
	"github.com/konradasb/rungar/internal/provider/proxmox"
)

// Each provider type's config implements the optional interfaces it is meant
// to, which a renamed method would otherwise silently stop.
var (
	_ filesChecker     = (*aws.Config)(nil)
	_ secretFilesNamer = (*aws.Config)(nil)

	_ filesChecker     = (*dicer.Config)(nil)
	_ secretFilesNamer = (*dicer.Config)(nil)

	_ filesChecker      = (*gcp.Config)(nil)
	_ secretFilesNamer  = (*gcp.Config)(nil)
	_ runnerNameChecker = (*gcp.Config)(nil)

	_ filesChecker     = (*proxmox.Config)(nil)
	_ secretFilesNamer = (*proxmox.Config)(nil)
	_ secretKeysNamer  = (*proxmox.Config)(nil)
)
