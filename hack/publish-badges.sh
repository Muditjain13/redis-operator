#!/usr/bin/env bash
# Publish badge JSON files to the orphan "badges" branch.
#
# Usage: hack/publish-badges.sh [badge-dir]   (default: badge-data)
#
# Several workflows publish their own badges, so each run fetches the current
# branch, overwrites only the files it produced, and retries if another
# workflow pushed in between.
#
# Environment:
#   GITHUB_REPOSITORY  owner/repo (set by GitHub Actions)
#   GITHUB_TOKEN       token with contents: write (set by the workflow)
#   GITHUB_SHA         commit being published (optional, used in the message)
#   BADGES_REMOTE_URL  override the remote, e.g. a local bare repo for testing
set -euo pipefail

branch="badges"
src_dir="$(cd "${1:-badge-data}" && pwd)"
remote="${BADGES_REMOTE_URL:-https://x-access-token:${GITHUB_TOKEN:-}@github.com/${GITHUB_REPOSITORY:-}.git}"

shopt -s nullglob
files=("${src_dir}"/*.json)
if [[ ${#files[@]} -eq 0 ]]; then
  echo "No badge files found in ${src_dir}" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
cd "${work}"
git init -q .
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git remote add origin "${remote}"

for attempt in 1 2 3 4 5; do
  if git fetch -q --depth=1 origin "${branch}" 2>/dev/null; then
    git checkout -q -B "${branch}" FETCH_HEAD
  else
    # First publication: start the branch with no shared history with main.
    git checkout -q --orphan "${branch}"
  fi

  cp "${files[@]}" .
  git add -- ./*.json
  if git diff --cached --quiet; then
    echo "Badges already up to date"
    exit 0
  fi

  sha="${GITHUB_SHA:-local}"
  git commit -q -m "Update badges for ${sha:0:7}"
  if git push -q origin "HEAD:${branch}"; then
    echo "Published ${#files[@]} badge file(s) to ${branch}"
    exit 0
  fi

  echo "Push rejected (attempt ${attempt}), retrying" >&2
  sleep $((attempt * 2))
done

echo "Could not publish badges after 5 attempts" >&2
exit 1
