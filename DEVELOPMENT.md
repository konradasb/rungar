# Development

## Getting the source

```console
$ git clone https://github.com/konradasb/rungar.git
$ cd rungar
```

Each provider's client library is an ordinary dependency in
[go.mod](go.mod). To work on one alongside Rungar, point Rungar at its
checkout with a workspace, which `.gitignore` keeps out of commits:

```console
$ go work init . ../the-library
```

## Building

```console
$ make build          # bin/rungar, for this machine
$ make build-all      # one binary per released platform
$ make docker         # the container image
$ make test           # unit tests
$ make cover          # unit tests, with a coverage summary
$ make fuzz           # fuzz tests, a minute each
$ make lint           # golangci-lint
$ make lint-workflows # actionlint and zizmor, over .github/workflows
$ make vuln           # govulncheck
$ make fmt            # format the Go code and the protobuf files
$ make docs-serve     # the documentation site, at http://localhost:1313
$ make docs-gen       # regenerate the reference pages from the code
$ make generate       # regenerate the gRPC code from proto/
$ make help           # every target
```

Rungar is a network client: it talks to its providers' APIs and to GitHub
over the network, and needs nothing of the machine it runs on. It builds for
Linux, macOS and FreeBSD on amd64 and arm64; releases carry Linux and macOS,
and `make build` gives you whatever you are sitting at. A laptop is a
perfectly good place to run a development daemon against a remote fleet.

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
cmd/rungar/              The binary: main, and the embedded time zone
                         database. The work is in internal/cli.

internal/types/          The data every package shares: scale sets, runners,
                         machines and providers. A struct that crosses a
                         package boundary goes here, unless one package
                         owns it, as events owns events.
                         Imported by nearly every package, imports nothing
                         of ours but errdefs.
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
                         GitHub, the scaling decisions, its runners -- created,
                         removed, adopted and reconciled against the fleet --
                         and what the command line asks of them. One file
                         per verb.
internal/grpcapi/        The daemon's side of the socket: one handler per
                         resource, turning what the scale sets answer, and
                         what the daemon says of itself, into the messages
                         proto/rungar/v1 defines.
internal/github/         A small client of GitHub's REST API, for what the
                         scale set API does not say: whether each runner is
                         online and busy. Authenticates through its HTTP
                         client's transport: a token, or a GitHub App.
internal/provider/       The boundary with what runners run on: the Provider
                         interface, and what the provider types share.
                         What a machine is lives in types.
