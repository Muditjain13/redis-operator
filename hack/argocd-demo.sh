#!/usr/bin/env bash
# Shows what Argo CD does and does not undo, next to what the operator does.
# Run after `make argocd-up`, once both Applications are Synced and Healthy.
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
TIMEOUT="${TIMEOUT:-120}"

step() { printf '\n== %s\n' "$*"; }

# wait_for <description> <command that succeeds when the wait is over>
wait_for() {
  local what=$1 start now
  shift
  start=$(date +%s)
  until "$@" >/dev/null 2>&1; do
    now=$(date +%s)
    if [ $((now - start)) -ge "$TIMEOUT" ]; then
      echo "Timed out after ${TIMEOUT}s waiting for: $what" >&2
      exit 1
    fi
    sleep 1
  done
  echo $(($(date +%s) - start))
}

portal_replicas_is_1() { [ "$($KUBECTL -n demo get deployment redis-portal -o jsonpath='{.spec.replicas}')" = "1" ]; }
cluster_replicas_is_5() { [ "$($KUBECTL -n demo get rediscluster rediscluster-sample -o jsonpath='{.spec.replicas}')" = "5" ]; }
sts_recreated() {
  local now
  now=$($KUBECTL -n demo get statefulset rediscluster-sample -o jsonpath='{.metadata.uid}')
  [ -n "$now" ] && [ "$now" != "$1" ]
}
app_synced() { [ "$($KUBECTL -n argocd get application "$1" -o jsonpath='{.status.sync.status}')" = "Synced" ]; }

step "Both Applications"
$KUBECTL -n argocd get applications

step "1. Change a manifest by hand: scale the portal Deployment to 3 (git says 1)"
$KUBECTL -n demo scale deployment/redis-portal --replicas=3
secs=$(wait_for "Argo CD to revert the Deployment" portal_replicas_is_1)
echo "Argo CD put it back to 1 replica after ${secs}s. The change never appeared in git, so git won."

step "2. Change a field Argo CD is told to ignore: scale the RedisCluster to 5"
$KUBECTL -n demo patch rediscluster rediscluster-sample --type merge -p '{"spec":{"replicas":5}}'
sleep 15
if cluster_replicas_is_5; then
  echo "Still 5 replicas: Argo CD left it alone, and the operator scaled the StatefulSet to match."
else
  echo "Argo CD reverted the RedisCluster, which it should not." >&2
  exit 1
fi
app_synced redis-portal && echo "The redis-portal Application still reports Synced."
echo "(The reset CronJob puts this back every 10 minutes; doing it now.)"
$KUBECTL -n demo patch rediscluster rediscluster-sample --type merge -p '{"spec":{"replicas":3}}'

step "3. Delete a child the operator owns: the StatefulSet (it is not in git at all)"
uid=$($KUBECTL -n demo get statefulset rediscluster-sample -o jsonpath='{.metadata.uid}')
$KUBECTL -n demo delete statefulset rediscluster-sample --wait=true
secs=$(wait_for "the operator to recreate the StatefulSet" sts_recreated "$uid")
echo "The operator recreated it after ${secs}s. Argo CD never knew: it only watches what git defines."

step "Summary"
echo "Argo CD heals manifests that are in git. The operator heals the children it creates from a RedisCluster."
