# On-premises metrics and environments

Sentinel reads Prometheus metrics from separately named environments. The
Compose stack includes a private Prometheus instance, a read-only Node Exporter
for host CPU, memory, filesystem and network metrics, and Sentinel's
authenticated `/metrics` endpoint for Go runtime and HTTP request metrics.
Prometheus is not published on a host port; only Sentinel's web UI is published,
bound to `127.0.0.1:8081`.

## Start the local metrics stack

```bash
cp .env.example .env
mkdir -p .secrets
openssl rand -hex 32 > .secrets/metrics_token
chmod 0700 .secrets
chmod 0444 .secrets/metrics_token
docker network create --driver bridge --internal sentinel-metrics
```

Set a unique `ADMIN_PASSWORD` in `.env`, then start the services:

```bash
docker compose up --build -d
```

Open `http://localhost:8081`. The default `.env.example` adds the local
Prometheus instance as the `lower` environment. Its database and scrape API are
available only on the private Compose network. Prometheus retains 15 days of
data in the `prometheus-data` volume. Node Exporter is read-only and does not
use host networking or privileged mode.

## Connect lower and production Prometheus servers

Create the private `sentinel-metrics` Docker network once on each host; Compose
services and application stacks can then join it without publishing metrics
ports on the host. Run the same metrics stack on each on-premises environment.
For the dashboard instance, set `PROMETHEUS_ENVIRONMENTS_JSON` to the list of
endpoints it may query. The `name` is shown in the environment selector; `url`
is the Prometheus base URL. Use a private TLS endpoint and a dedicated
read-only bearer token for remote environments:

```dotenv
PROMETHEUS_ENVIRONMENTS_JSON='[{"name":"lower","url":"https://prometheus-lower.example.internal","token":"LOWER_READ_ONLY_TOKEN"},{"name":"production","url":"https://prometheus-prod.example.internal","token":"PROD_READ_ONLY_TOKEN"}]'
COOKIE_SECURE=true
```

Use bearer tokens of at least 32 characters. Store production tokens in the
deployment's secret manager and inject the JSON at runtime; do not commit
tokens to `.env`, Compose files, or source control.
The Sentinel API does not return configured tokens. For a private CA, add
`"caFile":"/run/secrets/prometheus_ca"` to the endpoint and add a read-only
bind mount under the `sentinel` service in `docker-compose.yml`:

```yaml
services:
  sentinel:
    volumes:
      - /secure/path/prometheus-ca.pem:/run/secrets/prometheus_ca:ro
```

Never disable certificate verification. Do not publish Prometheus directly to
the internet; route remote access through a private network or an authenticated
TLS reverse proxy and restrict it to the dashboard.

For a dashboard outside the environment-local Compose network, replace the
local `http://prometheus:9090` entry with its private HTTPS reverse-proxy URL.
The built-in local URL works only for services attached to the same Compose
network.

## Scrape other applications

Sentinel publishes these application metrics: total HTTP requests and request
duration by method/status, Go goroutines, heap allocation, and process uptime.
Scrapes require a bearer token of at least 32 characters from the ignored
`.secrets/metrics_token` file. For another Compose app, join its service to the
shared network and add its service name and container port as a scrape target:

```yaml
services:
  my-app:
    networks:
      - default
      - sentinel-metrics

networks:
  sentinel-metrics:
    external: true
    name: sentinel-metrics
```

Then add a job for `my-app:<container-port>` to
`deployments/prometheus/prometheus.yml`; for example:

```yaml
  - job_name: my-app
    static_configs:
      - targets: ["my-app:8080"]
```

The dashboard reports target health for every additional job and displays CPU
and resident memory when the app exports the standard `process_*` metrics.
Configure app-specific Prometheus instrumentation and authentication as
appropriate. Keep the scrape network private.

The dashboard displays host CPU, memory, root filesystem use, application
request/error rates, goroutines and heap use. Missing or failed scrape data is
shown as degraded/unavailable; it is not treated as healthy.

## Production exposure

The Compose web port binds to loopback by default. Put Sentinel behind an
HTTPS reverse proxy with authentication/network restrictions, set
`COOKIE_SECURE=true`, and expose only the proxy to users. Keep Prometheus,
Node Exporter, and the metrics secret inaccessible from public networks.
Production deployments should pin container images by digest and apply host
firewall rules, resource limits, and a backup policy for the Prometheus volume.
