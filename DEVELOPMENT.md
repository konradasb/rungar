# Development

## Getting the source

```console
$ git clone https://github.com/konradasb/rungar.git
$ cd rungar
```

Dicer is an ordinary dependency, `github.com/konradasb/dicer` in
[go.mod](go.mod). Rungar uses two things of it: `dicer.NewClient`, which makes
the connection, and the API's generated messages in `proto/dicerd/v1`, which
are what every call takes and returns. Everything else in Dicer is internal to
it, and Rungar does not reach for it.

To work on both at once, point Rungar at a Dicer checkout with a workspace,
which `.gitignore` keeps out of commits:

```console
$ go work init . ../dicer
```

## Building

```console
$ make build          # bin/rungar, for this machine
$ make build-all      # one binary per released platform
$ make docker         # the container image
$ make test           # unit tests
$ make cover          # unit tests, with a coverage summary
$ make lint           # golangci-lint
$ make lint-workflows # actionlint and zizmor, over .github/workflows
$ make vuln           # govulncheck
$ make fmt            # format, and fix what can be fixed
$ make docs-serve     # the documentation site, at http://localhost:1313
$ make docs-gen       # regenerate the reference pages from the code
$ make generate       # regenerate the gRPC code from proto/
$ make help           # every target
```

Rungar is a network client: it talks to Dicer daemons over gRPC and to GitHub
over HTTPS, and needs nothing of the machine it runs on. It builds for Linux,
macOS, Windows and FreeBSD on amd64 and arm64; releases carry Linux and macOS,
and `make build` gives you whatever you are sitting at.

Dicer is the Linux part. Rungar only ever talks to it over the network, so a
laptop is a perfectly good place to run a development daemon against a remote
fleet.

### On a real host

`make deploy` builds `rungar` for Linux, installs it and its systemd unit on a
host that already runs the service (see [scripts/install.sh](scripts/install.sh)),
and restarts it. Runners keep running through the restart, and the new daemon adopts them:

```console
$ make deploy DEPLOY_HOST=root@my-host
```

`DEPLOY_GOARCH` builds for an arm64 host instead; it is amd64 by default.

## The layout

```
cmd/rungar/              The binary. Four lines; the work is in internal/cli.

internal/types/          The data every package shares: scale sets, runners,
                         machines, providers, and the daemon's status. A
                         struct that crosses a package boundary goes here.
                         Imported by everything, imports nothing of ours but
                         errdefs.
internal/errdefs/        The classes errors are put in, matched with errors.Is.
internal/version/        The build's identity, stamped in at link time.

internal/cli/            The command line: rungar serve and validate, and
                         every other command, which asks the running daemon
                         over its socket and writes what it answers.
internal/config/         The configuration file: its keys, loading and
                         checking it, and each provider type's part of it.
internal/daemon/         Wiring. Loads the configuration, connects to the
                         providers and GitHub, runs the scale sets, and
                         serves their API on the socket and their metrics
                         over HTTP.
internal/scaleset/       Every scale set, as it runs: its session with
                         GitHub, the scaling decisions, its runners -- made,
                         removed, adopted and reconciled against the fleet --
                         and what the command line asks of them. One file
                         per verb.
internal/grpcapi/        The daemon's side of the socket: one handler per
                         resource, turning what the scale sets answer into
                         the messages proto/rungar/v1 defines.
internal/github/         A small client of GitHub's REST API, for what the
                         scale set API does not say: whether each runner is
                         online and busy. Authenticates through its HTTP
                         client's transport: a token, or a GitHub App.
internal/provider/       The boundary with what runners run on: the Provider
                         interface. What a machine is lives in types.
internal/provider/dicer/ The dicer provider: one Dicer host. The only
                         package that speaks Dicer's API.
internal/provider/docker/
                         The docker provider: one Docker host, over its
                         Engine API.
internal/provider/proxmox/
                         The proxmox provider: one Proxmox VE cluster, VMs
                         cloned from a template.
internal/provider/gcp/   The gcp provider: Compute Engine, one project and
                         region.
internal/provider/aws/   The aws provider: EC2, one account and region.
internal/events/         The event log: what the daemon did to runners and
                         providers, kept in a file and followed by
                         rungar events. It is Dicer's.
internal/fleet/          Every provider: the order a scale set's runner is
                         tried on them, and what their refusals taught --
                         failing, full for a scale set, unable to make its
                         runner -- and their limits.
internal/metrics/        Prometheus.

proto/rungar/v1/         The socket's API, and the code generated from it by
                         'make generate'. Internal: the command line is its
                         one client, and ships with the daemon.

test/e2e/                rungar itself, deployed to a real host and driven
                         through its command line: real Dicer hosts and a
                         real GitHub. Behind the 'e2e' build tag.

scripts/                 The install script, and the service it installs.
build/                   The Linux packages, and their apt and dnf
                         repository; see build/README.md.
charts/                  The Helm chart, which runs the container image,
                         and its OCI repository; see charts/README.md.
```

