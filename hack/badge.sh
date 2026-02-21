#!/usr/bin/env bash
# Write a shields.io endpoint badge file: https://shields.io/badges/endpoint-badge
#
# Usage: hack/badge.sh <name> <label> <message> <color> [output-dir]
#
# Creates <output-dir>/<name>.json (default output dir: badge-data). The message
# must come from a measurement made in the same CI run, never a literal.
set -euo pipefail

if [[ $# -lt 4 ]]; then
  echo "usage: $0 <name> <label> <message> <color> [output-dir]" >&2
  exit 2
fi

name="$1" label="$2" message="$3" color="$4" out_dir="${5:-badge-data}"

if [[ -z "${message}" ]]; then
  echo "refusing to write badge '${name}' with an empty message (measurement missing?)" >&2
  exit 1
fi

mkdir -p "${out_dir}"
jq -n --arg label "${label}" --arg message "${message}" --arg color "${color}" \
  '{schemaVersion: 1, label: $label, message: $message, color: $color}' \
  >"${out_dir}/${name}.json"
echo "Wrote ${out_dir}/${name}.json: ${label} | ${message} | ${color}"
