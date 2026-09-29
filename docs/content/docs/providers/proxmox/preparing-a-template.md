---
title: Preparing a template
weight: 1
description: "The template VM a proxmox provider's runners are cloned from."
icon: document-duplicate
---

Runners are cloned from a template VM. It needs `qemu-guest-agent` enabled,
the Actions runner in `/home/runner` owned by a `runner` user, a
`/run/rungar` directory, and a unit that waits for the registration Rungar
writes, runs one job, and powers off.

Rungar writes the registration through the guest agent, which does not create
missing directories, and `/run` is emptied at every boot. Have
systemd-tmpfiles create the directory early in boot, before the guest agent
starts:

```ini
# /etc/tmpfiles.d/rungar.conf
d /run/rungar 0700 root root -
```

systemd-tmpfiles reads it at every boot; nothing needs enabling.

```ini
# /etc/systemd/system/rungar-runner.service
[Unit]
After=network-online.target qemu-guest-agent.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/rungar-runner
ExecStopPost=/usr/bin/systemctl poweroff

[Install]
WantedBy=multi-user.target
```

```sh
#!/bin/sh
# /usr/local/bin/rungar-runner
set -eu
jit=/run/rungar/jitconfig
while [ ! -s "$jit" ]; do sleep 1; done
export ACTIONS_RUNNER_INPUT_JITCONFIG="$(cat "$jit")"
rm -f "$jit"
cd /home/runner
exec runuser -u runner -- ./run.sh
```

Enable both, power the VM off, and convert it:

```console
# systemctl enable qemu-guest-agent rungar-runner
# poweroff
```

```console
# qm template 9000
```

A `jit_path` other than `/run/rungar/jitconfig` must match the script's, and
its directory must exist in the VM before Rungar writes to it: change the
tmpfiles line to create it.