The daemon has one fleet of every provider, and each scale set a `fleet.Group`
of its own providers, in its order. A daemon serving three sizes runs three
scale sets, each with its own session and listener, over one `fleet.Manager`.
Placement is the fleet's alone: `Group.Place` puts the scale set's providers
in order and asks each to make the runner's machine until one does, recording
what each refusal says. It never asks a provider what it has spare, and holds
no lock across a call to one: a provider's `max_runners` is the only thing
counted, under that provider's own mutex, with the runners being made there.

The dependencies run one way: `cli` → `daemon` → `grpcapi` → `scaleset` →
`fleet` → `provider` → `types`. `grpcapi` alone speaks proto. GitHub's scale
set API is `actions/scaleset`'s client, imported as `ghscaleset` and used
directly through the `scaleset.API` interface; its REST API is
`internal/github`. The command line reaches the daemon only through the
socket: it imports `daemon` to run `rungar serve`, and nothing else of it.
Nothing above `provider` knows which backend is in use. Dicer's API appears
only in `internal/provider/dicer`.

Each package declares an interface for what it needs of another --
`provider.Provider`, `scaleset.Fleet`, `scaleset.API` and
`scaleset.RunnerLister` -- rather than taking the concrete type, where that
lets its decisions be tested without the one below: placement without a VM,
scaling without GitHub. Each is checked against its implementation with a
`var _ Interface = (*Impl)(nil)` block where it is declared.

### Adding a provider

A new backend is a package under `internal/provider`, and one line in
`internal/config/provider.go`, which registers its `provider.Type` by the
name a configuration's `type:` gives. Nothing else changes: the fleet, the
scale sets and the daemon work with any provider.

A provider is one backend that makes machines and decides for itself where
on the backend they go: an OpenStack project, a cloud account, a Dicer host.
Rungar chooses only between providers, so a provider has no notion of hosts
to expose, and a backend with no scheduler of its own -- Dicer -- is a
provider per machine.

The type is handed its entry in `providers:` less the keys that are Rungar's
-- `name`, `type`, `weight`, `max_runners`, `disabled` and `runner` -- with anchors and merge
keys resolved (`provider.Resolve`), and, for each scale set on it, the runner
block already merged: the provider's, the scale set's, and the scale set's
for that provider (`provider.Merge`). It reads them with `provider.Decode`,
so that its settings are as strictly checked as Rungar's own. Inheritance is
not the type's to implement: it validates a runner block as complete. A
provider:

- lists, creates and deletes machines, bounding every call by a timeout of
  its own. A failed create sends the runner to the next provider, and the
  scale set skips this one for a while; the provider may mark the error
  `errdefs.NoCapacity` when the backend is full now, which lets a scale set
  of higher priority hold lower ones off it. It never says what it has
  spare: placement tries it;
- keeps Rungar's labels on its machines, in whatever form its backend has, and
  finds machines by them: they are the only record Rungar keeps;
- delivers each runner's just-in-time configuration to its machine,
  however its backend allows;
- creates a machine that boots once and is never restarted. A backend that
  cannot remove a machine when it stops need not: Rungar removes stopped
  machines itself.

## Testing

### Unit tests

```console
$ make test     # -race, no network
$ make cover    # the same, with a coverage summary
$ make cover-html
```

They cover the decisions: which host a runner should go on, what makes a VM a
runner, when a runner is written off, what a configuration means and which
ones are refused.

