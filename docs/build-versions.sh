#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Builds every version of the documentation that docs/versions lists into
# one directory, as the site is published: each under its own path, with a
# switcher between them, the ones that are not the latest release saying
# so, and the site's root leading to the latest.
#
#   docs/build-versions.sh HUGO OUT
#
# DOCS_BASE_URL is where the site is published; https://rungar.sh by default.
#

set -euo pipefail

hugo=$1
out=$2
base_url=${DOCS_BASE_URL:-https://rungar.sh}
base_url=${base_url%/}
# The path the site is published under, for links between versions.
prefix=$(sed -E 's#^[a-z]+://[^/]+##' <<<"$base_url")

root=$(git rev-parse --show-toplevel)

names=()
refs=()
seen=" "
while read -r name ref; do
  [[ -z $name || $name == \#* ]] && continue
  if [[ $seen == *" $name "* ]]; then
    echo "docs/versions lists $name twice" >&2
    exit 1
  fi
  seen+="$name "
  names+=("$name")
  refs+=("$ref")
done <"$root/docs/versions"
if ((${#names[@]} == 0)); then
  echo "docs/versions lists no versions" >&2
  exit 1
fi

# The latest is the first release listed; until there is one, the first
# version.
latest=${names[0]}
for name in "${names[@]}"; do
  if [[ $name != dev ]]; then
    latest=$name
    break
  fi
done

work=$(mktemp -d)
cleanup() {
  for tree in "$work"/tree-*; do
    [[ -d $tree ]] && git -C "$root" worktree remove --force "$tree"
  done
  rm -rf "$work"
}
trap cleanup EXIT

rm -rf "$out"
mkdir -p "$out"
out=$(cd "$out" && pwd)

for i in "${!names[@]}"; do
  name=${names[$i]}
  ref=${refs[$i]}

  src=$root/docs
  if [[ $ref != . ]]; then
    tree=$work/tree-$name
    git -C "$root" worktree add --quiet --detach "$tree" "$ref"
    src=$tree/docs
  fi
  if [[ ! -f $src/hugo.yaml ]]; then
    echo "$name ($ref) has no documentation site" >&2
    exit 1
  fi

  # What this version adds to the site's configuration: where it is
  # published, the versions to switch to, and, unless it is the latest, a
  # banner saying which it is.
  config=$work/$name.yaml
  {
    echo "baseURL: $base_url/$name/"
    echo "params:"
    echo "  versions:"
    echo "    current: $name"
    echo "    list:"
    for other in "${names[@]}"; do
      label=$other
      [[ $other == "$latest" ]] && label="$other (latest)"
      echo "      - name: \"$label\""
      echo "        url: $prefix/$other/"
    done
    if [[ $name != "$latest" ]]; then
      message="This is the documentation for $name."
      [[ $name == dev ]] && message="This is the documentation for main, which is not released yet."
      echo "  banner:"
      echo "    key: version-$name"
      echo "    message: \"$message [Read the latest]($base_url/$latest/).\""
    fi
  } >"$config"

  "$hugo" --source "$src" --config "$src/hugo.yaml,$config" \
    --destination "$out/$name" --gc --minify --quiet
done

# The site's root leads to the latest version, and its 404 page stands in
# for any path no version has.
cat >"$out/index.html" <<HTML
<!doctype html>
<meta charset="utf-8">
<title>Rungar</title>
<link rel="canonical" href="$base_url/$latest/">
<meta http-equiv="refresh" content="0; url=$prefix/$latest/">
<a href="$prefix/$latest/">Rungar documentation</a>
HTML
cp "$out/$latest/404.html" "$out/404.html"
