// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// module is this module's path.
const module = "github.com/konradasb/rungar"

// Packages declaring the types a configuration is read into.
const (
	awsPackage     = module + "/internal/provider/aws"
	configPackage  = module + "/internal/config"
	dicerPackage   = module + "/internal/provider/dicer"
	dockerPackage  = module + "/internal/provider/docker"
	gcpPackage     = module + "/internal/provider/gcp"
	proxmoxPackage = module + "/internal/provider/proxmox"
	typesPackage   = module + "/internal/types"
)

// section is part of a reference page: an introduction, then the keys of one
// struct under a prefix.
type section struct {
	intro, from, typ, prefix string
}

// writeConfiguration writes the configuration reference: the daemon's keys, and
// each provider type's.
func writeConfiguration(root, dir string) error {
	l := &loader{root: root, packages: map[string]*goPackage{}, keys: map[string]bool{}, commands: commandPaths()}

	pages := []struct {
		path     string
		meta     meta
		sections []section
	}{
		{
			path: filepath.Join(dir, "configuration.md"),
			meta: meta{
				title: "Configuration", weight: 1, icon: "cog",
				description: "Every key of rungar's configuration file.",
			},
			sections: []section{{
				intro: "`rungar` reads its configuration from `/etc/rungar/config.yaml`, or the file `--config` " +
					"names, when it starts: a changed file is taken up by restarting it. Unknown keys are rejected, " +
					"and a file that does not load keeps the daemon from starting.\n\n" +
					"A provider's own keys, and the keys of the `runner` blocks, are its type's to read: see " +
					"[Providers]({{< relref \"/docs/reference/providers\" >}}). " +
					"[example.yml](https://github.com/konradasb/rungar/blob/main/example.yml) shows them together.\n\n" +
					"## General {#general}\n\n" +
					"The daemon's own settings. The sections after them are GitHub, the providers, the scale " +
					"sets, the metrics and the events.",
				from: configPackage, typ: "Config",
			}},
		},
		{
			path: filepath.Join(dir, "providers", "dicer.md"),
			meta: meta{
				title: "dicer", weight: 1, icon: "server",
				description: "The dicer provider: runners as virtual machines on a Dicer host.",
			},
			sections: []section{
				{
					intro: "A provider of type `dicer` is one [Dicer](https://github.com/konradasb/dicer) host, " +
						"reached on its API. Dicer runs on one machine and has no cluster to place instances " +
						"across, so a fleet of Dicer hosts is a provider per host, whose shared settings a YAML " +
						"anchor keeps in one place. Its keys sit beside the ones every provider has:\n\n" +
						"```yaml\n" +
						"providers:\n" +
						"  - &dicer\n" +
						"    name: compute1\n" +
						"    type: dicer\n" +
						"    address: 10.10.0.101:7443\n" +
						"    tls:\n" +
						"      ca_file: /etc/rungar/ca.pem\n" +
						"    runner:\n" +
						"      image: ghcr.io/actions/actions-runner:latest\n" +
						"  - <<: *dicer\n" +
						"    name: compute2\n" +
						"    address: 10.10.0.102:7443\n" +
						"```\n\n" +
						"## How it works {#how-it-works}\n\n" +
						"- **A runner** is a Dicer instance, booted from the `runner` block's image and started " +
						"with its `command`.\n" +
						"- **Capacity** is `dicerd`'s to decide, which by default lets vCPUs outnumber the host's " +
						"CPUs; a runner's vCPUs are only its own if `dicerd` is configured without overcommit. A " +
						"host that is full refuses an instance -- out of CPU, memory or disk, or a volume or port " +
						"the runner needs in use -- and the runner is tried on the scale set's next provider. One " +
						"that could never start it -- more vCPUs than the host has CPUs, or a kernel, network, " +
						"volume or image it does not have -- refuses it every time, and the scale set skips it for " +
						"a while each time.\n" +
						"- **The registration** is put in the instance's environment as " +
						"`ACTIONS_RUNNER_INPUT_JITCONFIG`, which the official runner image's start script reads. " +
						"`RUNNER_ALLOW_RUNASROOT` is set too, since the VM is the isolation; `env` can override " +
						"it. Whoever can use the host's API can read an instance's environment, so it can read a " +
						"registration until the runner has used it.\n" +
						"- **Labels**: Rungar's labels are the instance's labels, as they are, which instances are " +
						"found by.\n" +
						"- **The end of a runner**: the instance is never restarted, and `dicerd` removes it when " +
						"it stops, so a runner whose job ends while Rungar is not watching leaves nothing behind.\n" +
						"- **The image** is a container image carrying the Actions runner at " +
						"`/home/runner/run.sh`, or wherever `command` says. Pin it with `@sha256:` in `image`.\n" +
						"- **Credentials**: Rungar identifies itself to `dicerd` with the client certificate in " +
						"`tls`, and checks `dicerd`'s against `tls.ca_file`. Without `tls`, a TCP address is " +
						"plaintext and unauthenticated; a `unix://` address is protected by the socket's " +
						"permissions instead.\n\n" +
						"## Sizing runners {#sizing-runners}\n\n" +
						"- **vCPUs** are threads on the host. `dicerd` lets four share each CPU unless its " +
						"`resources.cpu_overcommit` says otherwise; for runners that build, 1 gives every vCPU a " +
						"CPU of its own. **Memory** is not overcommitted unless `resources.memory_overcommit` says " +
						"so. See Dicer's [capacity guide](https://dicer.sh/docs/guides/capacity/).\n" +
						"- **The disk** is 20 GiB unless `disk` says otherwise, and sparse: it takes space on the " +
						"host only as a job writes to it.\n" +
						"- **A volume** mounted read-write can be used by one running instance at a time, so only " +
						"a scale set whose `max_runners` is 1 may mount one, and no other scale set the same " +
						"volume. Mounted `read_only` by every runner, one volume can be shared by all of them. A " +
						"`tmpfs` mount counts against the runner's `memory`.\n" +
						"- **Docker** in a runner needs a daemon started by `command` before the runner, a kernel " +
						"with Docker's networking, and its data on a `tmpfs` at `/var/lib/docker`: on the " +
						"instance's own disk, an overlay already, Docker falls back to a storage driver that " +
						"copies every layer in full. See Dicer's guide to [running Docker inside an " +
						"instance](https://dicer.sh/docs/examples/running-docker-inside-an-instance/).\n\n" +
						"## Keys {#keys}\n\n" +
						"Beside the keys every provider has -- `name`, `type`, `weight`, `max_runners`, `disabled` " +
						"and `runner` -- see [Providers]({{< relref \"/docs/reference/providers\" >}}), a `dicer` " +
						"provider reads these. Its `runner` block's keys are [below](#runner).",
					from: dicerPackage, typ: "Config",
				},
				{
					intro: "\n## `runner` {#runner}\n\n*mapping*\n\n" +
						"The keys of a `runner` block, the provider's and each scale set's for it: a runner is a " +
						"virtual machine booted from a container image. A scale set's block is written over " +
						"the provider's, so every key can be given in either. A name given here -- a kernel, " +
						"a network, a volume -- must exist on the host, and on every host a block shared by " +
						"several is given to.",
					from: dicerPackage, typ: "RunnerSpec", prefix: "runner",
				},
			},
		},
		{
			path: filepath.Join(dir, "providers", "docker.md"),
			meta: meta{
				title: "docker", weight: 2, icon: "cube",
				description: "The docker provider: runners as containers on a Docker host.",
			},
			sections: []section{
				{
					intro: "A provider of type `docker` is one Docker host, reached on its daemon's Engine API: its " +
						"socket on the same machine, or its TCP listener with TLS. A daemon runs on one machine, " +
						"so a fleet of Docker hosts is a provider per host.\n\n" +
						"```yaml\n" +
						"providers:\n" +
						"  - name: build1\n" +
						"    type: docker\n" +
						"    address: tcp://10.10.0.111:2376\n" +
						"    max_runners: 8\n" +
						"    tls:\n" +
						"      ca_file: /etc/rungar/docker-ca.pem\n" +
						"      cert_file: /etc/rungar/docker-client.pem\n" +
						"      key_file: /etc/rungar/docker-client-key.pem\n" +
						"    runner:\n" +
						"      image: ghcr.io/actions/actions-runner:latest\n" +
						"      cpus: 2\n" +
						"      memory: 4GiB\n" +
						"```\n\n" +
						"## How it works {#how-it-works}\n\n" +
						"- **A runner** is a container of the `runner` block's image, never privileged, started " +
						"with its `command` and limited to its `cpus` and `memory`. An init process reaps what a " +
						"job leaves running.\n" +
						"- **Capacity** is not something Docker refuses a container for: set the provider's " +
						"`max_runners` to the number of runners the host should take. A host out of disk refuses " +
						"as full, and the runner is tried on the scale set's next provider. One that could never " +
						"start the runner -- an image, network, volume or path it lacks -- refuses it every time, " +
						"and the scale set skips it for a while each time.\n" +
						"- **The registration** is put in the container's environment as " +
						"`ACTIONS_RUNNER_INPUT_JITCONFIG`, which the official runner image's start script reads. " +
						"Whoever can use the daemon can read a container's environment, so it can read a " +
						"registration until the runner has used it.\n" +
						"- **Labels**: Rungar's labels are the container's labels, as they are, which containers " +
						"are found by.\n" +
						"- **The end of a runner**: the container is removed when it stops, so a runner whose job " +
						"ends while Rungar is not watching leaves nothing behind.\n" +
						"- **The image** must carry the Actions runner, as `ghcr.io/actions/actions-runner` does. " +
						"It is pulled when missing, or every time with `pull: always`, without registry " +
						"credentials: a private image must already be on the host.\n" +
						"- **Credentials**: Rungar identifies itself to the daemon with the client certificate in " +
						"`tls`. The daemon's API gives root on the host to whoever can reach it: without `tls`, a " +
						"TCP address is plaintext and unauthenticated, so reach a remote daemon over TLS with " +
						"client certificates.\n" +
						"- **Isolation** is a container's: every job shares the host's kernel. There is no way to " +
						"give a job the Docker socket -- a bind of the socket, of a directory holding it, or of " +
						"Docker's state under `/run/docker` is refused -- so jobs that need Docker belong on a " +
						"provider of virtual machines.\n\n" +
						"## Keys {#keys}\n\n" +
						"Beside the keys every provider has -- `name`, `type`, `weight`, `max_runners`, `disabled` " +
						"and `runner` -- see [Providers]({{< relref \"/docs/reference/providers\" >}}), a `docker` " +
						"provider reads these. Its `runner` block's keys are [below](#runner).",
					from: dockerPackage, typ: "Config",
				},
				{
					intro: "\n" +
						"## `runner` {#runner}\n\n" +
						"*mapping*\n\n" +
						"The keys of a `runner` block, the provider's and each scale set's for it: a runner is a " +
						"container. A scale set's block is written over the provider's, so every key can be given " +
						"in either. A network or volume named here must exist on the host.",
					from: dockerPackage, typ: "RunnerSpec", prefix: "runner",
				},
			},
		},
		{
			path: filepath.Join(dir, "providers", "proxmox.md"),
			meta: meta{
				title: "proxmox", weight: 3, icon: "server",
				description: "The proxmox provider: runners as virtual machines on a Proxmox VE cluster.",
			},
			sections: []section{
				{
					intro: "A provider of type `proxmox` is one Proxmox VE cluster, reached on its API with an API " +
						"token. The cluster is one provider however many nodes it has: the provider chooses the " +
						"node each runner's VM goes on.\n\n" +
						"```yaml\n" +
						"providers:\n" +
						"  - name: pve\n" +
						"    type: proxmox\n" +
						"    url: https://pve.example.com:8006\n" +
						"    token_id: rungar@pve!rungar\n" +
						"    token_secret_path: /etc/rungar/pve-token\n" +
						"    tls:\n" +
						"      ca_file: /etc/rungar/pve-root-ca.pem\n" +
						"    nodes: [pve1, pve2]\n" +
						"    runner:\n" +
						"      template: 9000\n" +
						"      cores: 4\n" +
						"      memory: 8GiB\n" +
						"```\n\n" +
						"## How it works {#how-it-works}\n\n" +
						"- **A runner** is a QEMU VM cloned from the `template` VM -- a linked clone unless " +
						"`full_clone` is set -- sized with `cores` and `memory`, and started.\n" +
						"- **Where it goes** is the provider's choice. Of the allowed nodes that are online, the " +
						"one with the most memory free that has enough cores and memory takes the VM; cores are " +
						"not counted, as Proxmox VE lets VMs share them. The template must be on storage every " +
						"allowed node can reach, or `nodes` must name only the template's node.\n" +
						"- **Capacity**: no node with the memory free now is **full**, and the runner is tried on " +
						"the scale set's next provider; so is a clone out of disk, or a start out of memory. No " +
						"node that could ever hold the runner -- more cores or memory than any node has, a " +
						"template that does not exist or is not a template, `nodes` the cluster does not have -- " +
						"and the provider refuses it every time, and the scale set skips it for a while each time.\n" +
						"- **The registration** is written to `jit_path` in the guest through its QEMU guest " +
						"agent, once the agent answers, for the template's runner unit to read.\n" +
						"- **Labels**: Rungar's labels are kept exactly, as JSON in the VM's description, with when " +
						"the VM was made. The VM is also tagged with a short hash of each label, since Proxmox VE " +
						"allows few characters in a tag: listing narrows VMs down by tag, and reads the " +
						"description of those alone.\n" +
						"- **The end of a runner**: the template powers the VM off when the runner exits, and " +
						"Rungar removes a stopped VM, disks and all.\n" +
						"- **The image** is the `template` VM; see [The template](#the-template).\n" +
						"- **Credentials** are the API token in `token_id`, and its secret. It needs VM.Allocate, " +
						"VM.Clone, VM.Config.*, VM.PowerMgmt, VM.Audit, the guest agent's privileges " +
						"(VM.GuestAgent.* on Proxmox VE 9, VM.Monitor before it), Datastore.AllocateSpace and " +
						"Sys.Audit, on the template, the nodes, and the storage and pool runners go in.\n\n" +
						"## The template {#the-template}\n\n" +
						"The template needs `qemu-guest-agent` installed and enabled -- the provider sets `agent: " +
						"1` on each clone -- the Actions runner, and a unit that waits for the registration, runs " +
						"the runner, and powers the VM off after it:\n\n" +
						"```ini\n" +
						"# /etc/systemd/system/rungar-runner.service\n" +
						"[Unit]\n" +
						"Description=GitHub Actions runner, registered by Rungar\n" +
						"After=network-online.target qemu-guest-agent.service\n" +
						"Wants=network-online.target\n\n" +
						"[Service]\n" +
						"Type=oneshot\n" +
						"ExecStart=/usr/local/bin/rungar-runner\n" +
						"# One job, then the VM goes: Rungar removes stopped VMs.\n" +
						"ExecStopPost=/usr/bin/systemctl poweroff\n\n" +
						"[Install]\n" +
						"WantedBy=multi-user.target\n" +
						"```\n\n" +
						"```sh\n" +
						"#!/bin/sh\n" +
						"# /usr/local/bin/rungar-runner\n" +
						"set -eu\n" +
						"jit=/run/rungar/jitconfig\n" +
						"while [ ! -s \"$jit\" ]; do sleep 1; done\n" +
						"export ACTIONS_RUNNER_INPUT_JITCONFIG=\"$(cat \"$jit\")\"\n" +
						"rm -f \"$jit\"\n" +
						"cd /home/runner\n" +
						"exec runuser -u runner -- ./run.sh\n" +
						"```\n\n" +
						"## Keys {#keys}\n\n" +
						"Beside the keys every provider has -- `name`, `type`, `weight`, `max_runners`, `disabled` " +
						"and `runner` -- see [Providers]({{< relref \"/docs/reference/providers\" >}}), a `proxmox` " +
						"provider reads these. Its `runner` block's keys are [below](#runner).",
					from: proxmoxPackage, typ: "Config",
				},
				{
					intro: "\n" +
						"## `runner` {#runner}\n\n" +
						"*mapping*\n\n" +
						"The keys of a `runner` block, the provider's and each scale set's for it: a runner is a " +
						"VM cloned from a template. A scale set's block is written over the provider's, so every " +
						"key can be given in either.",
					from: proxmoxPackage, typ: "RunnerSpec", prefix: "runner",
				},
			},
		},
		{
			path: filepath.Join(dir, "providers", "gcp.md"),
			meta: meta{
				title: "gcp", weight: 4, icon: "cloud",
				description: "The gcp provider: runners as Compute Engine instances.",
			},
			sections: []section{
				{
					intro: "A provider of type `gcp` is one Google Cloud project's Compute Engine, in one region: the " +
						"zones it lists, which it tries in order.\n\n" +
						"```yaml\n" +
						"providers:\n" +
						"  - name: gcp\n" +
						"    type: gcp\n" +
						"    project: my-ci-123456\n" +
						"    zones: [europe-west1-b, europe-west1-c, europe-west1-d]\n" +
						"    service_account: runner@my-ci-123456.iam.gserviceaccount.com\n" +
						"    max_runners: 20\n" +
						"    runner:\n" +
						"      image: projects/my-ci-123456/global/images/family/actions-runner\n\n" +
						"scale_sets:\n" +
						"  - name: rungar-c4-m8\n" +
						"    max_runners: 10\n" +
						"    placement: pack\n" +
						"    providers:\n" +
						"      - name: compute1\n" +
						"        runner: &c4-m8 {vcpus: 4, memory: 8GiB}\n" +
						"      - name: compute2\n" +
						"        runner: *c4-m8\n" +
						"      - name: gcp\n" +
						"        runner: {machine_type: e2-custom-4-8192}\n" +
						"```\n\n" +
						"A scale set spanning Dicer hosts and Compute Engine gives each provider its size in that " +
						"provider's terms: `vcpus` and `memory` for the hosts, a `machine_type` for the cloud.\n\n" +
						"## How it works {#how-it-works}\n\n" +
						"- **A runner** is a Compute Engine instance in one of the provider's zones, made from the " +
						"`runner` block's image and machine type, on the configured network. It is never " +
						"restarted, and runs on Spot capacity if `spot` is set.\n" +
						"- **Where it goes**: the zones are tried in order. A zone out of stock or out of quota " +
						"sends the runner to the next zone, after removing whatever it left; every zone full, and " +
						"the provider is full, and the runner is tried on the scale set's next provider. A machine " +
						"type or image that does not exist, or any other error, stops the zones being tried: the " +
						"scale set skips this provider for a while and tries its next.\n" +
						"- **Capacity** is Compute Engine's to decide: its quotas, and what each zone has in " +
						"stock, which it says only by refusing. Set the provider's `max_runners` to cap what the " +
						"cloud may cost.\n" +
						"- **The registration** is put in the instance's metadata as `rungar-jitconfig`. The " +
						"default startup script reads it from the metadata server and starts the Actions runner in " +
						"`/home/runner` as the `runner` user. Anything on the instance can read its metadata, so a " +
						"job can read the registration, which by then it has used.\n" +
						"- **Labels**: Rungar's labels are kept whole, as JSON, in the `rungar-labels` metadata " +
						"item, and each is also set as a Compute Engine label -- `rungar_sh_scale-set=rungar-c4-m8` " +
						"-- which is what instances are found by; a value Compute Engine does not allow is a hash " +
						"of it. Labels starting `rungar_` are Rungar's.\n" +
						"- **The end of a runner**: the default startup script powers the instance off when the " +
						"runner exits, and Rungar deletes the stopped instance.\n" +
						"- **The image** needs, for the default startup script, the Actions runner unpacked in " +
						"`/home/runner`, owned by a `runner` user; `curl`, `runuser` and `shutdown`; and Google's " +
						"guest environment, which runs `startup-script`, as Google's public images have.\n" +
						"- **Credentials** are Application Default Credentials, or a service account's key in " +
						"`credentials_file`. Rungar's identity needs `roles/compute.instanceAdmin.v1` on the " +
						"project; with `service_account` set, `roles/iam.serviceAccountUser` on that account; for " +
						"an image in another project, `roles/compute.imageUser` there; and on a Shared VPC, " +
						"`roles/compute.networkUser` on it.\n\n" +
						"## Keys {#keys}\n\n" +
						"Beside the keys every provider has -- `name`, `type`, `weight`, `max_runners`, `disabled` " +
						"and `runner` -- see [Providers]({{< relref \"/docs/reference/providers\" >}}), a `gcp` " +
						"provider reads these. Its `runner` block's keys are [below](#runner).",
					from: gcpPackage, typ: "Config",
				},
				{
					intro: "\n" +
						"## `runner` {#runner}\n\n" +
						"*mapping*\n\n" +
						"The keys of a `runner` block, the provider's and each scale set's for it: a runner is a " +
						"Compute Engine instance. A scale set's block is written over the provider's, so every key " +
						"can be given in either.",
					from: gcpPackage, typ: "RunnerSpec", prefix: "runner",
				},
			},
		},
		{
			path: filepath.Join(dir, "providers", "aws.md"),
			meta: meta{
				title: "aws", weight: 5, icon: "cloud",
				description: "The aws provider: runners as EC2 instances.",
			},
			sections: []section{
				{
					intro: "A provider of type `aws` is one AWS account's EC2, in one region: the subnets it lists, " +
						"each in one Availability Zone, which it tries in order.\n\n" +
						"```yaml\n" +
						"providers:\n" +
						"  - name: aws\n" +
						"    type: aws\n" +
						"    region: eu-west-1\n" +
						"    subnets: [subnet-0a1b2c3d4e5f60718, subnet-0f1e2d3c4b5a69788]\n" +
						"    security_groups: [sg-0a1b2c3d4e5f60718]\n" +
						"    max_runners: 20\n" +
						"    runner:\n" +
						"      image: ami-0a1b2c3d4e5f60718\n\n" +
						"scale_sets:\n" +
						"  - name: rungar-c4-m8\n" +
						"    max_runners: 10\n" +
						"    placement: pack\n" +
						"    providers:\n" +
						"      - name: compute1\n" +
						"        runner: {vcpus: 4, memory: 8GiB}\n" +
						"      - name: aws\n" +
						"        runner: {instance_type: m7i.xlarge}\n" +
						"```\n\n" +
						"A scale set spanning Dicer hosts and EC2 gives each provider its size in that provider's " +
						"terms: `vcpus` and `memory` for the hosts, an `instance_type` for the cloud.\n\n" +
						"## How it works {#how-it-works}\n\n" +
						"- **A runner** is an EC2 instance in one of the provider's subnets, booted from the " +
						"`runner` block's AMI with its instance type and root volume, and requiring IMDSv2. It " +
						"runs as a one-time Spot Instance, terminated when taken back, if `spot` is set.\n" +
						"- **Where it goes**: the subnets are tried in order. A zone out of capacity for the " +
						"instance type, a subnet out of addresses, or a zone that does not offer the type sends " +
						"the runner to the next subnet. Every subnet full, or the account out of a quota -- its " +
						"vCPU limit, which the region's subnets share, stops the subnets being tried -- and the " +
						"provider is full, and the runner is tried on the scale set's next provider. An AMI, " +
						"security group or parameter EC2 does not accept, an instance type no subnet offers, or " +
						"any other error, and the scale set skips this provider for a while and tries its next.\n" +
						"- **Capacity** is EC2's to decide: the account's quotas, and what each zone has, which it " +
						"says only by refusing. Set the provider's `max_runners` to cap what the cloud may cost.\n" +
						"- **The registration** is put in the instance's user data: Rungar exports it as " +
						"`ACTIONS_RUNNER_INPUT_JITCONFIG` after the first line of the `user_data` script. The " +
						"default script starts the Actions runner in `/home/runner` as the `runner` user. Anything " +
						"on the instance can read its user data, so a job can read the registration, which by then " +
						"it has used.\n" +
						"- **Labels**: Rungar's labels are the instance's tags as they are -- " +
						"`rungar.sh/scale-set=rungar-c4-m8` -- and its volume's; the `Name` tag is the runner's " +
						"name. Instances are found by these tags, in the provider's subnets, and an instance " +
						"shutting down or terminated is gone.\n" +
						"- **The end of a runner**: the default script powers the instance off when the runner " +
						"exits, and an instance that shuts down terminates, so it is never restarted.\n" +
						"- **The image** is an AMI, which needs, for the default user data, the Actions runner " +
						"unpacked in `/home/runner`, owned by a `runner` user; `runuser` and `shutdown`; and " +
						"cloud-init, which runs the user data, as the AMIs of the major distributions have. Its " +
						"root device is asked of EC2 once, to size the root volume.\n" +
						"- **Credentials** are the SDK's default chain -- the environment, the shared files, then " +
						"the role of the ECS task or EC2 instance Rungar runs on -- or the named `profile`. Rungar's " +
						"identity needs `ec2:RunInstances`, `ec2:CreateTags` on instances and volumes as they are " +
						"made, `ec2:DescribeInstances`, `ec2:DescribeImages` and `ec2:TerminateInstances`; with " +
						"`instance_profile` set, `iam:PassRole` on its role; and, for an AMI or volume encrypted " +
						"with a customer managed key, the key's grants.\n\n" +
						"## Keys {#keys}\n\n" +
						"Beside the keys every provider has -- `name`, `type`, `weight`, `max_runners`, `disabled` " +
						"and `runner` -- see [Providers]({{< relref \"/docs/reference/providers\" >}}), a `aws` " +
						"provider reads these. Its `runner` block's keys are [below](#runner).",
					from: awsPackage, typ: "Config",
				},
				{
					intro: "\n" +
						"## `runner` {#runner}\n\n" +
						"*mapping*\n\n" +
						"The keys of a `runner` block, the provider's and each scale set's for it: a runner is an " +
						"EC2 instance. A scale set's block is written over the provider's, so every key can be " +
						"given in either.",
					from: awsPackage, typ: "RunnerSpec", prefix: "runner",
				},
			},
		},
	}

	for _, p := range pages {
		for _, sec := range p.sections {
			if err := l.collectKeys(sec.from, sec.typ, sec.prefix); err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
			}
		}
	}

	for _, p := range pages {
		var body bytes.Buffer

		for _, sec := range p.sections {
			b, err := l.document(sec.from, sec.typ, sec.prefix, sec.intro)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
			}
			body.Write(b)
		}

		if err := writePage(p.path, p.meta, body.Bytes()); err != nil {
			return err
		}
	}

	return nil
}

