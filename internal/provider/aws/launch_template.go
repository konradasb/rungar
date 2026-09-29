// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// launchTemplateRef is a launch template, by ID or by name, and its version:
// the template's default version when version is empty.
type launchTemplateRef struct {
	id, name, version string
}

var (
	// launchTemplateIDPattern matches a launch template's ID.
	launchTemplateIDPattern = regexp.MustCompile(`^lt-[0-9a-f]{8,17}$`)

	// launchTemplateNamePattern matches a launch template's name.
	launchTemplateNamePattern = regexp.MustCompile(`^[a-zA-Z0-9()./_-]{3,128}$`)

	// launchTemplateVersionPattern matches a launch template's version: a
	// number, $Latest or $Default.
	launchTemplateVersionPattern = regexp.MustCompile(`^([1-9][0-9]*|\$Latest|\$Default)$`)
)

// parseLaunchTemplateRef returns the launch template a runner block names: an
// ID or a name, either followed by a colon and a version, as in
// runner-gpu:3. A name holds no colon, so the first one starts the version.
func parseLaunchTemplateRef(s string) (launchTemplateRef, bool) {
	template, version, hasVersion := strings.Cut(s, ":")
	if hasVersion && !launchTemplateVersionPattern.MatchString(version) {
		return launchTemplateRef{}, false
	}

	switch {
	case launchTemplateIDPattern.MatchString(template):
		return launchTemplateRef{id: template, version: version}, true
	case launchTemplateNamePattern.MatchString(template):
		return launchTemplateRef{name: template, version: version}, true
	default:
		return launchTemplateRef{}, false
	}
}

// specification returns the template as RunInstances takes it.
func (r launchTemplateRef) specification() *ec2types.LaunchTemplateSpecification {
	spec := &ec2types.LaunchTemplateSpecification{}
	if r.id != "" {
		spec.LaunchTemplateId = aws.String(r.id)
	} else {
		spec.LaunchTemplateName = aws.String(r.name)
	}
	if r.version != "" {
		spec.Version = aws.String(r.version)
	}

	return spec
}
