# Hosting the live demo on one free VM

This runs the portal on the public internet from a single Oracle Cloud
Always Free VM, with k3s as the cluster, Argo CD deploying from this repository,
and a Cloudflare Tunnel as the only way in.

```
visitor --HTTPS--> Cloudflare --tunnel--> cloudflared (Pod, dials out)
                                              |
                                              v   only this Pod may connect
                                       redis-portal (Pod) --> Kubernetes API (demo namespace only)
                                              |
                              RedisCluster --> operator --> StatefulSet, Pods
```

The VM opens **no inbound port to the internet** except SSH, which you can limit
to your own address. The tunnel connects outward from the VM to Cloudflare.

> **Status.** The manifests are rendered and unit tested, the devcontainer is
> built and tested, and the Argo CD flow was run on kind (`docs/argocd.md`). The
> steps below were written from the k3s, Argo CD, Cloudflare and Oracle
> documentation. They have **not** been run on an actual Oracle VM, and the
> Cloudflare and Oracle consoles change their menus often, so look for the
> setting by name. Treat the first run as the test.

## What you need

- An Oracle Cloud account with Always Free resources. An Ampere A1 (arm64) VM
  with 2 OCPU and 12 GB is comfortable; the free tier allows up to 4 OCPU and
  24 GB. The Dockerfiles build natively for arm64. Free A1 capacity is
  sometimes "out of capacity" in a region, in which case retry later or pick
  another availability domain. Check Oracle's current terms about idle
  Always Free instances being reclaimed.
- A domain whose DNS is on Cloudflare (a free Cloudflare account is enough).
- A GitHub fine-grained token with **Contents: Read-only** on this repository, for
  Argo CD (`docs/argocd.md` has the steps). The repository can stay private.
- A read-only deploy key for the VM to clone with (below).

## 1. The VM

Create an Ubuntu 24.04 instance. In the virtual cloud network's security list,
allow inbound TCP 22 only (from your address if you can) and nothing else. Add
your SSH public key; disable password login.

```sh
sudo apt-get update && sudo apt-get -y upgrade
sudo apt-get install -y docker.io git
sudo usermod -aG docker "$USER"      # log out and in again
```

Docker is only used to build the two images.

## 2. k3s

k3s ships Traefik and a load balancer controller. Neither is needed: traffic
arrives through the tunnel, not through an ingress.

```sh
curl -sfL https://get.k3s.io | \
  INSTALL_K3S_VERSION='v1.37.0+k3s1' \
  INSTALL_K3S_EXEC='server --disable traefik --disable servicelb --write-kubeconfig-mode 644' \
  sh -
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl get nodes
```

(`--write-kubeconfig-mode 644` lets your user read the admin kubeconfig. On a
shared machine use `sudo` instead. Piping a script into `sh` runs whatever the
URL returns; download it and read it first if you prefer.)

k3s enforces NetworkPolicy, which the portal's protection relies on (see "Why
the network policy matters").

## 3. Clone and build the images

The VM needs read access to the private repository. Use a deploy key:

```sh
ssh-keygen -t ed25519 -f ~/.ssh/redis-operator-deploy -N ''
cat ~/.ssh/redis-operator-deploy.pub
```

Add that public key in the repository's Settings, Deploy keys, **without** write
access. Then:

```sh
GIT_SSH_COMMAND='ssh -i ~/.ssh/redis-operator-deploy' \
  git clone git@github.com:Muditjain13/redis-operator.git
cd redis-operator
bash hack/hosted-images.sh
```

`hosted-images.sh` builds `redis-operator:hosted` and `redis-portal:hosted` and
imports them into k3s, so no registry is needed. Docker Hub is only used for
base images; if a pull fails, retry.

## 4. Argo CD

```sh
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.3/manifests/install.yaml
kubectl -n argocd rollout status deployment/argocd-server --timeout=300s
```

Give Argo CD read access to the repository with the token, as in
`docs/argocd.md` (the `repo-redis-operator` Secret). Then create the three
Applications:

```sh
kubectl apply -f deploy/argocd/hosted/
kubectl -n argocd get applications -w
```

They deploy the operator, the portal with the demo RedisCluster, and
cloudflared. Keep the Argo CD UI private: reach it with
`kubectl -n argocd port-forward svc/argocd-server 8443:443` over an SSH tunnel,
not through Cloudflare. The first admin password is in the
`argocd-initial-admin-secret` Secret; change it and delete that Secret.

