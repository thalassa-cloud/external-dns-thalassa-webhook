# External DNS Thalassa Webhook

A webhook provider for [ExternalDNS](https://github.com/kubernetes-sigs/external-dns) that manages DNS records in [Thalassa Cloud](https://thalassa.cloud) using the [client-go DNS package](https://github.com/thalassa-cloud/client-go/tree/main/dns).

## Configuration

### Authentication

Authentication is resolved in this order:

1. **OIDC token exchange** — set `THALASSA_SERVICE_ACCOUNT_ID` and `THALASSA_ORGANISATION_ID`. The pod service account JWT is used automatically from `/var/run/secrets/kubernetes.io/serviceaccount/token` unless `THALASSA_SUBJECT_TOKEN` or `THALASSA_SUBJECT_TOKEN_FILE` is set.
2. **Personal access token** — set `THALASSA_TOKEN` or mount a token at `THALASSA_TOKEN_FILE`.
3. **OIDC client credentials** — set `THALASSA_CLIENT_ID` with `THALASSA_CLIENT_SECRET` or `THALASSA_CLIENT_SECRET_FILE`.

`THALASSA_ORGANISATION_ID` is always required.

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `THALASSA_ORGANISATION_ID` | Yes | - | Organisation identity |
| `THALASSA_SERVICE_ACCOUNT_ID` | Auth* | - | Service account for OIDC token exchange |
| `THALASSA_SUBJECT_TOKEN` | No | - | Subject JWT for OIDC token exchange |
| `THALASSA_SUBJECT_TOKEN_FILE` | No | K8s SA token path | Path to subject JWT file |
| `THALASSA_TOKEN` | Auth* | - | Personal access token |
| `THALASSA_TOKEN_FILE` | Auth* | - | Path to personal access token file |
| `THALASSA_CLIENT_ID` | Auth* | - | OIDC client ID |
| `THALASSA_CLIENT_SECRET` | Auth* | - | OIDC client secret |
| `THALASSA_CLIENT_SECRET_FILE` | Auth* | - | Path to OIDC client secret file |
| `THALASSA_OIDC_TOKEN_URL` | No | `{THALASSA_API_URL}/oidc/token` | OIDC token exchange URL |
| `THALASSA_ACCESS_TOKEN_LIFETIME` | No | - | Requested access token lifetime |
| `THALASSA_PROJECT_ID` | No | - | Project identity for project-scoped DNS |
| `THALASSA_API_URL` | No | `https://api.thalassa.cloud` | Thalassa Cloud API base URL |
| `THALASSA_INSECURE` | No | `false` | Disable TLS certificate verification |
| `THALASSA_DOMAIN_FILTER` | No | - | Comma-separated list of zones to manage |
| `THALASSA_DRY_RUN` | No | `false` | Enable dry-run mode |
| `THALASSA_HTTP_RETRY_MAX` | No | `3` | Maximum HTTP retries |
| `THALASSA_HTTP_RETRY_WAIT_MIN` | No | `1s` | Minimum wait between retries |
| `THALASSA_HTTP_RETRY_WAIT_MAX` | No | `30s` | Maximum wait between retries |
| `THALASSA_WORKERS` | No | `10` | Concurrent zone workers when listing records |

\*One authentication method from the precedence list above is required.

### Command Line Flags

All Thalassa settings are registered via flags with defaults taken from the corresponding `THALASSA_*` environment variables. Flags override env when both are set.

```
--log-level                       Log level (debug, info, warn, error) [default: info]
--log-format                      Log format (text, json) [default: text]
--host                            Webhook server host [default: 0.0.0.0]
--port                            Webhook server port [default: 8888]
--dry-run                         Enable dry-run mode
--retry-max                       Maximum HTTP retries [default: 3]
--retry-wait-min                  Minimum wait between retries [default: 1s]
--retry-wait-max                  Maximum wait between retries [default: 30s]
--workers                         Concurrent zone workers [default: 10]
--domain-filter                   Comma-separated list of DNS zones to manage
--thalassa-url                    Thalassa Cloud API base URL
--organisation                    Organisation identity
--thalassa-project                Project identity
--thalassa-token                  Personal access token
--thalassa-token-file             Path to personal access token file
--thalassa-service-account-id     Service account for OIDC token exchange
--thalassa-subject-token          Subject JWT for OIDC token exchange
--thalassa-subject-token-file     Path to subject JWT file
--thalassa-oidc-token-url         OIDC token exchange URL
--thalassa-access-token-lifetime  Requested access token lifetime
--thalassa-client-id              OIDC client ID
--thalassa-client-secret          OIDC client secret
--thalassa-client-secret-file     Path to OIDC client secret file
--thalassa-insecure               Disable TLS certificate verification
```

## Deployment

### Using Helm (OIDC workload federation)

Recommended for Kubernetes: no long-lived personal token in a Secret.

```yaml
provider:
  name: webhook
  webhook:
    image:
      repository: ghcr.io/thalassa-cloud/external-dns-thalassa-webhook
      tag: latest
    serviceAccount:
      create: true
      name: external-dns-thalassa
    env:
      - name: THALASSA_ORGANISATION_ID
        value: "my-org"
      - name: THALASSA_SERVICE_ACCOUNT_ID
        value: "sa-external-dns"
      - name: THALASSA_DOMAIN_FILTER
        value: "example.com"
    args:
      - --port=8080
      - --log-level=info
    securityContext:
      runAsUser: 65532
      runAsGroup: 65532
      runAsNonRoot: true
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop:
          - ALL
    livenessProbe:
      httpGet:
        path: /healthz
        port: http-webhook
      initialDelaySeconds: 10
      periodSeconds: 10
    readinessProbe:
      httpGet:
        path: /healthz
        port: http-webhook
      initialDelaySeconds: 5
      periodSeconds: 5
```

Install the chart:

```bash
helm repo add external-dns https://kubernetes-sigs.github.io/external-dns/
helm upgrade --install external-dns external-dns/external-dns -f values.yaml
```

> **Note:** The `--port=8080` flag is required because the ExternalDNS Helm chart expects the webhook to listen on port 8080 (`http-webhook`), while the binary defaults to 8888.

### Using Helm (personal access token)

```yaml
provider:
  name: webhook
  webhook:
    image:
      repository: ghcr.io/thalassa-cloud/external-dns-thalassa-webhook
      tag: latest
    env:
      - name: THALASSA_TOKEN
        valueFrom:
          secretKeyRef:
            name: thalassa-credentials
            key: token
      - name: THALASSA_ORGANISATION_ID
        value: "my-org"
      - name: THALASSA_DOMAIN_FILTER
        value: "example.com"
    args:
      - --port=8080
      - --log-level=info
```

### Kubernetes Deployment (Sidecar, OIDC workload federation)

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: external-dns-thalassa
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: external-dns
spec:
  template:
    spec:
      serviceAccountName: external-dns-thalassa
      containers:
        - name: external-dns
          image: registry.k8s.io/external-dns/external-dns:v0.21.0
          args:
            - --source=ingress
            - --source=service
            - --provider=webhook
            - --webhook-provider-url=http://localhost:8888
            - --policy=sync
            - --registry=txt
            - --txt-owner-id=my-cluster
            - --interval=1m

        - name: thalassa-webhook
          image: ghcr.io/thalassa-cloud/external-dns-thalassa-webhook:latest
          securityContext:
            runAsUser: 65532
            runAsGroup: 65532
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          args:
            - --log-level=info
            - --organisation=my-org
            - --thalassa-service-account-id=sa-external-dns
          env:
            - name: THALASSA_DOMAIN_FILTER
              value: "example.com"
          ports:
            - containerPort: 8888
              name: http
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 5
```

### Secret (personal access token)

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: thalassa-credentials
type: Opaque
stringData:
  token: "your-thalassa-personal-access-token"
```

## Building

### Prerequisites

- Go 1.25+
- Make
- [GoReleaser](https://goreleaser.com/) (required for Docker image builds)

### Commands

```bash
# Run tests
make test

# Build binary locally
make build

# Build Docker image (uses GoReleaser snapshot artifacts)
make docker-build

# Run locally with personal token
THALASSA_TOKEN=your-token \
THALASSA_ORGANISATION_ID=your-org \
make run
```

## Releases

Releases are published with [GoReleaser](https://goreleaser.com/) when a version tag matching `v*` is pushed. Container images are published to `ghcr.io/thalassa-cloud/external-dns-thalassa-webhook`.

```bash
git tag v0.1.0
git push origin v0.1.0
```

## Metrics

The webhook exposes Prometheus metrics at `/metrics`:

| Metric | Description |
|--------|-------------|
| `thalassa_api_requests_total` | Total number of Thalassa Cloud API requests |
| `thalassa_api_errors_total` | Total number of failed API requests |
| `thalassa_api_rate_limits_total` | Total number of rate limit hits |

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
