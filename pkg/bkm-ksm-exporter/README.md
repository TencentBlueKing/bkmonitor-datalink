# bkm-ksm-exporter

A small profile-based Prometheus exporter for Kubernetes compatibility and
extension metrics. The same image currently provides two isolated deployment
profiles:

- `hpa` (the default): emits `kube_hpa_*` from `autoscaling/v2`;
- `pod-terminating`: emits continuous `pod_terminating_seconds` series.

The profiles are intentionally deployed as separate Deployments. A large Pod
watch or Pod state capacity failure therefore cannot consume the HPA exporter's
cache or readiness budget.

## Why

kube-state-metrics v1.9.7 reads HPAs via the `autoscaling/v2beta1` API, which
Kubernetes **removed in v1.25**. On clusters >= 1.25 it can no longer list HPAs
and produces **no** `kube_hpa_*` metrics. This exporter reads HPAs from
`autoscaling/v2` (GA since Kubernetes 1.23) and emits the **same metric names,
labels and semantics as kube-state-metrics v1.9.7**, so existing dashboards,
alerting rules and metric-keep lists keep working unchanged on newer clusters.

The HPA profile is intentionally minimal and is meant as a high-version
compatibility supplement next to an existing kube-state-metrics deployment —
not a replacement. The collector registry (`exporter.Source`) is extensible, but
profiles with materially different cardinality or failure domains must remain
separate workloads.

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
| `kube_hpa_spec_target_metric` | `.spec.metrics[]` target (labels `metric_name`, `metric_target_type` one of `utilization`/`value`/`average`) |

> `kube_hpa_spec_target_metric` maps the `autoscaling/v2` `MetricSpec` to the same
> labels and values as kube-state-metrics v1.9.7: one series per target field that
> is set, with `metric_target_type` one of `utilization`/`value`/`average`. Like
> v1.9.7, a `Quantity` target that is not an exact integer (e.g. `1500m`) is
> skipped (`AsInt64` reports it cannot be represented), not emitted as `0`. The
> `autoscaling/v2`-only `ContainerResource` source (absent from the
> `autoscaling/v2beta1` that v1.9.7 read) is **not** emitted: the metric has no
> `container` label, so multiple `ContainerResource` targets differing only by
> container — e.g. the old/new pair recommended during a container rename — would
> collide into duplicate samples.

## Pod terminating profile

This profile exposes every Pod whose `metadata.deletionTimestamp` is set. It
does not apply a 1h/2h threshold: those are alert-strategy conditions, not
collection rules.

The collection path is:

1. stream a consistent, paginated cluster Pod List;
2. project and retain only deleting Pods while releasing each full page;
3. start a continuous Watch at the List snapshot resourceVersion;
4. reconnect ordinary Watch closures from the last resourceVersion;
5. relist only after resource-version expiry or lost consistency;
6. coalesce changes into ConfigMap checkpoints.

The Watch map is keyed by Pod UID, so a late Delete for an old Pod cannot remove
a same-name replacement. Metrics are keyed by `namespace/pod/node`. After a
persisted active dimension disappears, the exporter persists and exposes the
same dimension with value `0` for `recovery-hold`, allowing an ordinary static
threshold strategy to recover.

State PATCH results that may have reached the API server are read back before
the process commits its in-memory view or retries. A definitely failed write
does not freeze a recovery deadline; the first successfully persisted retry
gets a complete recovery hold.

Persistent memory is proportional to deleting Pods plus recovery entries, not
to total cluster Pods. Initial List transient memory is bounded by one API page.
Network and CPU are still proportional to cluster Pod Watch traffic. The
profile is disabled by default in the Chart and has independent resources,
page size, client QPS/burst and checkpoint interval.

## Run

Legacy HPA invocation remains unchanged:

```
bkm-ksm-exporter --listen=:8080
```

Pod terminating profile:

```
bkm-ksm-exporter \
  --collector=pod-terminating \
  --state-namespace=bkmonitor-operator \
  --state-configmap=pod-terminating-reporter-state
```

Common and HPA flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--collector` | `hpa` | `hpa`, `pod-terminating`, or the Helm hook profile `pod-terminating-state-init` |
| `--listen` | `:8080` | metrics HTTP listen address |
| `--kubeconfig` | `""` | kubeconfig for out-of-cluster runs; empty uses in-cluster config |
| `--resync` | `5m` | informer resync period |
| `--sync-timeout` | `2m` | max wait for the initial informer cache sync before exiting for restart |
| `--version` | | print version and exit |

Pod profile flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--state-namespace` | required | state ConfigMap namespace |
| `--state-configmap` | required | state ConfigMap name |
| `--page-limit` | `1000` | maximum Pods requested per List page |
| `--request-timeout` | `30s` | timeout for each List/Get/Patch request |
| `--checkpoint-interval` | `5s` | coalesced state persistence interval |
| `--recovery-hold` | `10m` | same-dimension zero retention |
| `--stale-after` | `15m` | suppress business rows after persistence remains unhealthy |
| `--client-qps` | `20` | Pod profile Kubernetes client QPS |
| `--client-burst` | `40` | Pod profile Kubernetes client burst |

Endpoints:

- `/metrics` — exposition. Returns **503 until the selected profile has completed
  its initial cache/snapshot sync and state checkpoint**.
- `/healthz` — liveness probe. 200 as soon as the process is up; it does not gate
  on cache readiness, so a slow sync will not get the pod restarted.
- `/readyz` — readiness probe. 200 once the cache has synced, 503 before. Wire
  this to the Kubernetes readiness probe.

## RBAC

The HPA ServiceAccount needs read access to HPAs:

```yaml
- apiGroups: ["autoscaling"]
  resources: ["horizontalpodautoscalers"]
  verbs: ["get", "list", "watch"]
```

The Pod profile needs cluster-wide `pods:list,watch` and only
`configmaps:get,patch` for its named state ConfigMap. A short-lived Helm hook
ServiceAccount has `configmaps:get,create` to create the state only when absent;
upgrade and reinstall validate and preserve existing state.

## Build

```
make bin          # build the linux/amd64 binary into ./dist
make test         # unit tests
```

The binary is also built via the repository top-level Makefile:
`make MODULE=bkm-ksm-exporter build`.
