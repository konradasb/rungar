# Security Policy

## Supported Versions

Only the latest release of Rungar receives security fixes. We do not backport
security patches to older versions.

| Version | Supported |
|---------|-----------|
| Latest  | Yes       |
| Older   | No        |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

If you believe you have found a security vulnerability in Rungar, please report
it privately using one of the following methods:

- **GitHub Security Advisories**: [Report a vulnerability](https://github.com/konradasb/rungar/security/advisories/new) (preferred)
- **Email**: Contact a maintainer listed in [MAINTAINERS.md](MAINTAINERS.md) directly

Please include as much of the following information as possible to help us
understand and reproduce the issue:

- Type of issue (e.g. credential disclosure, privilege escalation, unauthorized job execution)
- The component or file(s) involved
- Steps to reproduce the issue
- Proof-of-concept or exploit code (if available)
- Impact assessment — what an attacker could achieve

## Response Process

1. We will acknowledge receipt of your report within **3 business days**
2. We will investigate and provide an initial assessment within **7 business days**
3. We will work with you to understand the severity and develop a fix
4. We will coordinate a disclosure timeline — typically **90 days** from the report date, or sooner if a fix is available
5. We will credit reporters in the release notes unless they prefer to remain anonymous

## Scope

The following are in scope for security reports:

- Handling of GitHub credentials — the App private key, tokens, and the
  just-in-time runner registrations Rungar generates
- Anything that would let one runner's job affect another's, or reach the
  Rungar daemon
- Anything that would let a configuration produce a VM that is not an
  ephemeral, single-job runner
- Dependency vulnerabilities with a direct, exploitable path in Rungar

Out of scope:

- Vulnerabilities in Dicer itself — report those to
  [konradasb/dicer](https://github.com/konradasb/dicer/security/advisories/new)
- Vulnerabilities in the GitHub Actions runner or the scale set API — report
  those to GitHub
- The host kernel or hypervisor — report those upstream
- Issues in third-party dependencies with no exploitable path in this project

## Security Considerations for Users

Rungar holds credentials for a GitHub organisation and has full control of every
host its providers are pointed at. Operators should:

- Prefer a GitHub App to a personal access token: it is scoped to what it
  needs, and GitHub rotates it. Give it no more than the scale set requires
- Keep the App private key and any token in files owned by root and readable
  only by the daemon's user. Rungar reads them by path so that whatever manages
  secrets on the host stays in charge of them
- **Serve each Dicer host's API with TLS and client certificates**, and give
  Rungar its certificate in the dicer provider's `tls`. Anyone who can reach a host's API has
  full control of that host -- a client that can define instances can read
  any file on it through one. A host served without TLS is plaintext and
  authenticates neither end, so anything on the network path can read and
  alter the traffic; if you run one that way, put the fleet on a private
  network and treat that network as part of the hosts' security boundary.
  Keep Rungar's client key as you keep the App key
- Remember what a runner is: it executes whatever a workflow says. Use runner
  groups to decide which repositories may reach this scale set, and do not
  point a public repository's untrusted workflows at a fleet you care about
- Give runners their own network if their jobs should not reach the rest of
  your infrastructure; `network:` in the runner spec names a Dicer network
- Keep Rungar, Dicer and the runner image updated

## Disclosure Policy

We follow a coordinated disclosure model. We ask that reporters give us
reasonable time to develop and release a fix before public disclosure. We will
always credit reporters (with their consent) in our security advisories.
