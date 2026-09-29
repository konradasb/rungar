---
title: Introduction
weight: 1
description: "GitHub Actions runners, a fresh machine for every job, on hosts of your own and in the cloud."
---

Rungar runs GitHub Actions runners, a fresh machine for every job, on hosts of
your own and in the cloud.

It keeps a GitHub runner scale set supplied with runners: each is a machine
made for one job and destroyed after it, so nothing is shared between two
jobs. The daemon, `rungar`, learns from GitHub how many jobs are waiting and
makes that many runners through its providers -- Dicer hosts, Docker hosts,
Proxmox VE, Google Cloud and AWS -- and the `rungar` command line shows and
steers what it is doing.

{{< section-cards >}}
