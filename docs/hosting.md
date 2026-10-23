# Hosting the live demo on one small VM

This runs the portal on the public internet from a single small VM, with k3s as
the cluster and a Cloudflare Tunnel as the only way in. It is deployed at
`https://redisoperator.dpdns.org`.

```
visitor --HTTPS--> Cloudflare --tunnel--> cloudflared (Pod, dials out)
                                              |
                                              v   only this Pod may connect
                                       redis-portal (Pod) --> Kubernetes API (demo namespace only)
                                              |
                              RedisCluster --> operator --> StatefulSet, Pods
```

The VM opens **no inbound port to the internet** except SSH. The tunnel connects
outward from the VM to Cloudflare. Checked from outside: only port 22 answers; the
k3s API (6443), the kubelet (10250), 80 and 443 do not.

> **Status.** Run end to end on a Google Cloud VM (e2-medium: 2 vCPU, 4 GB,
> Ubuntu 24.04, x86_64) on 23 October 2026. Verified there: the k3s install, the
> operator, the portal and the demo RedisCluster, the named tunnel, the public
> HTTPS endpoint, the network policy, and the per-visitor rate limit.
> **Not** run on the VM: Argo CD (step 6; it was run on kind, see
> `docs/argocd.md`), building the images on the VM (step 3, option B), and an
> Oracle Cloud VM, which this guide used to assume. Cloud consoles change their
> menus often, so look for settings by name.

## What you need

- A small Linux VM with a public address, at least 2 vCPU and 4 GB of RAM for k3s,
  the operator, the portal, three Redis Pods and cloudflared. Any provider works.
  The Oracle Cloud Always Free Ampere A1 (arm64) shape is an option too: the
  images build natively for arm64 (untested here).
- SSH access with a key, and `sudo`.
- A domain whose DNS zone is on Cloudflare (a free Cloudflare account is enough).
  `redisoperator.dpdns.org` is a free `dpdns.org` subdomain delegated to Cloudflare.
- Docker on your own machine, to build the images (option A in step 3).

## 1. The VM

Create an Ubuntu 24.04 instance. Allow inbound TCP 22 only (from your address if
you can) and nothing else in the cloud firewall. Use key-only SSH.

You can check the result from your own machine after step 2: connecting to ports
6443 and 10250 on the public address should fail.

## 2. k3s

k3s ships Traefik and a load balancer controller. Neither is needed: traffic
arrives through the tunnel, not through an ingress.

```sh
curl -sfL https://get.k3s.io -o k3s-install.sh      # read it if you like
sudo INSTALL_K3S_VERSION='v1.37.0+k3s1' \
  INSTALL_K3S_EXEC='server --disable traefik --disable servicelb --write-kubeconfig-mode 644' \
  sh k3s-install.sh
sudo k3s kubectl get nodes
```

(`--write-kubeconfig-mode 644` lets any user on the VM read the admin kubeconfig.
On a shared machine leave it out and use `sudo k3s kubectl`.)

k3s enforces NetworkPolicy, which the portal's protection relies on (see "Why the
network policy matters").

## 3. Images and manifests onto the VM

The images are not published anywhere. Build them and put them into k3s directly,
so no registry and no pull secret is needed.

**Option A (tested): build on your machine, stream to the VM.**

```sh
docker build -t redis-operator:hosted .
docker build -f Dockerfile.portal -t redis-portal:hosted .
docker save redis-operator:hosted redis-portal:hosted | \
  ssh you@vm 'sudo k3s ctr images import -'

# The manifests, straight from git, so the VM needs no repository access.
git archive HEAD deploy config | ssh you@vm 'mkdir -p ~/redis-operator && tar -x -C ~/redis-operator'

# Pull the Redis images the buttons use now, so the first click does not wait.
ssh you@vm 'sudo k3s crictl pull redis:7.2; sudo k3s crictl pull redis:7.4'
```

The images must match the VM's architecture. Docker Desktop on x86 Windows builds
`linux/amd64`, which matched this VM.

**Option B (not tested): build on the VM.** Install Docker and git, give the VM a
read-only deploy key (repository Settings, Deploy keys, no write access), clone, and
run `bash hack/hosted-images.sh`, which builds and imports both images.

## 4. The Cloudflare Tunnel

Install `cloudflared` on the VM. This is the release the manifests pin, checked
against the published SHA-256 (copy the current value from the release notes):

