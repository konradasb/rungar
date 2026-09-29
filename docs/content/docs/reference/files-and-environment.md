---
title: Files and environment
weight: 7
description: "Where Rungar keeps things on its host and in its container, and the environment variables it reads."
icon: folder-open
---

Where Rungar keeps things, and the environment variables it reads. Rungar keeps
no state of its own -- the fleet is the record -- so there is little: the
binary, the configuration, the credentials it names, the socket the daemon
serves its command line on, and its event log.

## The daemon's host

| Path | Description |
|---|---|
| `/usr/bin/rungar` | The daemon and its command line, as the package installs it. `install.sh` puts it in `/usr/local/bin`. |
| `/etc/rungar/config.yaml` | The configuration. `--config` (`-f`) names another. |
| `/etc/rungar/` | The configuration's directory, root's and readable by the `rungar` group alone. Credentials and TLS material the configuration names are best kept here, owned `root:rungar`, mode `0640`. |
| `/usr/lib/systemd/system/rungar.service` | The service, as the package installs it. `install.sh` writes it to `/etc/systemd/system`. It runs `rungar` as the `rungar` user. |
| `/usr/share/rungar/example.yml` | A configuration describing every setting, as the package installs it. |
| `/usr/lib/sysusers.d/rungar.conf` | The `rungar` user and group, as the package declares them to systemd. |
| `/usr/share/bash-completion/completions/rungar`, `/usr/share/fish/vendor_completions.d/rungar.fish`, and `_rungar` in zsh's directory | Shell completions, as the package installs them. With `install.sh`, `rungar completion bash` (or `zsh`, `fish`) writes them. |
| `/run/rungar/rungar.sock` | The socket the daemon serves the other commands on, readable and writable by `rungar` and root alone. The `socket` key moves it. |
| `/var/log/rungar/events.jsonl` | The events `rungar events` shows, one JSON object a line, readable and writable by `rungar` alone. `events.file` moves it. |

`rungar` writes nothing to disk but its socket and its events. The service
gives it `/run/rungar` for the socket, which goes when it stops, and
`/var/log/rungar` for the events, which stays; it runs it with the
filesystem read-only but for those and `/tmp`, which is its own. The events
are history: removing the file loses what `rungar events` shows, and
nothing else.

The paths a configuration names -- `github.token_path`,
`github.app_private_key_path`, and a provider's TLS files -- are read as the
`rungar` user, and must be readable by it. They are read when the daemon
starts, as the configuration file is, so a change to one is taken up by
restarting it; see [GitHub
credentials](../../guides/github-credentials#rotating).

A provider that reaches a Dicer daemon on the same machine by its socket
needs the socket's group: add it to the service with `SupplementaryGroups=`.

## The container

| Path | Description |
|---|---|
| `/usr/local/bin/rungar` | The daemon, which is the image's entrypoint. |
| `/etc/rungar/config.yaml` | The configuration it is started with, by default: mount it, and the credentials it names, into the container. |
| `/run/rungar/rungar.sock` | The socket `docker exec` runs the other commands against. With the root filesystem read-only, `/run/rungar` needs a tmpfs of its own. |
| `/var/log/rungar/events.jsonl` | The events. `/var/log/rungar` is a volume, so that they outlive the container: name one to keep them across a new container too. |

The image is distroless, and runs as the `nonroot` user, UID 65532: what is
mounted in must be readable by it. It exposes port 9102, the metrics port,
which can be reached from outside the container only with `metrics.enable`
set and `metrics.listen` at `0.0.0.0:9102`, rather than the default loopback.
See [Installation](../../getting-started/installation#run-it-in-a-container).

## On a runner

What Rungar puts on a runner's machine, for every provider:

| What | Description |
|---|---|
| Labels | `rungar.sh/managed`, `rungar.sh/installation`, `rungar.sh/scale-set`, `rungar.sh/runner` and `rungar.sh/revision`, in whatever form the backend keeps labels. See [Labels](../labels). |
| The registration | The runner's just-in-time configuration, delivered as the provider's backend allows. |

The `dicer` provider delivers the registration in the machine's environment:

| Variable | Description |
|---|---|
| `ACTIONS_RUNNER_INPUT_JITCONFIG` | The registration, which the runner reads at start. It cannot be set in `env`. |
| `RUNNER_ALLOW_RUNASROOT` | `1`, so that the runner starts as root: the VM is the isolation. `env` can override it. |

## Environment variables

| Variable | Description |
|---|---|
| `NO_COLOR` | Set to anything to turn colour off in `rungar status`. Output that is not to a terminal never has any. |
| `RUNGAR_SOCKET` | The daemon's socket, for the commands that ask it, where it is not `/run/rungar/rungar.sock`. `--socket` says the same, and wins. |
| `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` | A proxy for Rungar's connections. They apply to GitHub, and to a provider's backend reached over the network -- a Dicer host's API included, so list the hosts in `NO_PROXY` if they are to be reached directly. |

Everything else Rungar is told comes from its configuration file: there is no
setting of the daemon's that an environment variable changes.
