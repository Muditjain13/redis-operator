#!/usr/bin/env bash
# Points one image of a kustomization at a registry image built from a commit.
# The images workflow runs this after it has pushed the images, so that Argo CD
# sees a new tag in git and rolls the deployment out.
#
#   hack/set-image-tag.sh <kustomization.yaml> <image-name> <registry-image> <commit-sha>
#
# It rewrites only the entry named <image-name> under `images:`, whatever form
# that entry had (a bare newTag, or an older newName and newTag), into
#
#   - name: <image-name>
#     newName: <registry-image>
#     newTag: "<commit-sha>"
#
# and leaves every other line alone. The tag is quoted because an unquoted
# all-digit or 1e5-looking SHA would be read as a number.
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: $0 <kustomization.yaml> <image-name> <registry-image> <commit-sha>" >&2
  exit 2
fi
file=$1
name=$2
repo=$3
sha=$4

[ -f "$file" ] || { echo "no such file: $file" >&2; exit 2; }
[[ $name =~ ^[a-z0-9-]+$ ]] || { echo "bad image name: $name" >&2; exit 2; }
[[ $repo =~ ^ghcr\.io/muditjain13/[a-z0-9-]+$ ]] || {
  echo "registry image must look like ghcr.io/muditjain13/<name> in lower case: $repo" >&2
  exit 2
}
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "the tag must be a full 40 character lower case commit SHA: $sha" >&2; exit 2; }

if ! tr -d '\r' < "$file" | grep -q -x -F -- "  - name: ${name}"; then
  echo "image ${name} is not listed under images: in ${file}" >&2
  exit 1
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
awk -v name="$name" -v repo="$repo" -v sha="$sha" '
  { sub(/\r$/, "") }
  $0 == "  - name: " name {
    print
    print "    newName: " repo
    print "    newTag: \"" sha "\""
    skipping = 1
    next
  }
  skipping && /^    (newName|newTag|digest):/ { next }
  { skipping = 0; print }
' "$file" > "$tmp"
cat "$tmp" > "$file"