```sh
mkdir -p ~/bin && cd "$(mktemp -d)"
curl -fsSL -o cloudflared \
  https://github.com/cloudflare/cloudflared/releases/download/2026.9.3/cloudflared-linux-amd64
echo '77e26d8d900e0b8469f416239d14b5f296525fdf79fee6f511ef55609e3fbac2  cloudflared' | sha256sum -c -
install -m 0755 cloudflared ~/bin/cloudflared
```

Authorize it once. `tunnel login` prints a URL; open it in a browser, log in, pick
the zone and click Authorize. That writes `~/.cloudflared/cert.pem`:

```sh
~/bin/cloudflared tunnel login
~/bin/cloudflared tunnel list          # lists tunnels (none yet); proves the login works
```

`tunnel list` shows tunnels, not zones. The zone is chosen in the browser step.

Create the tunnel and point the hostname at it:

```sh
~/bin/cloudflared tunnel create redis-operator-demo
~/bin/cloudflared tunnel route dns redis-operator-demo redisoperator.dpdns.org
```

`create` prints the tunnel's ID and writes its credentials to
`~/.cloudflared/<id>.json`. `route dns` adds a CNAME in the zone and refuses to
overwrite an existing record unless you pass `--overwrite-dns`.

If you use a different tunnel or hostname, change `tunnel:` and `hostname:` in
`deploy/hosting/cloudflared/configmap.yaml`. Neither is secret: both are visible in
DNS.

After the tunnel exists, `cert.pem` is no longer needed at runtime. It can create
tunnels and DNS records for the whole zone, so consider deleting it
(`rm ~/.cloudflared/cert.pem`); running `tunnel login` again brings it back. Keep
the `<id>.json` credentials file, or you cannot recreate the Secret.

## 5. Deploy

On the VM, from the directory holding the manifests (all with `sudo k3s kubectl`):

```sh
cd ~/redis-operator
k="sudo k3s kubectl"

# The operator, applied server side like the Argo CD Application does.
$k apply --server-side --force-conflicts -k deploy/hosting/operator
$k -n redis-operator-system rollout status deployment/redis-operator-controller-manager

# The portal, the demo RedisCluster and the network policy.
$k apply -k deploy/hosting/portal
$k -n demo rollout status deployment/redis-portal
$k -n demo wait --for=jsonpath='{.status.readyReplicas}'=3 rediscluster/rediscluster-sample --timeout=300s

# The tunnel. Create the namespace and the credentials Secret first.
$k apply -f deploy/hosting/cloudflared/namespace.yaml
$k -n cloudflared create secret generic cloudflared-credentials \
  --from-file=credentials.json=$HOME/.cloudflared/<id>.json
$k apply -k deploy/hosting/cloudflared
$k -n cloudflared rollout status deployment/cloudflared
```

The credentials never go into git, and only the `<id>.json` file goes into the
cluster (never `cert.pem`). Within a few seconds the `cloudflared` logs show
"Registered tunnel connection" lines, and `cloudflared tunnel info redis-operator-demo`
lists the connector.

## 6. Argo CD (optional, not run on the VM)

Instead of `kubectl apply`, Argo CD can deploy from the repository. It needs more
memory than a 4 GB VM has to spare for the full install; `core-install.yaml` is
lighter. The Applications are in `deploy/argocd/hosted/`.

```sh
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.3/manifests/install.yaml
kubectl -n argocd rollout status deployment/argocd-server --timeout=300s
```

Give Argo CD read access to the repository with a token, as in `docs/argocd.md`,
then `kubectl apply -f deploy/argocd/hosted/`. Argo CD will take over the resources
from step 5 without recreating them. The credentials Secret is not in git, so Argo
CD neither stores nor prunes it. Keep the Argo CD UI private (an SSH tunnel, not
through Cloudflare).

## 7. Check it

```sh
curl -s https://redisoperator.dpdns.org/healthz              # ok
kubectl -n demo get pods,cronjob,networkpolicy
```

Open the page, press a button, and watch the Pods and the heal time. A second
button inside 30 seconds should be refused with the cooldown message.

**If the name does not resolve on your own network** but works elsewhere (from the
VM, from a phone on mobile data), your router or ISP is probably redirecting and
filtering DNS. This was the case on the development machine: even a query sent to
an address that does not exist got an answer, so no public resolver setting could
help. Encrypted DNS is not redirected. Turn on "Secure DNS" in your browser, or test
with `curl --doh-url https://cloudflare-dns.com/dns-query https://redisoperator.dpdns.org/healthz`.

