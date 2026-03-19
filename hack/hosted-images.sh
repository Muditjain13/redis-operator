#!/usr/bin/env bash
# Builds the operator and portal images on the k3s VM and imports them into
# k3s's containerd, so the hosted deployment needs no image registry.
# Run from the repository root on the VM, as a user that can run docker and sudo.
set -euo pipefail

TAG="${TAG:-hosted}"

docker build -t "redis-operator:${TAG}" .
docker build -f Dockerfile.portal -t "redis-portal:${TAG}" .

for image in "redis-operator:${TAG}" "redis-portal:${TAG}"; do
  echo "Importing ${image} into k3s"
  docker save "${image}" | sudo k3s ctr images import -
done

sudo k3s ctr images ls | grep -E "redis-(operator|portal):${TAG}"

cat <<EOF

The images are in k3s. A tag does not change when you rebuild, so after a
rebuild restart what uses it:
  kubectl -n redis-operator-system rollout restart deployment/redis-operator-controller-manager
  kubectl -n demo rollout restart deployment/redis-portal
EOF
