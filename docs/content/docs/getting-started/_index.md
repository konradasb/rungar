---
title: Getting started
weight: 1
description: "Install Rungar, point it at GitHub and a Dicer host, and run a first job on a VM of its own."
icon: play
related_title: Learn more
related:
  - /docs/concepts/how-rungar-works
  - /docs/concepts/scale-sets
---

Rungar runs GitHub Actions jobs on machines of your own: each job
gets a fresh machine, which takes that one job and is then deleted, so
nothing is shared between two jobs. It keeps a GitHub runner scale set
supplied with them, on Dicer hosts, Proxmox VE, Google Cloud or AWS.

Getting started takes two steps: install Rungar, then point it at GitHub and
a Dicer host and run a first job.

{{< section-cards >}}