internal/provider/*/     One package per provider type, named for it. The
                         only package that speaks its backend's API.
internal/events/         The events, and their log: what the daemon did to
                         runners, providers and scale sets, kept in a file
                         and followed by rungar events.
internal/fleet/          Every provider: the order a scale set's runner is
                         tried on them, and what their refusals taught --
                         failing, full for a scale set, unable to create its
                         runner -- and their limits.
internal/metrics/        Prometheus.

proto/rungar/v1/         The socket's API, and the code generated from it by
                         'make generate'. Internal: the command line is its
                         one client, and ships with the daemon.

test/e2e/                rungar itself, deployed to a real host and driven
                         through its command line: real providers and a
                         real GitHub. Behind the 'e2e' build tag.

scripts/                 The install script, and the service it installs.
build/                   The Linux packages, and their apt and dnf
                         repository; see build/README.md.
build/runner-image/      Packer templates for the images the gcp and aws
                         providers' runners boot, sharing one install
                         script.
charts/                  The Helm chart, which runs the container image,
                         and its OCI repository; see charts/README.md.
```

The daemon has one fleet of every provider, and each scale set a
`fleet.ScaleSetProviders` of its own providers, in its order. A daemon serving
three sizes runs three scale sets, each with its own session and listener, over
one `fleet.Manager`. Placement is the fleet's alone:
`ScaleSetProviders.Place` puts the scale set's providers in order and asks each
to create the runner's machine until one does, recording what each refusal
says. It never asks a provider what it has spare, and holds no lock across a
call to one: a provider's `max_runners` is the only thing counted, under that
provider's own mutex, with the runners being created there.

The dependencies run one way: `cli` → `daemon` → `grpcapi` → `scaleset` →
`fleet` → `provider` → `types`. On the daemon's side, `grpcapi` alone speaks
proto; the command line is its client. GitHub's scale
set API is `actions/scaleset`'s client, imported as `ghscaleset` and used
directly through the `scaleset.API` interface; its REST API is
`internal/github`. The command line reaches the daemon only through the
socket: it imports `daemon` to run `rungar serve`, and nothing else of it.
Nothing above `provider` knows which backend is in use, and each backend's
API appears only in its provider's package.

Each package declares an interface for what it needs of another --
`provider.Provider`; in `scaleset` `Fleet`, `scaleSetProviders`, `API`,
`GitHubRunnerLister`, `EventRecorder` and `MetricsRecorder`; in `fleet`
`EventRecorder` and `ProviderCallRecorder`; and in `grpcapi`
`ScaleSetManager` and `EventLog` -- rather than
taking the concrete type, where that lets its decisions be tested without the
one below: placement without a machine, scaling without GitHub. Each is
checked against its implementation with a `var _ Interface = (*Impl)(nil)`
line in the package that declares it, beside the declaration or in the
package's block of such lines; an interface a provider implements, such as
`provider.Provider`, in that provider's package; and one whose implementation
its package does not import -- `scaleset.MetricsRecorder` and
`fleet.ProviderCallRecorder`, both `metrics.Metrics` -- in `daemon`, which
wires them in.

### Adding a provider

A new backend is a package under `internal/provider`, and one line in
`internal/config/provider.go`, which registers its `provider.Type` by the
name a configuration's `type:` gives. Nothing else changes: the fleet, the
scale sets and the daemon work with any provider.

A provider is one backend that creates machines and decides for itself where
on the backend they go: a cluster, a cloud account, a single machine.
Rungar chooses only between providers, so a provider has no notion of hosts
to expose, and a backend with no scheduler of its own is a provider per
machine.

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
  scale set skips this one for a while; the provider may make the error an
  `errdefs.ErrNoCapacity` error when the backend is full now, which lets a
  scale set of higher priority hold lower ones off it. It never says what it
  has spare: placement tries it;
- keeps Rungar's labels on its machines, in whatever form its backend has, and
  finds machines by them: they are the only record Rungar keeps;
- delivers each runner's just-in-time configuration to its machine,
  however its backend allows;
- creates a machine that boots once and is never restarted. A backend that
  cannot delete a machine when it stops need not: Rungar deletes stopped
  machines itself.

Its `provider.Config` may also have methods that `internal/config` looks for,
each through an interface of its own: `CheckFiles` reads the files it names,
for `rungar validate`; `SecretFiles` names those holding a secret, which the
daemon warns of if anyone can read them; `SecretKeys` names the keys that can
hold a secret inline, which `rungar config` prints redacted; and
`CheckRunnerName` refuses a scale set whose runners' names the backend cannot
take. Which type implements which is asserted in
`internal/config/provider_test.go`.

### Changing the configuration or the command line

[RELEASES.md](RELEASES.md#deprecation) promises that nothing is taken away
without a warning first.

- **A key added** needs nothing more; the configuration's version stays.
- **A key renamed or removed, or a value whose meaning changes,** is a
  `migration` in `internal/config/migrate.go`: it rewrites the old form in
  the file as parsed, and returns a `Deprecation` for each change, on the
  line the file has it. Loading applies it, so the old form keeps working
  and is warned of, and `rungar config migrate` writes its result. A change
  in meaning also raises `config.Version`, and the migration's `until` is
  the last version the old form is read in. A migration is deleted, and
  `oldestVersion` raised, in the major release that stops reading the old
  form.
- **A command or a flag** is deprecated with cobra's own: `Deprecated` on
  the command, `MarkDeprecated` on the flag, saying what replaces it. Both
  keep working, warn on stderr, and drop out of `--help` and the reference.

## Testing

### Unit tests

```console
$ make test     # -race, no network
$ make cover    # the same, with a coverage summary
$ make cover-html
```

They cover the decisions: which provider a runner should go on, what makes a
machine a runner, when a runner is written off, what a configuration means
and which ones are refused.

Each package fakes the interface it consumes in its own `_test.go` files:
`scaleset` a fleet and GitHub, `fleet` a provider, `cli` the daemon. There is
no shared test package. The provider packages fake their backend instead,
most of them with an in-memory server that a real client is pointed at, so
the calls, their timeouts and the translation between runners and the
backend's messages are all exercised.

Coverage sits at about 85% of statements. What is left is mostly `daemon.Serve`,
the top-level wiring, which the end-to-end tests exercise instead. A change
that drops coverage noticeably is worth a second look -- the CI summary on
each pull request reports it.

### Fuzz tests

```console
$ make fuzz                # each for a minute
$ make fuzz FUZZTIME=10m
```

They feed arbitrary files to what reads the configuration: loading it, which
must refuse what it cannot read rather than panic, and print what it loads as
YAML that loads again the same; the text edit `rungar config migrate` makes to
set the version, which must change nothing else; and `provider.Resolve` and
`provider.Merge`, which resolve anchors and merge runner blocks. `make test`
runs only their seeds and the inputs under `testdata/fuzz`. An input a fuzz
test fails on is written there: fix what it found, and commit the input, so
that it stays a test.

### End-to-end tests

These deploy `rungar` to a real host, over SSH, and drive it through its
command line against real providers and a real GitHub: a runner is placed,
registered, booted and connects; a restarted daemon adopts it; `runners rm`,
`reconcile`, `status`, the providers and the metrics endpoint are exercised.

The host needs a rungar configuration naming a GitHub and its providers.
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
| `RUNGAR_E2E_HOST` | The host to run `rungar` on, reached over SSH, or `local` for this machine, through `sudo`, as CI does. Required. |
| `RUNGAR_E2E_USER` | The SSH user; `root` by default. |
| `RUNGAR_E2E_KEY` | An SSH key, where the SSH configuration does not name one. |
| `RUNGAR_E2E_CONFIG` | The host's rungar configuration; `/etc/rungar/config.yaml` by default. |
| `RUNGAR_E2E_PROVIDERS` | The providers the tests' scale set uses, by name, such as `compute1,compute2`; all of them by default. |
| `RUNGAR_E2E_KEEP` | Leaves the daemon and its runners in place, to look at what failed. |

#### A configuration of their own

The tests can run against a host's real configuration, but a configuration
of their own, with a test GitHub repository and providers that can spare a
few runners, keeps them away from real jobs:

1. Prepare each provider so that it can create a runner, as its
   [reference page](docs/content/docs/providers) says.
2. Give the host a GitHub token that can manage the test repository's
   self-hosted runners, in a file only root can read.
3. Write the configuration, `/etc/rungar/e2e.yaml` say, with `github` and
   `providers` and nothing else; the tests bring the rest. Each provider's
   `runner` block must be a whole runner, unless the tests size that type's
   runners themselves (`runnerBlocks` in
   [test/e2e/environment_test.go](test/e2e/environment_test.go)). Keep `max_runners` low, as the tests need two at most.

```console
$ make test-e2e RUNGAR_E2E_HOST=10.10.0.101 RUNGAR_E2E_CONFIG=/etc/rungar/e2e.yaml
```

The teardown removes the scale set, and its runners' machines are deleted
with it. A run killed before it leaves them behind, carrying the
[label](docs/content/docs/reference/labels.md) `rungar.sh/installation` with
the value `rungar-e2e`, in whatever form the provider keeps labels in.

#### In CI

Each provider the tests run against in CI has a workflow of its own,
`e2e-<provider>.yaml`, run every night and on demand, whose header says what
it needs set up. It runs the tests on
its own runner with `RUNGAR_E2E_HOST=local`, so the daemon runs as root on
the workflow's machine:

1. it gets a credential for the provider, with no long-lived key where the
   provider allows it;
2. it writes a configuration of the tests' own, as above, under
   `/etc/rungar-e2e`, readable only by root;
3. it runs `make test-e2e`;
4. it deletes, whatever the outcome, the machines a killed run left,
   found by their installation label.

Runs of one workflow share its scale set and installation, so they run one at
a time. A workflow does nothing unless its provider's variables are set, as
in a fork. Every workflow shares, as the repository's variable and secret:

| | |
|---|---|
| `RUNGAR_E2E_GITHUB_URL` | Variable: the repository or organization the runners register with. |
| `RUNGAR_E2E_GITHUB_TOKEN` | Secret: a token that can manage that repository's or organization's self-hosted runners. |

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

- Comments say why, not what. A comment restating the code is noise; one
  explaining a decision that is not obvious from the code is the point.
- Errors are put in classes (see
  [internal/errdefs](internal/errdefs/errdefs.go)) and matched with
  `errors.Is`, never by reading a message. An error from a backend's API is
  matched by its code or type, which the provider puts in a class.
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
