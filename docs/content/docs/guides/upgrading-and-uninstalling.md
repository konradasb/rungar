---
title: Upgrading and uninstalling
weight: 5
description: "Upgrading Rungar without disturbing a job, and removing it with what it made."
icon: arrow-circle-up
related:
  - /docs/getting-started/installation
  - /docs/guides/running-the-daemon
---

A restart disturbs no running job, so upgrading Rungar is installing the new
version and restarting the daemon; see
[Stopping and restarting](../running-the-daemon#stopping-and-restarting).

## Upgrading

Upgrade the package, as any other:

```console
$ sudo apt update && sudo apt install rungar
$ sudo dnf upgrade rungar
```

It restarts a running daemon once the new `rungar` is in place, having
checked the configuration with it first: a configuration the new version
will not load -- a release that changes it says so in its notes -- leaves
the old daemon running, and says so. Fix it, then check it and restart:

```console
$ sudo -u rungar rungar validate
$ sudo systemctl restart rungar
```

The package replaces the binary and the unit, and leaves `/etc/rungar`
alone.

Installed with `install.sh`, run it again, for the latest release or the
one given, check the configuration with the new binary, and restart:

```console
$ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash -s -- --version v0.1.0
$ sudo -u rungar rungar validate
$ sudo systemctl restart rungar
```

`install.sh` replaces the binary and the unit, and leaves the configuration
alone. The running daemon carries on with the old binary until it is
restarted, so a configuration the new version will not load -- a release
that changes it says so in its notes -- is found by `rungar validate`, and
can be fixed before the restart rather than after a daemon that does not
start.

## Uninstalling

Remove the scale sets first, while the daemon is running. Uninstalling stops
it, and what it made stays: its runners on the fleet, still registered, and
its scale sets on GitHub, still offered to workflows that nothing will serve.
A Rungar installed again with the same configuration adopts them all, which
is why neither the package nor the script removes them.

Empty `scale_sets` in the configuration, restart the daemon, and remove each
one, now `LEFTOVER`, as
[Removing a scale set](../managing-the-fleet#removing-a-scale-set) says:

```console
$ sudo systemctl restart rungar
$ sudo -u rungar rungar scale-sets ls
$ sudo -u rungar rungar scale-sets rm rungar-c2-m4 --wait
```

Then remove Rungar the way you installed it:

{{< tabs >}}
  {{< tab name="Debian, Ubuntu" >}}
  ```console
  $ sudo apt purge rungar
  ```

  This removes `rungar`, the service, `/etc/rungar`, with the credentials
  kept there, and the events in `/var/log/rungar`. `apt remove` keeps both
  directories. The `rungar` user is kept either way.
  {{< /tab >}}
  {{< tab name="Fedora, RHEL, Rocky, AlmaLinux" >}}
  ```console
  $ sudo dnf remove rungar
  ```

  This removes `rungar` and the service. `/etc/rungar` is kept while
  anything is in it -- the configuration, the credentials -- as are the
  events in `/var/log/rungar` and the `rungar` user.
  {{< /tab >}}
  {{< tab name="openSUSE" >}}
  ```console
  $ sudo zypper remove rungar
  ```

  This removes `rungar` and the service. `/etc/rungar` is kept while
  anything is in it -- the configuration, the credentials -- as are the
  events in `/var/log/rungar` and the `rungar` user.
  {{< /tab >}}
  {{< tab name="The install script" >}}
  ```console
  $ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/uninstall.sh | sudo bash
  ```

  This removes `rungar`, the service and any drop-ins made for it, and
  `/etc/rungar`, with the credentials kept there. The events in
  `/var/log/rungar` are kept, and the `rungar` user with them, unless
  `--purge` is given:

  ```console
  $ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/uninstall.sh | sudo bash -s -- --purge
  ```
  {{< /tab >}}
{{< /tabs >}}

Credentials Rungar used live on after it: delete the GitHub App's private key
on GitHub, or revoke the token, when nothing else needs them.
