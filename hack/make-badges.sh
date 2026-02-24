#!/usr/bin/env bash
# Turn measurements taken in CI into shields.io endpoint badge files.
#
# Usage:
#   hack/make-badges.sh e2e     [e2e-metrics.json]     operator-reaction, full-recovery (self-heal), upgrade and k8s badges
#   hack/make-badges.sh specs   [spec-report.json]     envtest spec count badge
#   hack/make-badges.sh goreport                       Go Report Card grade badge
#
# Every value is read from a measurement file or tool output produced in the
# same run. The only literals here are labels and colour thresholds.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
badge="${here}/badge.sh"
out_dir="${BADGE_DIR:-badge-data}"

require_number() {
  if ! [[ "$2" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    echo "measurement '$1' is missing or not a number: '$2'" >&2
    exit 1
  fi
}

e2e() {
  local file="${1:-e2e-metrics/e2e-metrics.json}"
  local reaction heal upgraded total k8s
  reaction="$(jq -r '.operatorReactionSeconds // empty' "${file}")"
  heal="$(jq -r '.selfHealSeconds // empty' "${file}")"
  upgraded="$(jq -r '.podsUpgraded // empty' "${file}")"
  total="$(jq -r '.podsTotal // empty' "${file}")"
  k8s="$(jq -r '.kubernetesVersion // empty' "${file}")"
  require_number operatorReactionSeconds "${reaction}"
  require_number selfHealSeconds "${heal}"
  require_number podsUpgraded "${upgraded}"
  require_number podsTotal "${total}"

  # Time for the operator to recreate a deleted StatefulSet object.
  local reaction_color=brightgreen
  if awk -v s="${reaction}" 'BEGIN { exit !(s > 15) }'; then
    reaction_color=orange
  elif awk -v s="${reaction}" 'BEGIN { exit !(s > 5) }'; then
    reaction_color=yellow
  fi
  "${badge}" operator-reaction "operator reaction" \
    "$(awk -v s="${reaction}" 'BEGIN { printf "%.1f s", s }')" "${reaction_color}" "${out_dir}"

  # Time until every replacement Pod is Ready again. The file keeps its
  # original name, self-heal.json, so the badge URL stays stable.
  local heal_color=brightgreen
  if awk -v s="${heal}" 'BEGIN { exit !(s > 180) }'; then
    heal_color=orange
  elif awk -v s="${heal}" 'BEGIN { exit !(s > 60) }'; then
    heal_color=yellow
  fi
  "${badge}" self-heal "full recovery (${total} pods)" \
    "$(awk -v s="${heal}" 'BEGIN { printf "%.1f s", s }')" "${heal_color}" "${out_dir}"

  local upgrade_color=red
  if [[ "${upgraded}" -eq "${total}" && "${total}" -gt 0 ]]; then
    upgrade_color=brightgreen
  fi
  "${badge}" upgrade "rolling upgrade" "${upgraded}/${total} pods" "${upgrade_color}" "${out_dir}"

  "${badge}" k8s "kubernetes" "${k8s}" blue "${out_dir}"
}

specs() {
  local file="${1:-spec-report.json}"
  local passed failed total
  passed="$(jq -r '.passed // empty' "${file}")"
  failed="$(jq -r '.failed // empty' "${file}")"
  total="$(jq -r '.total // empty' "${file}")"
  require_number passed "${passed}"
  require_number failed "${failed}"
  require_number total "${total}"

  if [[ "${failed}" -eq 0 && "${passed}" -gt 0 ]]; then
    "${badge}" specs "envtest specs" "${passed} passing" brightgreen "${out_dir}"
  else
    "${badge}" specs "envtest specs" "${passed}/${total} passing" red "${out_dir}"
  fi
}

goreport() {
  local output grade
  output="$(goreportcard-cli -v 2>&1)"
  echo "${output}"

  # When a check cannot run (a missing linter binary) the tool still prints a
  # grade, an F, instead of failing. Never publish that as a real result.
  if grep -q 'ERROR' <<<"${output}"; then
    echo "goreportcard-cli reported errors; refusing to publish a grade" >&2
    exit 1
  fi

  # Output looks like: "Grade .......... A+ 100.0%"
  grade="$(sed -nE 's/^Grade \.+ +([A-F][+-]?).*/\1/p' <<<"${output}" | head -n1)"
  if [[ -z "${grade}" ]]; then
    echo "could not read a grade from goreportcard-cli output" >&2
    exit 1
  fi

  local color
  case "${grade}" in
    A*) color=brightgreen ;;
    B*) color=yellow ;;
    C*) color=orange ;;
    *) color=red ;;
  esac
  "${badge}" goreport "go report" "${grade}" "${color}" "${out_dir}"
}

case "${1:-}" in
  e2e) e2e "${2:-}" ;;
  specs) specs "${2:-}" ;;
  goreport) goreport ;;
  *)
    echo "usage: $0 {e2e|specs|goreport} [measurement-file]" >&2
    exit 2
    ;;
esac
