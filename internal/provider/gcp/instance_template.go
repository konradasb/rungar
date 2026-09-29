// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"fmt"
	"regexp"

	"google.golang.org/api/compute/v1"
)

// instanceTemplateRef is an instance template: global when region is empty.
type instanceTemplateRef struct {
	project, region, name string
}

// instanceTemplateURLPattern matches an instance template's URL, global or
// regional, with or without the API's prefix.
var instanceTemplateURLPattern = regexp.MustCompile(
	`^(?:https://(?:www\.|compute\.)googleapis\.com/compute/v1/)?` +
		`projects/([^/]+)/(?:global|regions/([^/]+))/instanceTemplates/([^/]+)$`)

// parseInstanceTemplateRef returns the instance template a runner block names:
// by URL, or by name, as a global template in project.
func parseInstanceTemplateRef(nameOrURL, project string) (instanceTemplateRef, bool) {
	if m := instanceTemplateURLPattern.FindStringSubmatch(nameOrURL); m != nil {
		return instanceTemplateRef{project: m[1], region: m[2], name: m[3]}, true
	}
	if resourceNamePattern.MatchString(nameOrURL) {
		return instanceTemplateRef{project: project, name: nameOrURL}, true
	}

	return instanceTemplateRef{}, false
}

// path returns the template as the path an instance is created from.
func (r instanceTemplateRef) path() string {
	if r.region == "" {
		return "projects/" + r.project + "/global/instanceTemplates/" + r.name
	}

	return "projects/" + r.project + "/regions/" + r.region + "/instanceTemplates/" + r.name
}

// instanceTemplateProperties returns the properties of an instance template:
// what an instance created from it has. A template cannot be changed, so each
// is read from Compute Engine once and kept.
func (p *Provider) instanceTemplateProperties(ctx context.Context, ref instanceTemplateRef) (*compute.InstanceProperties, error) {
	key := ref.path()

	p.mu.Lock()
	properties, ok := p.instanceTemplates[key]
	p.mu.Unlock()
	if ok {
		return properties, nil
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	var instanceTemplate *compute.InstanceTemplate
	var err error
	if ref.region == "" {
		instanceTemplate, err = p.compute.InstanceTemplates.Get(ref.project, ref.name).Context(ctx).Do()
	} else {
		instanceTemplate, err = p.compute.RegionInstanceTemplates.Get(ref.project, ref.region, ref.name).Context(ctx).Do()
	}
	if err != nil {
		return nil, fmt.Errorf("instance template %s: %w", key, err)
	}

	properties = instanceTemplate.Properties
	if properties == nil {
		properties = &compute.InstanceProperties{}
	}

	p.mu.Lock()
	if p.instanceTemplates == nil {
		p.instanceTemplates = map[string]*compute.InstanceProperties{}
	}
	p.instanceTemplates[key] = properties
	p.mu.Unlock()

	return properties, nil
}
