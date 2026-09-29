# TODO

## Bugs found on compute1 and compute2 (2026-09-28)

Found by the e2e suite, konradasb/tests' acceptance workflow, and fault
injection on both hosts. Fixed since: scale set names equal but for case,
names no runner can be named after, memory below 128MiB or not in whole MiB,
a NaN or infinite weight, too short a `reconcile_interval` or
`start_timeout`, the machine a failed create leaves, `rungar status` calling
the fleet full while a smaller runner fits, providers going away and coming
back unlogged, the gRPC error in the STATUS column, `scale-sets rm` of
nothing succeeding, and the listener's INFO lines. What is left:

- **A blackholed provider delays the runners tried on it.** Placement no
  longer asks providers anything first, so one not answering stalls no other
  scale set; but until the next listing finds it not answering, each runner
  tried on it waits for the call to fail -- gRPC's connect timeout, or the
  runner's start_timeout at worst -- before the next provider is tried.
  Measure it on compute2, and give Create a timeout of its own for the call
  to be accepted, apart from the time a cold image pull takes.
- **`rungar_github_requests_total` does not count the message sessions'
  polls.** Over ten long polls it counted no `202`, only the REST listings
  and session creation, though the reference says a working daemon always
  has a 2xx rate from them. The polls go through the same retrying client,
  so the cause is still to be found.
- **Priority refills an idle reserve before a lower priority's queued jobs.**
  With compute2 down, rungar-c4-m8 (priority 10) rebuilt its three idle
  `min_runners` as room freed on compute1 while rungar-c2-m4 had eight jobs
  queued. Decide whether an idle reserve should outrank real work.

Left as it is: `runners ls --scale-set NAME` of a scale set with no runners
lists none rather than failing, since `scale-sets inspect` asks it of scale
sets GitHub alone has, in any runner group.

- **Leftover scale sets, said by the daemon.** `rungar scale-sets ls` shows a
  scale set no longer configured whose runners are still on the fleet, but
  only when asked. Have the daemon say so too: warn once per scale set on
  each reconcile, and add a `rungar_orphaned_runners` metric. Do not remove
  them automatically: two Rungars serving one GitHub URL with different scale
  sets on one fleet is valid, and each would remove the other's.

## Runner sizes

- **`HTTPS_PROXY` silently applies to Dicer hosts.** Log at start which
  endpoints go through a proxy, or have providers ignore the proxy variables
  unless told otherwise.

## Troubleshooting

- **`rungar runners rm` cannot remove a registration with no machine.** An
  offline runner left on GitHub after its machine was lost is "not on the
  fleet", so the only way is GitHub's settings. Remove the registration when
  GitHub has a runner of that name, of one of this installation's scale sets.
- **Nothing notices offline registrations left on GitHub.** Reconciliation
  removes the registrations of runners the daemon knows; after a host is
  lost, those of runners never adopted stay listed. Each reconcile, compare
  GitHub's runners of the scale set with the fleet, and remove those offline
  with no machine.

## Towards a mature project

### Correctness

- **High availability.** One daemon is a single point of failure. A second
  with the same configuration may already stand by -- GitHub refuses it the
  session until the first's ends -- but it adopts and may reconcile runners
  before it has one. Verify, add an explicit lease if needed, and document it.

### Providers and platforms

- **An external provider protocol.** A provider is compiled in today. Define a
  gRPC or exec interface so that a backend can be added without forking, as
  GARM's providers are.
- **A provider conformance suite**: the tests every provider must pass --
  labels, stopped machines, not-found on delete, refusing when full.
- **More providers**: libvirt/QEMU, OpenStack and Azure, and Tart for
  macOS.
- **Try the new providers on real backends.** docker has run against a real
  Docker Engine; proxmox, gcp and aws are tested against fakes only. Check on
  a real Proxmox VE (the API's error wording, task results, the guest agent's
  file-write and its privileges), on Compute Engine (the stockout and quota
  error codes, the startup script, which CPU quota a machine family counts
  against) and on EC2 (the capacity and quota error codes, the user data on
  the major distributions' AMIs, and how a fleet reacts to an instance that
  terminates itself).
- **Registry credentials for docker**: images are pulled without any, so a
  private image must already be on the host.
- **Architecture and OS in the model.** Runners are assumed Linux. ARM hosts,
  macOS and Windows need the runner's architecture and OS to be something
  placement and scale sets reason about.

### Scale and multi-tenancy

- **Schedules**: `min_runners` by time of day -- warm in working hours, zero
  at night.

### Observability

- **Job wait and boot time histograms**: from a job assigned to it started,
  and from a machine created to its runner connected. The numbers a service
  level is set on.
- **Per-repository accounting.** Job messages carry the repository and the
  workflow: runner-seconds per repository, for chargeback and planning, and
  the job each runner is running in `rungar runners ls`.
- **Tracing** of the path from a job message to a runner made, for slow boots.

### Security

- **Check the credential's permissions at start**, and name the one missing,
  rather than failing on the first call that needs it.
- **An audit trail** of the commands that change things -- `rm`, `disable`,
  `enable` -- saying who ran what, when.

### Operability

- **A stability promise for the configuration and the command line**: a
  versioned configuration, deprecation warnings, and `rungar config migrate`.

### Engineering

- **End-to-end tests in CI**, nightly on real hardware, rather than by hand.
- **Scale and chaos tests**: hundreds of runners, providers disappearing,
  GitHub answering 5xx.
- **Fuzzing** of the configuration's parsing and of the YAML editing.