// loader parses the packages a configuration's types are declared in.
type loader struct {
	root     string
	fset     token.FileSet
	packages map[string]*goPackage

	// keys are every key of every page, alone and under its prefix, and
	// commands every rungar command: what the text writes as code.
	keys, commands map[string]bool
}

// goPackage is a package's struct types.
type goPackage struct {
	path    string
	structs map[string]*structType
}

// structType is a struct as declared, with the imports its fields' types
// refer to.
type structType struct {
	pkg     *goPackage
	name    string
	fields  []*ast.Field
	imports map[string]string
}

// load parses a package of this module, without its tests.
func (l *loader) load(importPath string) (*goPackage, error) {
	if p, ok := l.packages[importPath]; ok {
		return p, nil
	}

	rel, ok := strings.CutPrefix(importPath, module+"/")
	if !ok {
		return nil, fmt.Errorf("%s is not part of %s", importPath, module)
	}

	dir := filepath.Join(l.root, filepath.FromSlash(rel))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	p := &goPackage{path: importPath, structs: map[string]*structType{}}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(&l.fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}

		imports := map[string]string{}
		for _, imp := range file.Imports {
			ipath, _ := strconv.Unquote(imp.Path.Value)
			alias := packageName(ipath)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			imports[alias] = ipath
		}

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}

			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					p.structs[ts.Name.Name] = &structType{
						pkg: p, name: ts.Name.Name, fields: st.Fields.List, imports: imports,
					}
				}
			}
		}
	}

	l.packages[importPath] = p

	return p, nil
}

