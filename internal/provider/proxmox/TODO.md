# TODO: the proxmox provider

## Before it is relied on

- **Runners made at once all go on one node.** `chooseNode` picks the node
  with the most memory free, as `/cluster/resources` reports it: memory in
  use now, not memory promised to VMs still cloning or booting. A burst of
  Creates all see the same free memory and all pick the same node, which
  then runs out of memory. Count what this provider has placed and not yet
  seen in use, or read each node's configured VM memory, before choosing.
- **Try it on a real Proxmox VE.** The provider is tested against a fake
  only. Check the error wording it matches on (`already exists`, `local
  storage`, `does not exist`, `not running`, the out-of-memory and out-of-disk
  messages), task results, and the guest agent's `file-write` and the
  privileges it needs, on Proxmox VE 8 and 9.
- **Errors are matched on their English wording.** `isGone`, `isNotRunning`,
  `createError` and `clone` look for phrases in the message. A reworded
  message in a new release turns a full node into a failing one, or a gone VM
  into a Delete that never succeeds. Pin the phrases with tests taken from
  real responses, and prefer HTTP status codes where the API gives them.

## At scale

- **Listing costs a call per VM.** `List` reads each tagged VM's config to
  get its labels from the description: one request per runner, for every
  scale set, every reconcile pass, with no bound on the whole. Keep the
  labels somewhere `/cluster/resources` returns, or cache descriptions by
  VMID, since they never change after Create.
- **VMs on a node that is down cannot be deleted.** A VM on an offline node
  lists as stopped, so Rungar removes it, and the stop and destroy fail until
  the node is back. Leave VMs on an unreachable node alone, rather than
  failing to delete them every pass.
- **Deleting a VM that is still cloning fails.** A VM locked by its clone is
  refused by destroy until the clone ends. Wait for the lock, or say why in
  the error.
- **The endpoint is a node, not the cluster.** Two providers pointing at two
  nodes of one cluster pass the endpoint check, and each lists and counts
  the other's VMs. Ask the cluster for its name, or document it.
- **Disk space is not checked before a clone.** A full clone onto storage
  without room fails only once it is tried. Read the storage's free space
  when choosing a node.

## Features

- **Disk size.** A runner gets the template's disk as it is. Add a
  `disk_size` that resizes the clone's boot disk before it starts.
- **Network**: the bridge and VLAN of a runner's NIC, rather than the
  template's.
- **Cloud-init** as another way to deliver the registration, for templates
  without the guest agent.
- **CPU type, ballooning and NUMA**, which build-heavy runners care about.
- **Choosing nodes by more than memory**: CPU load, or a spread across nodes
  rather than always the emptiest.

## Minor

- **The registration goes through the API.** It is sent in a `file-write`
  request, so anyone who can read the API's access logs or holds
  `VM.GuestAgent.FileWrite` on the VM can see it until it is used. Say so in
  the reference.
- **Updating a template changes its VMID**, which replaces every runner made
  from the old one. Document the workflow: clone a new template, change
  `template`, and delete the old one once no linked clone uses it.
