---
title: GitHub credentials
weight: 1
description: "A GitHub App or a personal access token, with the permissions each scope needs."
icon: key
related:
  - /docs/reference/configuration
  - /docs/concepts/scale-sets
---

Rungar talks to GitHub as one identity, for every scale set it runs. With it,
Rungar:

- finds each scale set on GitHub, and creates it if it is not there;
- holds each scale set's message session;
- registers every runner, and removes the registration of one that never
  ran a job;
- lists the runners of the repository, organisation or enterprise, to see
  which are connected and which are busy.

The identity is a **GitHub App** or a **personal access token**, never both.
Which scale sets it may manage follows from `github.url`: a repository, an
organisation, or an enterprise.

## Which to use

A GitHub App is the one to prefer. It is given exactly the permissions below
and nothing else, it belongs to the organisation rather than to a person who
may leave, and the tokens Rungar uses with it are minted by GitHub for an hour
at a time -- Rungar never holds a long-lived secret but the App's private key.

A token is simpler to set up, and is what an **enterprise** needs: GitHub
does not let an App manage runners at the enterprise level, and a
configuration with an App and an enterprise `github.url` is refused when it
loads.

## Permissions

| `github.url` | GitHub App | Fine-grained token | Classic token |
|---|---|---|---|
| A repository | Repository: **Administration** read and write, **Metadata** read | Repository: **Administration** read and write | `repo` |
| An organisation | Organisation: **Self-hosted runners** read and write; Repository: **Metadata** read | Organisation: **Self-hosted runners** read and write, **Administration** read | `admin:org` |
| An enterprise | not possible | not possible | `manage_runners:enterprise` |

A scale set is created in the runner group `runner_group` names, `Default`
unless set; the identity must be able to use that group. For an organisation,
the group decides which repositories may use the scale set.

## A GitHub App

1. Create the App under the organisation that owns the runners:
   **Settings → Developer settings → GitHub Apps → New GitHub App**. It needs
   a name and a homepage URL, and nothing else: untick **Webhook → Active**,
   since Rungar takes nothing from GitHub but what it asks for.
2. Give it the permissions in the table above for its `github.url`, and no
   others.
3. Note its **Client ID**, at the top of its settings page: `Iv23li...`. Its
   numeric App ID works too.
4. Under **Private keys**, generate one. GitHub hands over a `.pem` file,
   once.
5. **Install App**, on the organisation, for the repositories it should
   serve. The installation's settings page is at a URL ending in its
   **installation ID**:
   `https://github.com/organizations/my-org/settings/installations/12345678`.

Then:

```yaml
github:
  url: https://github.com/my-org
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem
```

## A token

A fine-grained token is limited to one owner and to the permissions it is
given; a classic token has every scope it is given, on everything its owner
can reach. Either way it acts as the person who created it: a machine account
of its own is better than someone's own account.

Create it under **Settings → Developer settings → Personal access tokens**,
with the permissions in the table above, and an expiry you will act on. When
it expires, Rungar stops working.

```yaml
github:
  url: https://github.com/my-org
  token_path: /etc/rungar/token
```

## Keeping it secret

Each credential can be given in the configuration file (`app_private_key`,
`token`), or as the path of a file holding it (`app_private_key_path`,
`token_path`). A path is better: the configuration stays free of secrets and
can be shown, kept in version control, or pasted into an issue, and whatever
manages secrets on the host stays in charge of the file.

The file should be readable by Rungar and nobody else. The install script
runs `rungar` as the `rungar` user and makes `/etc/rungar` readable by root
and that group only; a credential in it is best owned the same way:

```console
$ sudo install -o root -g rungar -m 0640 app.pem /etc/rungar/app.pem
```

Given inline, the configuration file itself is the secret, and is best
permissioned as one.

As ssh does of a private key, `rungar` warns at start of a secret anyone on
the host may read -- the token or key file; a provider's credentials file,
TLS private key or Proxmox `token_secret_path`; and the configuration file
when a secret is written in it, a GitHub credential or a provider's, such as
Proxmox's `token_secret`. The owner and the group are trusted; others are
not:

```text
level=WARN msg="a file holding a secret is readable by anyone on the host; make it readable by its owner and group alone" file=/etc/rungar/app.pem mode=0644
```

It warns rather than refusing to start.

`rungar status` never prints a credential: it says only where one was read
from, or that it was given inline.

## Rotating

- **An App's key**: generate a second key on the App, and replace the file
  with it, and restart Rungar. Then delete the old key on GitHub. The hourly
  installation tokens need nothing: Rungar mints a new one when the last is
  about to expire.
- **A token**: create the new one, replace the file, and restart Rungar.

Rungar reads the files its configuration names when it starts. A restart
disturbs no running job; see
[Running the daemon](../running-the-daemon#changing-the-configuration).
A credential given inline is part of the configuration file, and is picked
up the same way.

## GitHub Enterprise

`github.url` is the address the organisation or repository has in a
browser, on any GitHub:

- **GitHub Enterprise Cloud with data residency**:
  `https://my-company.ghe.com/my-org`. Its API is at `api.my-company.ghe.com`.
- **GitHub Enterprise Server**: `https://github.example.com/my-org`. Its API
  is at `/api/v3` on the same host.

An enterprise is `https://github.com/enterprises/my-enterprise`, or the same
path on its own host.

## Checking it

A credential is first used when `rungar` starts: each scale set is looked up
on GitHub before any runner is created, so a credential that is wrong, or lacks
a permission, stops the daemon at once, with GitHub's answer in the log:

```console
$ journalctl -u rungar -n 20
```

A `401` is a credential GitHub does not accept -- a wrong key, an expired
token, an App not installed where `github.url` points. A `403` or `404` is a
credential without the permission, or without access to the repository.
