# TODO: the gcp provider

## Before it is relied on

- **Try it on real Compute Engine.** The provider is tested against a fake
  only. Check the stockout and quota error codes, and whether they come in
  the request or the operation; that the default startup script runs on
  Google's public images; and which CPU quota each machine family counts
  against.
- **A Spot VM taken back is recorded as lost.** With
  `instanceTerminationAction: DELETE`, a preempted instance disappears from
  `List`, and the reconciler forgets its runner as `LossGone` ("Runner lost:
  its VM on gcp is gone"), not as stopped. Use `STOP`, so that Rungar sees it
  stopped and deletes it itself, as it does an instance whose runner has
  exited.
- **Host maintenance kills the job.** Every instance has
  `onHostMaintenance: TERMINATE`, so a maintenance event stops it and fails
  the job it runs. Instances that are not Spot and have no GPU can live
  migrate: use `MIGRATE` for them. Automatic restart stays off either way.

## At scale

- **A regional quota is retried in every zone.** CPU and address quotas are
  the region's, so `QUOTA_EXCEEDED` in one zone will be the same in the
  next. Return no capacity at once, as the aws provider does for its
  account-wide quotas, rather than trying (and cleaning up after) each zone.
- **Compute Engine's read quota.** Every scale set lists every zone every
  reconcile pass. With many scale sets and zones in one project, the
  per-minute read quota fails `List` and the provider looks unreachable.
  Share one short-lived listing per provider, or list with an aggregated
  call across the region.
- **Leaked instances run until someone notices.** If Rungar is gone, an
  instance whose runner hangs keeps running. Set `scheduling.maxRunDuration`
  with a `DELETE` action from the scale set's `max_age`, so Compute Engine
  removes it itself.

## Features

- **Instance templates**: an `instance_template` key would cover GPUs, local
  SSDs, Shielded and Confidential VMs, and minimum CPU platforms without a
  Rungar key for each.
- **Fallback machine types**, such as `[c3-standard-4, n2-standard-4]`,
  tried before the next zone. On Spot it finds more capacity than more
  zones.
- **GPUs**: `guest_accelerators`, now that instances already terminate on
  host maintenance, as GPUs require.
- **Workload Identity on GKE.** Application Default Credentials should find
  it already; the chart does not say how, and its values say nothing of
  Google Cloud.
- **Service account impersonation**: a key to act as another service
  account, rather than a key file.

## Minor

- **Say who can read the registration.** Anyone with
  `compute.instances.get` in the project can read an instance's metadata,
  and with it the registration, until the runner has used it. The reference
  page says only that a job can.
- **`credentials_file` takes service account keys alone.** An external
  account (workload identity federation) file is refused by `CheckFiles`,
  though Application Default Credentials would accept it.
