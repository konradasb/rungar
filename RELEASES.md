# Releases

## Versioning

Rungar follows [Semantic Versioning](https://semver.org): a release is
`MAJOR.MINOR.PATCH`, tagged `vMAJOR.MINOR.PATCH`.

- **Major**: something that worked stops working, or works differently.
- **Minor**: something new, that leaves what worked working.
- **Patch**: fixes only.

What a version makes promises about:

- The configuration file
- The commands and flags of `rungar`; not the wording of `rungar status` or
  of the log, which are for people
- The labels Rungar puts on the machines it creates, `rungar.sh/*`, and
  the installation ID it derives from the GitHub URL: a newer Rungar adopts the
  runners an older one left on the fleet
- The provider types, and what each one's configuration says
- The names and labels of the Prometheus metrics
- The oldest Dicer release the dicer provider works with, which each
  release's notes name

Anything under `internal/` is not covered.

Until 1.0.0, a minor release may break any of these, and says so in its
release notes; a patch release never does. A pre-release is tagged
`v1.2.0-rc.1`, and promises nothing.

### Deprecation

Nothing in the configuration or the command line is taken away without a
warning first. A key, a value's meaning, a command or a flag that is to go
is deprecated: it keeps working, and whoever uses it is told, for at least
one minor release before a later major release removes it (before 1.0.0, a
later minor release). The release notes say when each is deprecated and
when it is removed.

- **The configuration** says which version it is written for, in
  `version`, which changes only when a key is removed or a value changes
  meaning; a key added leaves it alone. A Rungar reads every version since
  the last major release, and a file with anything deprecated in it has the
  daemon log a warning at start, and `rungar validate` and `rungar config`
  print one. `rungar config migrate` rewrites the file for the version that
  Rungar writes, meaning what it did.
- **A command or a flag** deprecated still works, and says so on stderr
  each time it is used, with what replaces it. It is hidden from `--help`
  and the reference.

## Creating a release

Releasing takes a maintainer, and `main` passing CI.

1. **Choose the version.** Look at what changed since the last release, by
   PR title: a `feat:` is a minor release, a `fix:` a patch release, and a
   breaking change a major one (a minor one before 1.0.0).

2. **Tag `main` and push the tag.**

   ```console
   git switch main && git pull
   git tag -a v0.2.0 -m v0.2.0
   git push origin v0.2.0
   ```

   The [release workflow](.github/workflows/release.yaml) builds a draft
   release with GoReleaser: `rungar` for Linux and macOS, on amd64 and
   arm64, the `rungar` deb and rpm packages, with checksums signed by
   cosign, and SBOMs. Its notes are left empty.

3. **Write the release notes.** In Claude Code, in this repository:

   ```text
   /release-notes v0.2.0
   ```

   The [skill](.claude/skills/release-notes/SKILL.md) reads the commits and
   pull requests since the last release, writes the notes as its
   [template](.claude/skills/release-notes/template.md) lays them out, and
   shows them to you; once you approve them, it puts them in the draft.
   Breaking changes come first, each with what to do about it. Running the
   release workflow again replaces the draft, notes and all.

4. **Review the draft and publish it.** Check the notes as GitHub shows
   them. For a pre-release, tick *Set as a pre-release*.

   Publishing runs the [image workflow](.github/workflows/image.yaml), which
   pushes `ghcr.io/konradasb/rungar` for linux/amd64 and linux/arm64, signed
   with cosign: tagged `0.2.0`, `0.2` and `latest` for a release, and only
   its version for a pre-release. Check it succeeds.

   It also runs the [chart workflow](.github/workflows/chart.yaml), which
   pushes the Helm chart to `oci://ghcr.io/konradasb/charts/rungar`, signed
   with cosign, versioned as the release and running its image. Check it
   succeeds too.

   Publishing a release, not a pre-release, also runs the
   [packages workflow](.github/workflows/packages.yaml), which adds its
   packages to the apt and dnf repository at `pkg.rungar.sh`;
   [build](build/README.md) covers how, and setting it up. Check it
   succeeds.

A release is installed from the package repository, or with the install
script's `--version`:

```console
curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash -s -- --version v0.2.0
```

## Verifying a release

The checksums and the image are signed with cosign by the workflows
themselves, with no key to keep. Check the checksums, then the archives
against them:

```console
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity https://github.com/konradasb/rungar/.github/workflows/release.yaml@refs/tags/v0.2.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --check --ignore-missing checksums.txt
```

And the image, and the chart:

```console
cosign verify ghcr.io/konradasb/rungar:0.2.0 \
  --certificate-identity-regexp '^https://github\.com/konradasb/rungar/\.github/workflows/image\.yaml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify ghcr.io/konradasb/charts/rungar:0.2.0 \
  --certificate-identity-regexp '^https://github\.com/konradasb/rungar/\.github/workflows/chart\.yaml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
