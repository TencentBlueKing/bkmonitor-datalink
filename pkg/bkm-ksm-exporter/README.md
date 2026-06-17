# bkm-ksm-exporter

A small Prometheus exporter that emits `kube_hpa_*` metrics for
HorizontalPodAutoscaler objects, read from the `autoscaling/v2` API.

## Why

kube-state-metrics v1.9.7 reads HPAs via the `autoscaling/v2beta1` API, which
Kubernetes **removed in v1.25**. On clusters >= 1.25 it can no longer list HPAs
and produces **no** `kube_hpa_*` metrics. This exporter reads HPAs from
`autoscaling/v2` (GA since Kubernetes 1.23) and emits the **same metric names,
labels and semantics as kube-state-metrics v1.9.7**, so existing dashboards,
alerting rules and metric-keep lists keep working unchanged on newer clusters.

It is intentionally minimal and is meant as a high-version compatibility
supplement next to an existing kube-state-metrics deployment — not a replacement.
The collector registry (`exporter.Source`) is extensible, so other resource
families whose old API versions have been removed can be added the same way.

## Metrics

All gauges, with default labels `namespace` and `hpa`:

| Metric | Source field |
|--------|--------------|
| `kube_hpa_metadata_generation` | `.metadata.generation` |
| `kube_hpa_spec_max_replicas` | `.spec.maxReplicas` |
| `kube_hpa_spec_min_replicas` | `.spec.minReplicas` |
| `kube_hpa_status_current_replicas` | `.status.currentReplicas` |
| `kube_hpa_status_desired_replicas` | `.status.desiredReplicas` |
| `kube_hpa_labels` | object labels, as `label_<key>` (value `1`) |
| `kube_hpa_status_condition` | `.status.conditions[]` (labels `condition`, `status`) |

> `kube_hpa_spec_target_metric` (the 8th kube-state-metrics HPA family) is not
> emitted yet — it requires mapping the `autoscaling/v2` `MetricSpec` shape and is
> tracked as a follow-up.

## Run

```
bkm-ksm-exporter --listen=:8080
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `:8080` | metrics HTTP listen address |
| `--kubeconfig` | `""` | kubeconfig for out-of-cluster runs; empty uses in-cluster config |
| `--resync` | `5m` | informer resync period |
| `--version` | | print version and exit |

Endpoints: `/metrics` (exposition), `/healthz` (liveness/readiness probe).

## RBAC

The pod's ServiceAccount needs read access to HPAs:

```yaml
- apiGroups: ["autoscaling"]
  resources: ["horizontalpodautoscalers"]
  verbs: ["get", "list", "watch"]
```

## Build

```
make bin          # build the linux/amd64 binary into ./dist
make test         # unit tests
```

The binary is also built via the repository top-level Makefile:
`make MODULE=bkm-ksm-exporter build`.
