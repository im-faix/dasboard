# Sentinel

Self-managed SRE dashboard for Kubernetes inventory, TLS certificate expiry,
DNS resolution, and public domain-expiry checks. It is a deliberately compact
starting point: monitors are held in memory, so connect a database before using
it as a production alert source.

## Run locally

```bash
cp .env.example .env
# edit .env and set a strong ADMIN_PASSWORD
docker compose up --build
```

Open `http://localhost:8080`, sign in with `ADMIN_EMAIL` / `ADMIN_PASSWORD`,
and add your TLS, DNS, and domain monitors.

## Email alerts and users

Set `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`,
and `SMTP_TO` in `.env`. Port `587` uses STARTTLS; port `465` uses implicit
TLS. Sentinel checks monitors every `ALERT_INTERVAL_MINUTES` (15 by default)
and emails warning/critical TLS and domain-expiry results within 30 days, plus
failed DNS checks. A monitor state is re-sent at most once every 24 hours.

Use `SENTINEL_USERS_JSON` to configure `admin`, `operator`, and `viewer`
accounts. `viewer` can inspect only; `operator` can add/remove monitors; and
`admin` has full access. Store this value in a secret rather than committing it.

## Kubernetes access

Set `KUBERNETES_API_URL` and `KUBERNETES_TOKEN` to a **read-only** service
account token. The dashboard requests only `GET /api/v1/nodes`; grant no write
verbs. For a real cluster deployment, use a separate service account, a NetworkPolicy
that permits only the API server, TLS at the ingress, `COOKIE_SECURE=true`, and a
secret manager or Kubernetes Secret rather than committing `.env`.

For in-cluster deployment, apply `deployments/kubernetes/sentinel-readonly.yaml`
for the read-only node RBAC, then create the application secret and apply
`deployments/kubernetes/sentinel-deployment.yaml`:

```bash
kubectl apply -f deployments/kubernetes/sentinel-readonly.yaml
kubectl -n sentinel create secret generic sentinel-secrets \
	--from-literal=ADMIN_EMAIL=admin@example.com \
	--from-literal=ADMIN_PASSWORD='replace-with-a-long-password' \
	--from-literal=SENTINEL_USERS_JSON='[{"email":"admin@example.com","password":"replace-admin-password","role":"admin"},{"email":"viewer@example.com","password":"replace-viewer-password","role":"viewer"}]' \
	--from-literal=SMTP_HOST=smtp.example.com \
	--from-literal=SMTP_USERNAME= \
	--from-literal=SMTP_PASSWORD= \
	--from-literal=SMTP_FROM=sentinel@example.com \
	--from-literal=SMTP_TO=ops@example.com
kubectl apply -f deployments/kubernetes/sentinel-deployment.yaml
```

For an external cluster, set `KUBERNETES_API_URL`, `KUBERNETES_TOKEN`, and
`KUBERNETES_CA_FILE` in the same application secret. The Kubernetes manifest
uses only a read-only `ClusterRole` for listing nodes.

The default credentials are intentionally unsafe and are only a local-development
fallback. Change them before exposing the dashboard.
