---
title: Introduction
weight: 1
description: "GitHub Actions runners, a fresh machine for every job, on hosts of your own and in the cloud."
---

Rungar runs GitHub Actions runners, a fresh machine for every job, on hosts of
your own and in the cloud.

It keeps a GitHub runner scale set supplied with runners: each is a machine
created for one job and deleted after it, so nothing is shared between two
jobs. The daemon, `rungar`, learns from GitHub how many jobs are waiting and
creates that many runners through its providers -- Dicer hosts,
Proxmox VE, Google Cloud and AWS -- and the `rungar` command line shows and
steers what it is doing.

{{< section-cards >}}