// packageName returns the default name of the package at importPath: the last
// element of its path, less a version suffix.
func packageName(importPath string) string {
	name := path.Base(importPath)
	if i := strings.Index(name, ".v"); i > 0 {
		name = name[:i]
	}

	return name
}

// document returns the reference for a struct and every mapping under it, with
// its keys under prefix.
func (l *loader) document(importPath, name, prefix, intro string) ([]byte, error) {
	p, err := l.load(importPath)
	if err != nil {
		return nil, err
	}

	root, ok := p.structs[name]
	if !ok {
		return nil, fmt.Errorf("no struct %s in %s", name, importPath)
	}

	var out bytes.Buffer

	out.WriteString(intro + "\n")

	if err := l.writeMapping(&out, root, prefix, map[*structType]string{}); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// collectKeys adds a struct's keys, and those of every mapping under it, to
// l.keys: each alone, and under its prefix.
func (l *loader) collectKeys(importPath, name, prefix string) error {
	p, err := l.load(importPath)
	if err != nil {
		return err
	}

	st, ok := p.structs[name]
	if !ok {
		return fmt.Errorf("no struct %s in %s", name, importPath)
	}

	return l.collectStruct(st, prefix, map[*structType]bool{})
}

func (l *loader) collectStruct(st *structType, prefix string, seen map[*structType]bool) error {
	if seen[st] {
		return nil
	}
	seen[st] = true

	keys := yamlKeys(st)
	for _, f := range st.fields {
		if len(f.Names) == 0 {
			continue
		}
		key, ok := keys[f.Names[0].Name]
		if !ok {
			continue
		}

		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		l.keys[key], l.keys[full] = true, true

		_, ref, err := l.describe(st, f.Type)
		if err != nil {
			return err
		}
		if ref != nil {
			if err := l.collectStruct(ref, full, seen); err != nil {
				return err
			}
		}
	}

	return nil
}

// entry is a key of a mapping, ready to write.
type entry struct {
	key, full, typ, text string

	// mapping is the struct of a key that is a mapping, or a list of them.
	mapping *structType
}

// writeMapping writes a mapping's plain keys, then each of its mappings as a
// section of its own, so that every key is read under its mapping. A struct
// already documented is linked to rather than written again.
func (l *loader) writeMapping(out *bytes.Buffer, st *structType, prefix string, seen map[*structType]string) error {
	entries, err := l.entries(st, prefix)
	if err != nil {
		return err
	}

	seen[st] = prefix

	for _, e := range entries {
		if e.mapping == nil {
			fmt.Fprintf(out, "\n### `%s` {#%s}\n\n*%s*\n\n%s\n", e.full, anchor(e.full), e.typ, e.text)
		}
	}

	for _, e := range entries {
		if e.mapping == nil {
			continue
		}

		fmt.Fprintf(out, "\n## `%s` {#%s}\n\n*%s*\n\n%s\n", e.full, anchor(e.full), e.typ, e.text)

		if other, ok := seen[e.mapping]; ok {
			fmt.Fprintf(out, "\nIts keys are the same as [`%s`](#%s)'s.\n", other, anchor(other))
			continue
		}

		if err := l.writeMapping(out, e.mapping, e.full, seen); err != nil {
			return err
		}
	}

	return nil
}

// entries returns a mapping's keys, in declaration order.
func (l *loader) entries(st *structType, prefix string) ([]entry, error) {
	keys := yamlKeys(st)

	var (
		out      []entry
		previous string
		lastLine int
	)

	for _, f := range st.fields {
		if len(f.Names) == 0 {
			return nil, fmt.Errorf("%s embeds a field, which is not documented", st.name)
		}

		goName := f.Names[0].Name
		key, ok := keys[goName]
		if !ok {
			continue
		}

		full := key
		if prefix != "" {
			full = prefix + "." + key
		}

		typ, ref, err := l.describe(st, f.Type)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", st.name, goName, err)
		}
		if typ == "list of mappings" {
			full += "[]"
		}

		line := l.fset.Position(f.Pos()).Line

		var text string
		switch {
		case f.Doc != nil:
			text = asCode(prose(f.Doc.Text(), keys), l.keys, l.commands)
		case previous != "" && line == lastLine+1:
			// Declared on the line after a documented field, which
			// documents both.
			text = fmt.Sprintf("See `%s`, above.", previous)
		default:
			return nil, fmt.Errorf("%s.%s has no doc comment, so %s would go undocumented", st.name, goName, full)
		}

		out = append(out, entry{key: key, full: full, typ: typ, text: text, mapping: ref})
		previous, lastLine = key, l.fset.Position(f.End()).Line
	}

	return out, nil
}

