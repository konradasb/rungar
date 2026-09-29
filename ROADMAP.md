# Roadmap

What Rungar is for, what is planned, and what is deliberately not.

## What Rungar is

One daemon that keeps one GitHub Actions runner scale set supplied with
runners, each a VM, across its providers -- Dicer hosts, to start with. It holds no state of its own: the VMs on the fleet are the record. Its event
log is history, which nothing it does is decided by.

## Now

Working and covered by tests, including end-to-end tests against a real fleet:

- The scale set message session, and scaling on assigned jobs -- up, and
  down again without ever taking a runner away mid-job
- Just-in-time registration, one per runner, never reused, and removed again
  for a runner that never ran a job
- Placement across providers, spread or packed, with weights, by room where a
  provider reports it
- Adoption of the runners a previous daemon left behind
- Reconciliation against the fleet: VMs that have gone, VMs that have
  stopped, and registrations GitHub has dropped
- Several scale sets in one daemon, each with its own label, ceiling and size
  of runner, sharing one fleet
- `rungar status`, reporting what is running on the fleet
  without needing the daemon
- Priority between scale sets, so a size of small runners cannot starve a
  large one
- Draining a provider, and backing off one that keeps refusing
- Pausing a scale set while the daemon runs, or in the configuration,
  without disturbing a job
- Pinning the runner image by digest, and refusing a tag when the fleet
  requires one
- Restarting, to upgrade or take up a changed configuration, without
  disturbing a job
- Runs anywhere Go does -- Linux, macOS, a 6 MB container -- since Rungar only
  ever talks to Dicer over the network
- Prometheus metrics
- An event log, `rungar events`: runners created, adopted, removed and lost,
  with why, and providers found full or failing, kept across restarts
- Mutual TLS to each Dicer host
- Providers: what runners run on is a provider, Dicer is the first, and a
  new backend is a package that leaves the rest of Rungar alone
- An installation ID on every runner, so that two Rungars can share a fleet
- Runners that never connect to GitHub -- a broken image, a network that
  cannot reach it -- found and replaced, rather than left standing in for a
  runner that could take a job
- A signed container image for linux/amd64 and linux/arm64, published with
  every release

## Next

- **A pull-through cache for runner images.** Every host pulls the runner
  image itself today. A cold host's first runner waits for that pull.

## Perhaps

- **Placement by something other than free capacity.** Affinity to a host with
  the image already pulled, or spreading a workflow's jobs deliberately.
- **A Kubernetes controller.** Only if there is a reason to prefer it to
  running the daemon.

## Not planned

- **Replacing `actions-runner-controller`.** Rungar is for people who want VMs
  on their own hosts. If Kubernetes is where the runners should live, the
  controller is the better tool.
- **Managing the Dicer hosts.** Rungar places runners on hosts; it does not
  install, configure, patch or monitor them.
- **A runner image of our own.** The official `ghcr.io/actions/actions-runner`
  image is the one to use, and any image carrying the runner works.
- **Secrets management.** Credentials are read from paths in the
  configuration. Whatever manages secrets on the host stays in charge of them.
- **Scaling on anything but the scale set statistics.** GitHub's
  `TotalAssignedJobs` is the number that is always current. Counting messages
  or maintaining a queue would be guessing.
