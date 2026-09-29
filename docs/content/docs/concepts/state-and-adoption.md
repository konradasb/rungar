---
title: State and adoption
weight: 4
description: "Rungar keeps no state: the machines on the fleet, and their labels, are the record."
icon: database
related_title: Next steps
related:
  - /docs/reference/labels
  - /docs/guides/running-the-daemon
---

Rungar has no database and no state file. What runners there are is read
from the fleet itself: every machine Rungar creates carries labels saying
whose it is, and a Rungar that starts finds its runners by them. Everything
else it knows -- which runner is busy, which provider was refusing -- is
rebuilt from what the providers and GitHub say, or starts afresh.

The one file it writes is its event log, `/var/log/rungar/events.jsonl`,
which `rungar events` shows. It is history: nothing Rungar does is decided by
it, and losing it loses only the record of what happened.

## Labels

Every runner's machine carries five labels, which its provider keeps in
whatever form its backend has:

| Label | Value |
|---|---|
| `rungar.sh/managed` | `true`: the machine is Rungar's. |
| `rungar.sh/installation` | The Rungar installation it belongs to. |
| `rungar.sh/scale-set` | Its scale set. |
| `rungar.sh/runner` | Its runner's name, which is also the machine's. |
| `rungar.sh/revision` | The revision of the runner block it was created from. |

Rungar only ever looks at, and only ever removes, machines carrying the first
three with its own values. Anything else on a provider -- another scale
set's runners, another installation's, machines nobody created with Rungar -- is
left alone, and only counted as what it is: room the provider no longer has.

The **installation** is what keeps two Rungars apart. Unless `installation` is
set, it is derived from `github.url`, so one Rungar serving an organisation
and another serving a repository can share providers, and scale sets of the same
name, without either taking the other's runners. Two Rungars serving the
*same* GitHub URL on one fleet must set `installation` to different values;
otherwise each adopts the other's runners.

## Adoption

When a scale set starts, once it holds its message session with GitHub,
Rungar lists the machines carrying its labels, on every one of its
providers:

- A machine that is **starting or running** is adopted: counted as one of the
  scale set's runners, and looked after as if this Rungar had created it.
- A machine that has **stopped** has done its one job, or never will, and is
  deleted, with its registration.

Nothing on the fleet says whether an adopted runner is part way through a
job, so Rungar asks GitHub, and the runner is marked **adopted** and takes the
state GitHub gives it: busy, idle, or starting if it has not connected yet
and is new enough to still be booting. `rungar status` shows both: what the
daemon took it for in its STATE column, and what GitHub says in its GITHUB
column.

If GitHub cannot be asked, the runner is taken for idle. That is still safe,
because of how Rungar removes runners (see
[Runner lifecycle](../runner-lifecycle)): it asks GitHub to remove the
registration first, and GitHub refuses for a runner running a job. A runner
that turns out to be busy is left to finish, and removed when GitHub says its
job completed.

Adoption is not only for starting. Every reconciliation adopts any machine
with the scale set's labels that Rungar is not counting -- one on a provider
that could not be reached at start, say.

## Restarts and stops

Stopping Rungar leaves every runner where it is, busy or idle, and starting
it adopts them. A restart -- which is how a changed configuration or a new
version is taken up -- therefore disturbs no job. Busy runners finish while
Rungar is away, and a runner that finished its job in the meantime has gone
with its machine, or has its stopped machine deleted when it is found.
Idle ones wait, and may take a job GitHub gives them before Rungar is back.

A scale set adopts its runners only once it holds its message session, and
forgets them if it loses it, so two Rungars of one configuration never look
after the same runners: one stands by until the other lets go. See [Running a
standby](../../guides/running-the-daemon#running-a-standby).

## Providers that cannot be reached

A provider that does not answer is not taken for empty: a network problem
says nothing about whether a job on it is still running. Its runners are
kept, and still counted, for five minutes. After that they are written off,
so that the scale set replaces them on providers that do answer.

If the provider comes back, whatever is still on it is adopted again. For a
while the scale set may then have more runners than it needs; the idle
ones are removed as usual, and the busy ones finish their jobs.

## Changing the configuration

Runners are found by their labels, so a change that changes a label leaves
the runners that carry the old one behind: still running, but no longer
looked at or counted by anyone.

- **Changing `github.url`**, or `installation`, changes the installation.
  Change it with the old Rungar stopped and its providers empty. A scale set that
  finds runners of its name under another installation leaves them alone,
  and logs a warning saying so on every start -- which is expected when
  another Rungar, serving another GitHub, shares the fleet.
- **Removing or renaming a scale set** leaves its runners where they are,
  and the scale set on GitHub. Busy ones finish their job and go; idle ones
  take the next job for the old scale set, if any comes, and go. `rungar
  scale-sets ls` shows such a scale set as `LEFTOVER`, and `rungar
  scale-sets rm` removes it for good: its runners, the safe way round, and
  then the scale set on GitHub. See [Draining, pausing and
  removing](../../guides/managing-the-fleet#scale-sets-and-runners).
- **Changing a scale set's runner** -- its size, its image -- applies to the
  runners created after it. Those already running are adopted, and each is
  replaced once it is not running a job: its revision label says it was created
  from the old runner. A busy one finishes its job first. See
  [Runner lifecycle](../runner-lifecycle).

For removing a provider, or taking one out of a scale set's list, see
[Providers](../providers#moving-things-around).