// yamlKeys maps a struct's fields to their YAML keys, leaving out those not
// read from a file.
func yamlKeys(st *structType) map[string]string {
	keys := map[string]string{}

	for _, f := range st.fields {
		if f.Tag == nil || len(f.Names) == 0 || !f.Names[0].IsExported() {
			continue
		}

		tag, _ := strconv.Unquote(f.Tag.Value)
		key, _, _ := strings.Cut(reflect.StructTag(tag).Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}

		keys[f.Names[0].Name] = key
	}

	return keys
}

// describe returns a field's type as a configuration writes it, and the struct
// it is a mapping of, if any.
func (l *loader) describe(in *structType, expr ast.Expr) (string, *structType, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return l.describe(in, t.X)

	case *ast.ArrayType:
		elem, ref, err := l.describe(in, t.Elt)
		if err != nil {
			return "", nil, err
		}
		if ref != nil {
			return "list of mappings", ref, nil
		}

		return "list of " + plural(elem), nil, nil

	case *ast.MapType:
		value, _, err := l.describe(in, t.Value)
		if err != nil {
			return "", nil, err
		}

		return "mapping of " + plural(value), nil, nil

	case *ast.Ident:
		if scalar, ok := scalars[t.Name]; ok {
			return scalar, nil, nil
		}

		return l.named(in.pkg.path, t.Name)

	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			break
		}

		importPath, ok := in.imports[pkg.Name]
		if !ok {
			return "", nil, fmt.Errorf("unknown package %s", pkg.Name)
		}

		return l.named(importPath, t.Sel.Name)
	}

	return "", nil, fmt.Errorf("cannot describe a %T", expr)
}