Each package fakes the interface it consumes in its own `_test.go` files:
`scaleset` a fleet, `fleet` and `daemon` a provider. There is no shared test
package. The exception that is not really a unit test, and is the more
valuable for it, is `internal/provider/dicer`, which runs real gRPC servers
in memory and points a real Dicer client at them, so the dialling, the
per-host timeout and the translation between runners and Dicer's messages are
all exercised.

Coverage sits at about 85% of statements. What is left is mostly `Daemon.Run`,
the top-level wiring, which the end-to-end tests exercise instead. A change
that drops coverage noticeably is worth a second look -- the CI summary on
each pull request reports it.

### End-to-end tests

These deploy `rungar` to a real host, over SSH, and drive it through its
command line against real Dicer hosts and a real GitHub: a runner is placed,
registered, booted and connects; a restarted daemon adopts it; `runners rm`,
`reconcile`, `status`, the providers and the metrics endpoint are exercised.

The host needs a rungar configuration naming a GitHub and its Dicer providers.
The tests use its credential and providers as they are and nothing else: they
run their own daemon, under `/opt/rungar-e2e` and as the systemd unit
`rungar-e2e`, with their own installation and scale set, both `rungar-e2e`, so a
real Rungar on the same host and fleet is left alone. At the end they remove
their runners and their scale set from GitHub with `rungar scale-sets rm`.

```console
$ make test-e2e RUNGAR_E2E_HOST=10.10.0.101
```

| Variable | |
|---|---|
| `RUNGAR_E2E_HOST` | The host to run `rungar` on, reached over SSH. Required. |
| `RUNGAR_E2E_USER` | The SSH user; `root` by default. |
| `RUNGAR_E2E_KEY` | An SSH key, where the SSH configuration does not name one. |
| `RUNGAR_E2E_CONFIG` | The host's rungar configuration; `/etc/rungar/config.yaml` by default. |
| `RUNGAR_E2E_KEEP` | Leaves the daemon and its runners in place, to look at what failed. |

### What a host needs

A Dicer host needs a few things before it can run VMs at all, and the error
for each arrives only when the first VM is started. Worth checking on a fresh
host:

```console
$ mkfs.erofs --version        # erofs-utils: images are converted to EROFS
$ iptables --version          # the bridge's NAT rules
$ sysctl net.ipv4.ip_forward  # must be 1
$ dicer kernel list           # at least one kernel
$ dicer network list          # at least one network
```

A runner block that names a kernel or a network, given to several providers,
names it on each of their hosts. A fleet whose hosts each have exactly one of
each needs neither, and takes each host's default.

## The container image

`make docker` builds it for this machine. The [Dockerfile](Dockerfile) is a
multi-stage build that cross-compiles on the builder's own platform, so
building for linux/amd64 and linux/arm64 together needs no emulation, which is
how the [image workflow](.github/workflows/image.yaml) publishes a release's.
Its base images are pinned by digest, and Dependabot moves them.

It is `distroless/static` and about 6 MB: the binary, a certificate store and
a passwd file. There is no shell, which is worth remembering when something
goes wrong inside it -- `rungar status` is the way to ask it what it sees.

Rungar keeps no state of its own, so a restarted container picks up where the
last one left off by reading the fleet. Its one volume, `/var/log/rungar`,
holds the event log, which is history: losing it loses what `rungar events`
shows, and nothing else.

## Conventions

Same as Dicer's, since it is the same codebase to read:

- Comments say why, not what. A comment restating the code is noise; one
  explaining a decision that is not obvious from the code is the point.
- Errors are put in classes (see
  [internal/errdefs](internal/errdefs/errdefs.go)) and matched with
  `errors.Is`, never by reading a message. An error from a Dicer host is a
  gRPC status, and is matched by its code.
- Exported identifiers are documented, and the doc comment says what the thing
  is for rather than repeating its name.
- A configuration field's doc comment is its entry in the configuration
  reference, which `make docs-gen` writes -- and refuses to write if a field
  has none. Write it for the person configuring Rungar: values as they are
  written in the file ("Unset is 10s"), not the Go names behind them.
- Every file carries the licence header. `make license` adds missing ones.

Before opening a pull request:

```console
$ make fmt
$ make lint
$ make cover
```

## Releasing

See [RELEASES.md](RELEASES.md): what a version promises, how a release is
made, and how to verify one.
