---
name: release-notes
description: Write the release notes for a Rungar release into the draft GoReleaser made, from the commits and pull requests since the last release. Use when asked to write, draft or update release notes for a tag.
argument-hint: "[tag, e.g. v0.2.0; default: the newest draft]"
disable-model-invocation: true
---

# Release notes

Write the notes for the release `$ARGUMENTS` into its draft on GitHub.
GoReleaser makes the draft, with every asset and an empty body, when the tag
is pushed; see RELEASES.md. The notes are for people who run Rungar: what
changed for them, and what they have to do about it.

## 1. Find the release

- The tag is `$ARGUMENTS`. If none was given, take the newest draft:
  `gh release list --json tagName,isDraft,isPrerelease`.
- `gh release view TAG --json isDraft,isPrerelease,body,assets` must show a
  draft. Never edit a published release unless the user asks you to, in this
  conversation.
- If the draft already has a body, ask before replacing it.
- The previous release is the tag before this one. For a release, skip
  pre-releases; for a pre-release, include them:

  ```console
  git fetch --tags
  git describe --tags --abbrev=0 --match 'v*' --exclude '*-*' TAG^   # a release
  git describe --tags --abbrev=0 --match 'v*' TAG^                   # a pre-release
  ```

  With no previous tag, this is the first release: say so, and describe
  Rungar as a whole rather than listing commits.

## 2. Gather what changed

- The commits: `git log --reverse --format='%H %s' PREV..TAG`.
- Pull requests are squashed, so a subject ending in `(#N)` is one. Read
  each one's description, which says why, not only what:
  `gh pr view N --json title,body,author,labels`.
- For a commit with no pull request, read the commit itself: `git show --stat SHA`.
- Check what RELEASES.md promises to keep stable, for changes a subject may
  not admit to:

  ```console
  git diff --stat PREV..TAG -- go.mod internal/daemon/ internal/scaleset/ internal/types/ internal/metrics/
  ```

  and read the diff where the stat is not enough.

## 3. Sort it

Every change goes in one place, or none:

- **Breaking changes**: a `!` after the type, `BREAKING CHANGE` in the body,
  or anything that changes what RELEASES.md promises: the configuration
  file, `rungar`'s commands and flags, the labels on runner instances, the
  metrics, or the oldest Dicer it works with. Each says what to do about it.
- **New features** (`feat`): what a user can now do, not how it was built.
- **Fixes** (`fix`): the problem a user saw, not the code that changed.
- **Left out**: `chore`, `ci`, `test`, `refactor`, `docs` and dependency
  updates, unless a user would notice them. A dependency update that fixes a
  vulnerability in Rungar goes in Fixes, with its advisory ID.

Merge commits that are one change seen twice, and leave out changes undone
within the release.

## 4. Write it

Follow [template.md](template.md). For the voice, read the last release's
notes (`gh release view PREV --json body --jq .body`), README.md and
example.yml: plain, short sentences, no marketing words, and what a user sees
rather than what the code does.

- Name the Dicer release Rungar was built against, the
  `github.com/konradasb/dicer` version in go.mod at TAG
  (`git show TAG:go.mod`), as the oldest Dicer it works with.

- Link each item to its pull request, `(#N)`, which GitHub turns into a link.
- Link README.md or example.yml at the tag,
  `https://github.com/konradasb/rungar/blob/TAG/...`, where they explain more.
- Credit contributors by GitHub login: a pull request's author, or for a
  commit with no pull request,
  `gh api repos/konradasb/rungar/commits/SHA --jq .author.login`. Never write
  an email address, or a name from a commit's author line. Leave out bots.
- For a pre-release, say at the top that it is one, and that it promises
  nothing.

Write the notes to a file in the scratchpad directory, and show them to the
user in full.

## 5. Put them in the draft

Only once the user approves them:

```console
gh release edit TAG --notes-file NOTES.md
```

Then check the draft shows them (`gh release view TAG --json body`), and give
the user its URL. Do not publish it: the maintainer does, after reviewing the
draft.
