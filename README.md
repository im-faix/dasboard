# Sentinel

Self-managed SRE dashboard for Kubernetes inventory, TLS certificate expiry,
DNS resolution, public domain-expiry checks, and Prometheus-based on-premises
host/application metrics across lower and production environments. It is a deliberately compact
starting point: monitors are held in memory, so connect a database before using
it as a production alert source.

## Run locally

```bash
cp .env.example .env
# edit .env and set a strong ADMIN_PASSWORD; see metrics deployment for the scrape secret
docker compose up --build
```

Open `http://localhost:8081`, sign in with `ADMIN_EMAIL` / `ADMIN_PASSWORD`,
and add your TLS, DNS, and domain monitors.

Docker Compose publishes Sentinel on `http://localhost:8081` and includes a
private Prometheus + Node Exporter metrics stack. See
[on-premises metrics deployment](./docs/deployment/metrics.md) to configure
the metrics secret, connect lower and production Prometheus endpoints, and
scrape additional applications.

## Email alerts and users

Set the SMTP section in `.env`: `SMTP_HOST`, `SMTP_PORT`, `SMTP_TLS_MODE`,
`SMTP_TLS_SERVER_NAME`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, and
`SMTP_TO`. `SMTP_TLS_MODE` supports `starttls`, `implicit`, or `none`.
Use `ALERT_TLS_ENABLED`, `ALERT_DNS_ENABLED`, and `ALERT_DOMAIN_ENABLED` to
control alert categories. Sentinel checks monitors every `ALERT_INTERVAL_MINUTES` (15 by default)
and emails warning/critical TLS and domain-expiry results within 30 days, plus
failed DNS checks. A monitor state is re-sent at most once every 24 hours.

Use `SENTINEL_USERS_JSON` to configure `admin` and `viewer` accounts. The
`ADMIN_EMAIL` account is the protected master admin and can add/remove users.
Admins can manage monitors; viewers can inspect dashboards and cluster metrics
only. Store this value in a secret rather than committing it. User changes are
currently held in memory and should be backed by a database before production.
The dashboard includes a responsive admin User management section and a theme
choice stored per authenticated email in the browser. Viewers receive the same
responsive dashboard in read-only mode and do not see admin controls.

## Kubernetes access

Set `KUBERNETES_API_URL` and `KUBERNETES_TOKEN` to a **read-only** service
account token for one cluster. For multiple regions or on-premise clusters,
set `KUBERNETES_CLUSTERS_JSON` to an array such as
`[{"name":"eu-prod","api":"https://eu-api:6443","token":"...","caFile":"/etc/sentinel/eu-ca.crt"}]`.
The dashboard reads node CPU and memory from Metrics Server and filesystem
usage from the node stats summary API. Grant no write verbs. For a real cluster deployment, use a separate service account, a NetworkPolicy
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
uses a read-only `ClusterRole` for nodes, node proxy stats, and Metrics Server.

Use unique credentials and store them in a secret manager before exposing the
dashboard.