// named describes a named type: a special one, or a struct of this module.
func (l *loader) named(importPath, name string) (string, *structType, error) {
	if special, ok := specials[importPath+"."+name]; ok {
		return special, nil, nil
	}

	if !strings.HasPrefix(importPath, module+"/") {
		return "", nil, fmt.Errorf("%s.%s is not a type a configuration can hold", importPath, name)
	}

	p, err := l.load(importPath)
	if err != nil {
		return "", nil, err
	}

	st, ok := p.structs[name]
	if !ok {
		return "", nil, fmt.Errorf("%s.%s is not a struct", importPath, name)
	}

	return "mapping", st, nil
}

// scalars maps Go's basic types to how a configuration writes them.
var scalars = map[string]string{
	"string":  "string",
	"bool":    "boolean",
	"int":     "integer",
	"int32":   "integer",
	"int64":   "integer",
	"float64": "number",
}

// specials maps named types to how a configuration writes them.
var specials = map[string]string{
	"time.Duration":                    "duration, such as 30s or 5m",
	typesPackage + ".Size":             "size, such as 512MiB or 4GiB",
	"gopkg.in/yaml.v3.Node":            "mapping, read by the provider",
	typesPackage + ".Placement":        "string: spread or pack",
	dicerPackage + ".MountType":        "string: volume, file or tmpfs",
	dockerPackage + ".MountType":       "string: volume, bind or tmpfs",
	dockerPackage + ".PullPolicy":      "string: missing or always",
	module + "/internal/provider.Node": "mapping, read by the provider",
}