## Why the network policy matters

The portal's rate limit is per visitor. Behind a tunnel every request arrives from
the cloudflared Pod, so the portal has to read the real visitor address from the
`CF-Connecting-IP` header (`--trust-proxy-headers`, added only by
`deploy/hosting/portal`). A header is only trustworthy if the request really came
through Cloudflare. If anything else in the cluster could call the portal directly,
it could send any `CF-Connecting-IP` it liked and get a fresh allowance each time.
`deploy/hosting/portal/networkpolicy.yaml` closes that: only the cloudflared Pod may
open a connection to the portal. `deploy/hosting/hosting_test.go` checks the policy
and that the base manifests never enable the flag.

Checked live: a pod in the `demo`, `default` and `cloudflared` namespaces could not
reach the portal Service (while the same probe reached the Redis Service, so the
probe itself works), and kubelet health checks are unaffected. From outside, the
first action was accepted, the second was refused with `Retry-After`, and a request
from a different address (the VM itself) got its own allowance. A request that
carries its own `CF-Connecting-IP` header never reaches the portal at all:
Cloudflare's edge answers it with a 403 (`error code: 1000`).

## Security notes

- **Blast radius.** The portal's ServiceAccount can only change the `demo`
  namespace and only in the five ways the buttons allow (`docs/DESIGN_NOTES.md`,
  "Platform portal"). A CronJob restores 3 replicas and Redis 7.2 every 10
  minutes.
- **Secrets on the VM.** The k3s admin kubeconfig, the tunnel credentials file and
  the Secret made from it, `cert.pem` if you keep it, and (with Argo CD) the Argo CD
  admin password and repository token. None are in git. To revoke the tunnel,
  delete it with `cloudflared tunnel delete redis-operator-demo`.
- **Keep it patched.** `sudo apt-get upgrade` regularly, or enable
  `unattended-upgrades`.
- **Optional extra.** A Cloudflare rate-limiting or WAF rule in front of the
  hostname protects against floods that the portal's per-visitor limit does not
  (many visitors, or many connections that never press a button).

## Operating it

- **Change manifests:** copy them again (`git archive HEAD deploy config | ssh ...`)
  and `kubectl apply -k` the changed overlay, or push and let Argo CD do it (step 6).
- **Change code:** rebuild and stream the images again (step 3), then restart the two
  Deployments. The tag `hosted` does not change, so nothing can see a new image by
  itself:
  `kubectl -n redis-operator-system rollout restart deployment/redis-operator-controller-manager`
  and `kubectl -n demo rollout restart deployment/redis-portal`.
- **Take it down:** `kubectl delete -k deploy/hosting/cloudflared`, then delete the
  tunnel (`cloudflared tunnel delete redis-operator-demo`) and the CNAME in the
  Cloudflare DNS dashboard.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Operator or portal Pod `ErrImagePull` or `ImagePullBackOff` | The image was not imported. Run `sudo k3s ctr images ls` to look for `redis-operator:hosted` and `redis-portal:hosted`, then repeat step 3. |
| `cloudflared` Pod `CreateContainerConfigError` | The `cloudflared-credentials` Secret does not exist yet. Create it (step 5); the Pod starts by itself. |
| `cloudflared` `CrashLoopBackOff` | The credentials do not match `tunnel:` in the ConfigMap (a different tunnel, or one that was deleted). Check `kubectl -n cloudflared logs deploy/cloudflared`. |
| Cloudflare error 1033 or 502 in the browser | The tunnel has no connector or cannot reach the portal. `cloudflared tunnel info redis-operator-demo` should list a connector; check that the hostname rule in the ConfigMap points to `redis-portal.demo.svc.cluster.local:80`. |
| Page loads but every visitor gets "wait 30 seconds" | The portal is not seeing real visitor addresses. Check the Deployment args include `--trust-proxy-headers`, and that requests come through the tunnel. |
| The name does not resolve at home but does elsewhere | Your network redirects DNS. See the note in step 7. |
| `tunnel route dns` says a record already exists | Something already uses that name. Remove it in the Cloudflare DNS dashboard, or pass `--overwrite-dns` if you are sure. |
| Redis Pods `Pending` | No default storage class. k3s provides `local-path`; check `kubectl get storageclass`. |
| Application `Unknown` with `ComparisonError` (Argo CD only) | Argo CD cannot read the repository: missing or expired token. |
