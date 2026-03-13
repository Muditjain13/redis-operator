# Running the demo with Argo CD

Argo CD deploys the operator and the portal from this repository. Two
Applications in `deploy/argocd/` do it:

| Application | Path in this repo | Deploys into |
|---|---|---|
| `redis-operator` | `deploy/operator` (CRD, RBAC, manager from `config/default`) | `redis-operator-system` |
| `redis-portal` | `deploy/portal` (portal, reset CronJob, `demo` namespace and the RedisCluster) | `demo` |

Both sync automatically, prune what was removed from git, and revert manual
changes (`automated: {prune: true, selfHeal: true}`).

One exception is deliberate. The `redis-portal` Application ignores
`/spec/replicas` and `/spec/version` of the `rediscluster-sample` RedisCluster.
The portal's buttons and the reset CronJob change those two fields, so if Argo CD
reverted them the buttons would appear to do nothing. Every other field of that
resource, and every other resource, is still held to git.

## Try it on kind

Needs Docker, kind and kubectl. Keep only one kind cluster running; do not run it
together with `make test-e2e`.

```sh
export ARGOCD_REPO_TOKEN=...   # only while the repository is private, see below
make argocd-up                  # cluster, images, Argo CD v3.5.3, both Applications
kubectl -n argocd get applications -w
make argocd-demo                # Argo CD reverting a manual change
```

`argocd-up` installs Argo CD's `core-install.yaml` (application controller,
repo server, Redis; no UI or login), which fits on a laptop. For the UI use
`make argocd-up ARGOCD_INSTALL=install.yaml`, then
`kubectl -n argocd port-forward svc/argocd-server 8443:443`. The initial admin
password is in the `argocd-initial-admin-secret` Secret.

The images are built locally and loaded into kind (`redis-operator:dev`,
`redis-portal:dev`); nothing is pulled from a registry for them.

## Giving Argo CD read access to a private repository

While the repository is private, Argo CD needs a credential to clone it. Use a
fine-grained personal access token that can only read this one repository:

1. GitHub, Settings, Developer settings, Personal access tokens, **Fine-grained
   tokens**, Generate new token.
2. Resource owner: your account. Repository access: **Only select repositories**,
   then `redis-operator`.
3. Repository permissions: **Contents: Read-only**. Leave everything else at "No
   access" (Metadata read-only is added automatically).
4. Set a short expiry (30 to 90 days) and copy the token.

Give it to the cluster either through the Makefile:

```sh
export ARGOCD_REPO_TOKEN=github_pat_...
make argocd-up
```

or by creating the Secret yourself:

```sh
kubectl -n argocd create secret generic repo-redis-operator \
  --from-literal=type=git \
  --from-literal=url=https://github.com/Muditjain13/redis-operator.git \
  --from-literal=username=git \
  --from-literal=password="$ARGOCD_REPO_TOKEN"
kubectl -n argocd label secret repo-redis-operator argocd.argoproj.io/secret-type=repository
```

The `username` can be any non-empty string; GitHub only checks the token. The
token lives in that Secret and nowhere in git. Rotate it before it expires by
re-running the command with a new token. If the repository is later made public,
the Secret can be deleted and nothing else changes.

If an Application shows `ComparisonError` with "authentication required" or
"repository not found", the token is missing, expired, or does not have access to
this repository.

## What the demo shows

`make argocd-demo` runs three changes against the live cluster:

1. **Scale the portal Deployment to 3 by hand.** Git says 1. Argo CD reverts it
   within seconds, and the script prints how long it took.
2. **Scale the RedisCluster to 5 by hand.** Argo CD is told to ignore that field,
   so it stays at 5 and the operator scales to match. The Application still
   reports `Synced`.
3. **Delete the StatefulSet.** It is not in git, so Argo CD does not care. The
   operator recreates it.

Changes 1 and 3 are the two kinds of self-healing in this project. See
`docs/DESIGN_NOTES.md`, "GitOps with Argo CD".