// plural returns a type description in the plural.
func plural(s string) string {
	switch {
	case strings.HasSuffix(s, "s"):
		return s
	case strings.Contains(s, ","), strings.Contains(s, ":"):
		return s
	default:
		return s + "s"
	}
}

// prose turns a doc comment into the reference's text, with the struct's Go
// field names written as their keys. The comment's first word is always the
// field's name; elsewhere only compound names such as TokenPath are replaced,
// as a single word such as Token may be meant as the word.
func prose(comment string, keys map[string]string) string {
	if first, rest, ok := strings.Cut(comment, " "); ok {
		if key, ok := keys[first]; ok {
			comment = "`" + key + "` " + rest
		}
	}

	for goName, key := range keys {
		if !compound.MatchString(goName) || goName == "GitHub" {
			continue
		}
		comment = regexp.MustCompile(`\b`+regexp.QuoteMeta(goName)+`\b`).
			ReplaceAllString(comment, "`"+key+"`")
	}

	var paragraphs []string
	for para := range strings.SplitSeq(strings.TrimSpace(comment), "\n\n") {
		if isCode(para) {
			paragraphs = append(paragraphs, "```yaml\n"+dedent(para)+"\n```")
			continue
		}
		paragraphs = append(paragraphs, strings.Join(strings.Fields(para), " "))
	}

	return strings.Join(paragraphs, "\n\n")
}

