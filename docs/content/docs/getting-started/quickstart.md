---
title: Quickstart
weight: 3
description: "From one provider to a workflow job running on a machine of its own."
icon: lightning-bolt
related_title: Next steps
related:
  - /docs/concepts/how-rungar-works
  - /docs/guides/designing-runner-sizes
  - /docs/guides/github-credentials
  - /docs/guides/running-the-daemon
---

From one provider -- a [Dicer](https://github.com/konradasb/dicer) host, a
Proxmox VE cluster, a Google Cloud project or an AWS account -- to a workflow
job running on a machine of its own.

You need:

- a GitHub repository you administer. The runners serve only it: an
  organisation takes a different permission -- **Self-hosted runners** in
  place of the repository's **Administration** -- as
  [GitHub credentials](../../guides/github-credentials) sets out;
- a Linux machine for Rungar, with systemd: for Dicer, the Dicer host itself;
- a provider ready for runners, as its tab in
  [Connect a provider](#connect-a-provider) sets out.

{{% steps %}}

### Create a GitHub App

Under your account or organisation, **Settings → Developer settings → GitHub
Apps → New GitHub App**:

- a name, and a homepage URL -- the repository's will do;
- **Webhook → Active** unticked: Rungar asks GitHub for what it needs;
- **Repository permissions**: **Administration** read and write. GitHub
  adds **Metadata** read by itself.

Create it, and note its **Client ID**. Under **Private keys**, generate one:
GitHub hands over a `.pem` file, once. Then **Install App**, on the
repository alone, and note the **installation ID**, the number that ends the
installation's URL: `.../settings/installations/12345678`.

### Install Rungar

On Rungar's machine:

```console
$ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash
```

This installs `rungar` and its service, stopped, and the `rungar` user. See
[Installation](../installation) for the packages, and what else it does.

### Connect a provider

Let Rungar reach the provider, and write down the provider's entry in the
configuration, named `main`, for the next step. Each runner gets 2 vCPUs and
4 GiB.

{{< tabs >}}
  {{< tab name="Dicer" >}}
  **Before you start:** Dicer
  [installed](https://dicer.sh/docs/getting-started/installation/) on the
  host, with a network and a kernel, as Dicer's
  [Quickstart](https://dicer.sh/docs/getting-started/quickstart/) sets up.
  Rungar reaches Dicer through its socket, so there is no network between
  the two to secure; the [Dicer provider](../../providers/dicer) says how to
  reach hosts elsewhere.

  Dicer's socket is usable by the `dicer` group; the service runs as
  `rungar`. Give it the group with a drop-in, which an upgrade leaves alone:

  ```console
  $ sudo systemctl edit rungar
  ```

  and add, between the comments:

  ```ini
  [Service]
  SupplementaryGroups=dicer
  ```

  {{< callout type="warning" >}}
    The `dicer` group is as much as root on the host, and so is `rungar` with
    it. Keep its members, and the configuration's credentials, to whom you
    would give root.
  {{< /callout >}}

  The provider:

  ```yaml
  providers:
    - name: main
      type: dicer
      address: unix:///run/dicer/dicer.sock
      runner:
        image: ghcr.io/actions/actions-runner:latest
        vcpus: 2
        memory: 4GiB
  ```

  `dicer ps` shows its runners.
  {{< /tab >}}
  {{< tab name="Proxmox VE" >}}
  **Before you start:** a Proxmox VE 8 or 9 cluster Rungar's machine can
  reach on port 8006, and a template VM with the Actions runner and the QEMU
  guest agent; see
  [Preparing a template](../../providers/proxmox/preparing-a-template). Its
  VMID here is 9000.

  Give Rungar a user and an API token of its own. On any node, as root:

  ```console
  # pveum user add rungar@pve
  # pveum role add Rungar --privs "VM.Allocate VM.Clone VM.Audit VM.PowerMgmt
      VM.Config.CDROM VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk
      VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options
      VM.GuestAgent.Audit VM.GuestAgent.FileRead VM.GuestAgent.FileWrite
      VM.GuestAgent.FileSystemMgmt VM.GuestAgent.Unrestricted
      Datastore.AllocateSpace Sys.Audit"
  # pveum acl modify / --users rungar@pve --roles Rungar
  # pveum user token add rungar@pve rungar --privsep 0
  ```

  On Proxmox VE 8, put `VM.Monitor` in place of the `VM.GuestAgent`
  privileges. The role here is granted on the whole cluster; the
  [Proxmox VE provider](../../providers/proxmox) says where it is needed, to
  grant it more narrowly.

  The last command prints the token's secret, once. On Rungar's machine, put
  it where the service, and only the service, can read it, with the
  cluster's CA from `/etc/pve/pve-root-ca.pem` on any node beside it:

  ```console
  $ sudo install -o root -g rungar -m 0640 /dev/null /etc/rungar/pve-token
  $ sudo nano /etc/rungar/pve-token
  $ sudo install -o root -g rungar -m 0640 pve-root-ca.pem /etc/rungar/pve-root-ca.pem
  ```

  The provider, the cluster by any of its nodes:

  ```yaml
  providers:
    - name: main
      type: proxmox
      url: https://pve.example.com:8006
      token_id: rungar@pve!rungar
      token_secret_path: /etc/rungar/pve-token
      tls:
        ca_file: /etc/rungar/pve-root-ca.pem
      runner:
        template: 9000
        cores: 2
        memory: 4GiB
  ```

  The web interface shows its runners, VMs named for them, with `rungar-`
  tags.
  {{< /tab >}}
  {{< tab name="GCP" >}}
  **Before you start:** a Google Cloud project with the Compute Engine API
  enabled, `gcloud` signed in to it, and an image with the Actions runner in
  the `actions-runner` family; see
  [Building an image](../../providers/gcp/building-an-image). The project
  here is `my-ci-123456`.

  Give Rungar a service account of its own, which may manage the project's
  instances:

  ```console
  $ gcloud iam service-accounts create rungar --project my-ci-123456
  $ gcloud projects add-iam-policy-binding my-ci-123456 \
      --member serviceAccount:rungar@my-ci-123456.iam.gserviceaccount.com \
      --role roles/compute.instanceAdmin.v1
  ```

  If Rungar's machine is a Compute Engine instance, run it as that account,
  with the `cloud-platform` scope, and leave `credentials_file` out below.
  Otherwise, make a key, and put it where the service, and only the service,
  can read it:

  ```console
  $ gcloud iam service-accounts keys create rungar-key.json \
      --iam-account rungar@my-ci-123456.iam.gserviceaccount.com
  $ sudo install -o root -g rungar -m 0640 rungar-key.json /etc/rungar/gcp-key.json
  $ rm rungar-key.json
  ```

  A key is a secret that does not expire. Workload identity federation, or
  impersonation, does without one: see
  [`credentials_file`](../../providers/gcp/configuration#credentials-file).

  The provider:

  ```yaml
  providers:
    - name: main
      type: gcp
      project: my-ci-123456
      zones: [europe-west1-b, europe-west1-c]
      credentials_file: /etc/rungar/gcp-key.json
      runner:
        image: projects/my-ci-123456/global/images/family/actions-runner
        machine_type: e2-medium
  ```

  `gcloud compute instances list` shows its runners.
  {{< /tab >}}
  {{< tab name="AWS" >}}
  **Before you start:** an AWS account, the `aws` CLI signed in to it, a
  subnet with a route to the internet, and an AMI with the Actions runner;
  see [Building an image](../../providers/aws/building-an-image). The region
  here is `eu-west-1`.

  Give Rungar a policy that lets it run and terminate instances,
  `rungar-policy.json`:

  ```json
  {
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": [
          "ec2:RunInstances",
          "ec2:CreateTags",
          "ec2:DescribeInstances",
          "ec2:DescribeImages",
          "ec2:TerminateInstances"
        ],
        "Resource": "*"
      }
    ]
  }
  ```

  If Rungar's machine is an EC2 instance, attach the policy to the role of
  its instance profile, and leave `credentials_file` out below. Otherwise,
  give it to a user of Rungar's own, with an access key:

  ```console
  $ aws iam create-user --user-name rungar
  $ aws iam put-user-policy --user-name rungar --policy-name rungar \
      --policy-document file://rungar-policy.json
  $ aws iam create-access-key --user-name rungar
  ```

  and put the key where the service, and only the service, can read it,
  `/etc/rungar/aws-credentials`:

  ```console
  $ sudo install -o root -g rungar -m 0640 /dev/null /etc/rungar/aws-credentials
  $ sudo nano /etc/rungar/aws-credentials
  ```

  ```ini
  [default]
  aws_access_key_id = AKIAIOSFODNN7EXAMPLE
  aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
  ```

  The provider:

  ```yaml
  providers:
    - name: main
      type: aws
      region: eu-west-1
      subnets: [subnet-0a1b2c3d4e5f60718]
      public_ip: true
      credentials_file: /etc/rungar/aws-credentials
      runner:
        image: ami-0a1b2c3d4e5f60718
        instance_type: t3.medium
  ```

  The EC2 console shows its runners, instances named for them.
  {{< /tab >}}
{{< /tabs >}}

### Configure it

Put the App's key where the service, and only the service, can read it:

```console
$ sudo install -o root -g rungar -m 0640 my-app.private-key.pem /etc/rungar/app.pem
```

Then write `/etc/rungar/config.yaml`, with the repository's URL, the App's
client and installation IDs, and the provider from the step above:

```yaml
version: 1

github:
  url: https://github.com/my-org/my-repo
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

# The providers block from Connect a provider goes here.

# One scale set: the label workflows target, and at most two runners on the
# provider.
scale_sets:
  - name: rungar-c2-m4
    max_runners: 2
    providers: [main]
```

```console
$ sudo chown root:rungar /etc/rungar/config.yaml
$ sudo chmod 0640 /etc/rungar/config.yaml
```

### Start it

Check the configuration, start the daemon, and see what it is doing:

```console
$ sudo -u rungar rungar validate
/etc/rungar/config.yaml is valid: 1 provider, 1 scale set
$ sudo systemctl enable --now rungar
$ sudo -u rungar rungar status
```

`rungar status` should show the credentials working, `rungar-c2-m4`
`LISTENING`, and `main` answering. On GitHub, the repository's **Settings →
Actions → Runners** now lists the scale set. It has no runners yet: Rungar
creates one when a job asks for it. If something is not right, the daemon's
log says why:

```console
$ journalctl -u rungar -n 50
```

### Run a job

Add a workflow to the repository, `.github/workflows/rungar.yaml`:

```yaml
name: rungar
on: workflow_dispatch

jobs:
  hello:
    runs-on: rungar-c2-m4
    steps:
      - run: |
          uname -a
          nproc
          free -h
```

and run it from the repository's **Actions** tab. In the meantime, watch
Rungar answer:

```console
$ sudo -u rungar rungar events -f
2026-09-28 10:15:02   Runner   rungar-c2-m4-3f9a01c2   Created       Runner created on main: 2 vCPU, 4 GiB
2026-09-28 10:15:29   Runner   rungar-c2-m4-3f9a01c2   Connected     Runner connected to GitHub, 27s after its machine was created
2026-09-28 10:15:30   Runner   rungar-c2-m4-3f9a01c2   Job started   Job started: hello of my-org/my-repo, after waiting 31s, 30s of it for a runner
2026-09-28 10:15:41   Runner   rungar-c2-m4-3f9a01c2   Removed       Runner removed from main: its job completed
```

The job was queued; GitHub told Rungar; Rungar created a machine on the
provider, registered only for that job; the job ran on it, printing two
CPUs; and the machine was deleted. A second run gets a machine of its own.
The provider shows it while it is there, as its tab says.

{{% /steps %}}

## Where next

- Keep a runner warm, so a job starts without waiting for a boot:
  `min_runners` on the scale set. See [Scale sets](../../concepts/scale-sets).
- More sizes, and more providers: see
  [Designing runner sizes](../../guides/designing-runner-sizes) and
  [Placement](../../concepts/placement).
- What to tighten before relying on runners -- the image, the network, the
  identity jobs run as: see each provider's notes, for
  [Dicer](../../providers/dicer#notes),
  [Proxmox VE](../../providers/proxmox#notes),
  [GCP](../../providers/gcp#notes) and
  [AWS](../../providers/aws#notes).
