#!/usr/bin/env bash
# Live self-healing demo. Run `make kind-deploy` first.
#
# It deletes things that belong to a RedisCluster and shows them come back:
#   1. a Pod            -> recreated by Kubernetes' StatefulSet controller
#   2. the StatefulSet  -> recreated by this operator
#   3. Service, ConfigMap -> recreated by this operator
#   4. a hand-made edit (scale to 1) -> reverted by this operator
#
# Environment overrides: DEMO_CLUSTER, DEMO_NAME, DEMO_NAMESPACE, PAUSE (seconds between steps).
set -euo pipefail

DEMO_CLUSTER="${DEMO_CLUSTER:-redis-operator}"
DEMO_NAME="${DEMO_NAME:-rediscluster-sample}"
DEMO_NAMESPACE="${DEMO_NAMESPACE:-default}"
PAUSE="${PAUSE:-2}"
TIMEOUT="${TIMEOUT:-240s}"

k() { kubectl --context "kind-${DEMO_CLUSTER}" -n "${DEMO_NAMESPACE}" "$@"; }

step() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; sleep "${PAUSE}"; }
run() { printf '\033[2m$ kubectl %s\033[0m\n' "$*"; k "$@"; }

# wait_for_resource polls until a resource exists again (kubectl wait cannot
# wait for a name that does not exist yet).
wait_for_resource() {
  local kind="$1" name="$2" deadline=$((SECONDS + 120))
  until k get "${kind}" "${name}" >/dev/null 2>&1; do
    if ((SECONDS > deadline)); then
      echo "Timed out waiting for ${kind}/${name}" >&2
      exit 1
    fi
    sleep 1
  done
}

wait_ready() {
  k rollout status "statefulset/${DEMO_NAME}" --timeout="${TIMEOUT}"
}

show() {
  run get rediscluster "${DEMO_NAME}"
  run get statefulset,service,configmap,pods -l "app.kubernetes.io/instance=${DEMO_NAME}"
}

if ! k get rediscluster "${DEMO_NAME}" >/dev/null 2>&1; then
  echo "RedisCluster ${DEMO_NAME} not found in context kind-${DEMO_CLUSTER}. Run 'make kind-deploy' first." >&2
  exit 1
fi

step "Waiting for the cluster to be ready"
wait_ready
show

step "1/4  Delete a Pod (redis-1). The StatefulSet controller recreates it with the same name and volume."
run delete pod "${DEMO_NAME}-1" --wait=false
wait_for_resource pod "${DEMO_NAME}-1"
wait_ready
run get pods -l "app.kubernetes.io/instance=${DEMO_NAME}"

step "2/4  Delete the StatefulSet. The operator notices (Owns watch) and recreates it."
run delete statefulset "${DEMO_NAME}" --wait=true
wait_for_resource statefulset "${DEMO_NAME}"
wait_ready
run get statefulset,pods -l "app.kubernetes.io/instance=${DEMO_NAME}"

step "3/4  Delete the headless Service and the ConfigMap. The operator recreates both."
run delete service "${DEMO_NAME}" --wait=true
run delete configmap "${DEMO_NAME}-config" --wait=true
wait_for_resource service "${DEMO_NAME}"
wait_for_resource configmap "${DEMO_NAME}-config"
run get service,configmap -l "app.kubernetes.io/instance=${DEMO_NAME}"

step "4/4  Drift: scale the StatefulSet to 1 behind the operator's back. It reverts it to spec.replicas."
DESIRED="$(k get rediscluster "${DEMO_NAME}" -o jsonpath='{.spec.replicas}')"
run scale statefulset "${DEMO_NAME}" --replicas=1
until [[ "$(k get statefulset "${DEMO_NAME}" -o jsonpath='{.spec.replicas}')" == "${DESIRED}" ]]; do sleep 1; done
wait_ready
run get statefulset "${DEMO_NAME}"

step "What the operator reported"
run get rediscluster "${DEMO_NAME}" -o jsonpath='{range .status.conditions[*]}{.type}{"="}{.status}{" ("}{.reason}{")\n"}{end}'
run events --for "rediscluster/${DEMO_NAME}"

printf '\n\033[1;32mDone: everything was restored.\033[0m\n'