// compound matches a name made of several words.
var compound = regexp.MustCompile(`^[A-Z][a-z0-9]+[A-Z]`)

// isCode reports whether a paragraph is an indented example.
func isCode(para string) bool {
	for line := range strings.SplitSeq(para, "\n") {
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "  ") {
			return false
		}
	}

	return true
}

// dedent removes an example's indentation.
func dedent(para string) string {
	lines := strings.Split(para, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\t")
	}

	return strings.Join(lines, "\n")
}

// anchor returns a key's anchor on the page.
func anchor(key string) string {
	return strings.NewReplacer(".", "-", "[]", "", "_", "-").Replace(key)
}

// codeWords matches what the reference writes as code wherever the comments
// it is generated from write it plainly: a command, a flag, an absolute path,
// an environment variable, or a configuration key of more than one word.
var codeWords = regexp.MustCompile(
	`\brungar(?: [a-z][a-z-]*){0,2}\b` +
		`|(?:^|[\s(])--[a-z][a-z-]*` +
		`|(?:^|[\s(])/(?:etc|run|var|usr|home|tmp|opt)/[\w./@:-]*[\w/]` +
		`|\$?\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\b` +
		`|\b[a-z][a-z0-9]*(?:[._][a-z0-9]+)+\b`)

// asCode writes as code the words codeWords matches in text, outside code
// already. A key is written so only if it is one of keys, and a command only
// if it is one of commands, as the patterns match ordinary words too.
func asCode(text string, keys, commands map[string]bool) string {
	parts := strings.Split(text, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = codeWords.ReplaceAllStringFunc(parts[i], func(m string) string {
			lead := ""
			if m[0] == ' ' || m[0] == '\t' || m[0] == '\n' || m[0] == '(' {
				lead, m = m[:1], m[1:]
			}

			switch {
			case strings.HasPrefix(m, "rungar"):
				// The longest command the words name.
				for w := strings.Fields(m); len(w) > 0; w = w[:len(w)-1] {
					if name := strings.Join(w, " "); commands[name] {
						return lead + "`" + name + "`" + strings.TrimPrefix(m, name)
					}
				}
				return lead + m
			case strings.HasPrefix(m, "--"), strings.HasPrefix(m, "/"):
				return lead + "`" + m + "`"
			case strings.ToUpper(m) == m:
				return lead + "`" + m + "`"
			case keys[m]:
				return lead + "`" + m + "`"
			}

			return lead + m
		})
	}

	return strings.Join(parts, "`")
}