`cloudflared` will wait with `CreateContainerConfigError` until step 5 creates
its token. That is expected.

## 5. Cloudflare Tunnel

In the Cloudflare dashboard, under Zero Trust, Networks, Tunnels:

1. Create a tunnel of type **Cloudflared** and name it, for example `redis-demo`.
2. Copy the **tunnel token**. Treat it like a password.
3. Add a **public hostname**, such as `demo.example.com`, with service type HTTP
   and URL `redis-portal.demo.svc.cluster.local:80`.

Create the Secret on the VM (the token is never in git):

```sh
kubectl -n cloudflared create secret generic cloudflared-token \
  --from-literal=token='<tunnel token>'
```

Within a minute the `cloudflared` Pod should be Running, and the tunnel shows as
healthy in the dashboard.

## 6. Check it

```sh
curl -s https://demo.example.com/healthz              # ok
kubectl -n demo get pods,cronjob,networkpolicy
kubectl -n argocd get applications                    # all Synced, Healthy
```

Open the page, press a button, and watch the Pods and the heal time. Try a
second button inside 30 seconds: it should be refused with the cooldown message.

## Why the network policy matters

The portal's rate limit is per visitor. Behind a tunnel every request arrives
from the cloudflared Pod, so the portal has to read the real visitor address from
the `CF-Connecting-IP` header (`--trust-proxy-headers`, added only by
`deploy/hosting/portal`). Cloudflare sets that header itself, but a header is
only trustworthy if the request really came through Cloudflare. If anything else
in the cluster could call the portal directly, it could send any
`CF-Connecting-IP` it liked and get a fresh allowance each time.
`deploy/hosting/portal/networkpolicy.yaml` closes that: only the cloudflared Pod
may open a connection to the portal. `deploy/hosting/hosting_test.go` checks the
policy and that the base manifests never enable the flag.

## Security notes

- **Blast radius.** The portal's ServiceAccount can only change the `demo`
  namespace and only in the five ways the buttons allow (`docs/DESIGN_NOTES.md`,
  "Platform portal"). A CronJob restores 3 replicas and Redis 7.2 every 10
  minutes.
- **Secrets on the VM.** The k3s admin kubeconfig, the Argo CD admin password, the
  tunnel token and the Argo CD repository token. None are in git. Rotate the
  tokens if the VM is ever shared or lost.
- **Keep it patched.** `sudo apt-get upgrade` regularly, or enable
  `unattended-upgrades`.
- **Optional extra.** A Cloudflare rate-limiting or WAF rule in front of the
  hostname protects against floods that the portal's per-visitor limit does not
  (many visitors, or many connections that never press a button).

## Operating it

- **Change manifests:** push to `main`. Argo CD syncs within a few minutes.
- **Change code:** push, then on the VM `git pull && bash hack/hosted-images.sh`
  and restart the two Deployments (the script prints the commands). The tag
  `hosted` does not change, so Argo CD cannot see a new image by itself.
- **See what Argo CD did:** `kubectl -n argocd get applications` and the UI.
- **Take it down:** delete the three Applications (the finalizer removes what
  they created), then delete the tunnel in the Cloudflare dashboard.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Operator or portal Pod `ErrImagePull` or `ImagePullBackOff` | The image was not imported. Run `sudo k3s ctr images ls` to look for `redis-operator:hosted` and `redis-portal:hosted`, then `hack/hosted-images.sh` again. |
| Application `Unknown` with `ComparisonError` | Argo CD cannot read the repository: missing or expired token. |
| `cloudflared` `CrashLoopBackOff` | Wrong or revoked tunnel token. Recreate the Secret and delete the Pod. |
| Page loads but every visitor gets "wait 30 seconds" | The portal is not seeing real visitor addresses. Check the Deployment args include `--trust-proxy-headers`, and that requests come through the tunnel. |
| Page does not load, tunnel healthy | The public hostname service URL is wrong. It must be `redis-portal.demo.svc.cluster.local:80`. |
| Redis Pods `Pending` | No default storage class. k3s provides `local-path`; check `kubectl get storageclass`. |
| Portal Pod never Ready after applying the network policy | Report it. NetworkPolicy should not affect kubelet probes, but this was not run on k3s here. |
