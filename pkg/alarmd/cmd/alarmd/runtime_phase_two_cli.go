// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/k8sread"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

type cliRuntimeFacts struct {
	Scope            string                            `json:"scope"`
	ReadAt           time.Time                         `json:"read_at"`
	Config           *observability.RuntimeConfigFacts `json:"config"`
	PlatformSettings *platformsettings.Observation     `json:"platform_settings"`
}

type cliControlBinding struct {
	Incarnation string
	StreamToken string
	Server      *viewstream.Server
	// Metrics is this process's registry, read by metrics.get. Nil leaves
	// the operation listed and unavailable with its reason.
	Metrics prometheus.Gatherer
	// PublicWindows serves /api/windows on a restricted public surface
	// (fleet.NewPublicWindowsHandler). Nil leaves the route unserved there.
	PublicWindows http.Handler
	// RedisFailures, when set, hears an unanswered call of the CLI's own
	// Redis clients -- evidence, auth -- by its reason, with the error's
	// bounded text where the answer cannot carry it (auth).
	RedisFailures func(client, reason, detail string)
	// RedisDialRetries, when set, hears each second dial of the CLI's own
	// Redis clients -- evidence, auth -- by why the first dial failed.
	RedisDialRetries func(client, reason string)
	// Lookback is this process's late-data lookback, nil when it does not
	// run one, and LookbackStanding why; read by lookback.get.
	Lookback         *lookback.Engine
	LookbackStanding lookbackStanding
	ReadHolds        *productionReadHolds
	// Maintenance is the effective-time maintenance, bound once the loop is
	// built; read by maintenance.get.
	Maintenance *maintenanceSource
}

// cliRedisFailures counts an unanswered call of a CLI client by its reason,
// and for the authorization store, whose public answer carries the reason
// only, also logs the error's bounded text as a limited auth_store line.
func cliRedisFailures(recorder *metric.Recorder, observer observability.Observer) func(client, reason, detail string) {
	return func(client, reason, detail string) {
		recorder.ObserveDiagnosticRedisFailure(client, reason)
		if client != "auth" {
			return
		}
		observeRuntime(context.Background(), observer, observability.Observation{
			Component: observability.ComponentRuntime, Stage: observability.StageAuthStore,
			Result: observability.ResultFailed, Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonContractRetryable, Err: fmt.Errorf("authorization store %s: %s", reason, detail),
		})
	}
}

// cliRedisOptions are one CLI Redis client's options: a small pool that
// connects on first use, whose dials are tried a second time when the first
// fails (withDialRetry), counted under the client's name.
func cliRedisOptions(connection config.RedisConnectionConfig, name string, retries func(client, reason string)) *redis.UniversalOptions {
	options := observationRedisOptions(connection)
	options.PoolSize = 2
	options.MinIdleConns = 0
	withDialRetry(options, goRedisDial(options), func(reason string) {
		if retries != nil {
			retries(name, reason)
		}
	})
	return options
}

// cliLifecycleOperation reads every replica's start and stop record, which
// outlives the Pods it describes.
func cliLifecycleOperation(client redis.UniversalClient, key string) obchannel.Operation {
	return obchannel.Operation{ID: "lifecycle.get", Summary: "读取各副本最近的启动与停止记录（含停止原因、错误），以及同名副本两次启动之间没写停止的次数（OOM、SIGKILL 这类不干净退出）；Pod 被删后仍可读。" +
		"停止记录的 drain 是停止时等在途 Slot 的结果：outcome（idle 没有在跑的、finished 都返回了、deadline 到停止时限取消了还在跑的、deadline_unreturned 取消后仍有没返回的）、waited 开始等时在跑几个、cancelled 到时限还在跑被取消几个、unreturned 取消后没返回几个、wait_ms 等了多久；deadline 与 deadline_unreturned 时这些 Slot 的事件可能已发出而状态写入被拒，下一任属主会再发一次。",
		EvidenceScope: "deployment", Fields: map[string]obchannel.Field{}, OutputSchema: obchannel.SchemaOf(lifecycleReading{}),
		Limits: map[string]any{"entries": lifecycleRecordEntries, "redis_commands": 1},
		Run: func(ctx context.Context, _ obchannel.Params) obchannel.Outcome {
			reading, err := readLifecycle(ctx, client, key)
			if err != nil {
				return obchannel.Outcome{Error: &obchannel.Failure{Code: "store_unavailable", Message: "the runtime store did not answer the lifecycle read"}}
			}
			return obchannel.Outcome{Value: reading, Complete: reading.Unreadable == 0,
				Limitations: []string{"Only the last 64 starts and stops are kept.",
					"unclean_restarts counts starts not followed by a stop record: an OOM kill or SIGKILL, but also a stop the store could not take. Not every one is a crash.",
					"The replica name is the worker id, which is the Pod name: a container restarted in place counts under the same name, while a Pod that died and was replaced leaves its old name's last entry a start - read the old name's last entry for that case."}}
		}}
}

func cliRuntimeOperation(facts func() *observability.RuntimeConfigFacts, settings *platformsettings.Cache) obchannel.Operation {
	return obchannel.Operation{ID: "runtime.get", Summary: "读取实际回答进程的运行配置、预算、连接位置和已采用动态配置；可指定实例或按执行租约定位逻辑 Worker。", EvidenceScope: "process", Targetable: true, Fields: map[string]obchannel.Field{}, OutputSchema: obchannel.SchemaOf(cliRuntimeFacts{}), Limits: map[string]any{"redis_commands": 0, "scope": "answering_replica"}, Run: func(context.Context, obchannel.Params) obchannel.Outcome {
		value := cliRuntimeFacts{Scope: "answering_replica", ReadAt: time.Now().UTC(), Config: facts()}
		if settings != nil {
			observed := settings.Observe()
			value.PlatformSettings = &observed
		}
		out := obchannel.Outcome{Value: value, Complete: value.Config != nil && value.PlatformSettings != nil, Limitations: []string{"Use meta.answered_by to identify this process. These facts do not prove other replicas have the same version."}}
		return out
	}}
}

// buildPhaseTwoCLI allocates only small, lazy diagnostic pools. There is no
// startup Ping, refresh loop or detector dependency. Configuration or auth-store
// failures disable the CLI surface, never the existing execution path.
//
// native is the whole API. The CLI operations read it as it is. The public
// surface serves it whole unless the configuration asks for a restricted
// surface and the CLI came up in full; restricted says which, and then the
// public surface is publicAPI's. A CLI that fails to come up leaves the
// surface open, since restricting it would leave no way in.
func buildPhaseTwoCLI(cfg config.Config, native http.Handler, catalog *controlplane.RedisCatalogRepository, progressStore *progress.Store, settings *platformsettings.Cache, facts func() *observability.RuntimeConfigFacts, control cliControlBinding) (handler http.Handler, closeCLI func() error, restricted bool) {
	runtime, err := openProductionRoleChannel(cfg, ChannelBinding{Native: native, Catalog: catalog, Progress: progressStore,
		Settings: settings, Facts: facts, Control: control})
	if err != nil {
		return composeCLI(native, nil, nil), func() error { return nil }, false
	}
	return runtime.Handler, runtime.Close, runtime.Restricted
}

// deploymentReads builds the reads the deployment section is made of: the
// Redis evidence operations (store.info among them) and alarmd's own
// workload (k8s.pods among them). The CLI registers them as operations; a
// public diagnosis without the CLI composes its deployment section from the
// same ones. It also returns the diagnostic runtime pool, which the CLI's
// route discovery reuses.
func deploymentReads(cfg config.Config, catalog *controlplane.RedisCatalogRepository, progressStore *progress.Store,
	newClient func(config.RedisConnectionConfig) redis.UniversalClient, failed func(reason string)) (store, workload []obchannel.Operation, diagnosticRuntime redis.UniversalClient) {
	bind := func(role string, connection config.RedisConnectionConfig, prefix string) obevidence.RedisBinding {
		return obevidence.RedisBinding{Client: newClient(connection), Location: obevidence.Location{Role: role, Address: redisAddress(connection), Mode: connection.Mode, DB: connection.DB, Prefix: prefix}}
	}
	options := obevidence.Options{Catalog: catalog, Progress: progressStore}
	options.SourceStrategy = bind("strategy_cache", cfg.StrategySourceRedis(), cfg.PhaseTwo.Control.StrategyCachePrefix)
	options.CMDBCache = bind("cmdb_cache", cfg.CMDBCacheRedis(), cfg.PlatformKeyPrefix())
	// Catalog and progress share a diagnostic runtime pool, not detector I/O.
	runtimeConnection := cfg.RuntimeStoreRedis()
	diagnosticRuntime = newClient(runtimeConnection)
	options.Published = obevidence.RedisBinding{Client: diagnosticRuntime, Location: obevidence.Location{Role: "runtime", Address: redisAddress(runtimeConnection), Mode: runtimeConnection.Mode, DB: runtimeConnection.DB, Prefix: cfg.Redis.StatePrefix}}
	options.QueryProgress = options.Published
	// Shared records remain readable before a business Bundle exists. These
	// constructors validate coordinates only; every connection remains lazy.
	if options.Catalog == nil {
		options.Catalog, _ = controlplane.NewRedisCatalogRepository(diagnosticRuntime,
			productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg))
	}
	if progressStore == nil {
		if keys, err := ownership.NewRedisStoreWithClient(diagnosticRuntime, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership")); err == nil {
			options.Progress, _ = progress.NewObservationKeys(productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), keys)
		}
	}
	// The pool records sit in the same runtime store, under their own prefix.
	options.QueryCooldown = obevidence.RedisBinding{Client: diagnosticRuntime, Location: obevidence.Location{Role: "runtime", Address: redisAddress(runtimeConnection), Mode: runtimeConnection.Mode, DB: runtimeConnection.DB, Prefix: queryCooldownPrefix(cfg)}}
	if connection, configured := cfg.TargetGroupRedis(); configured {
		options.TargetGroup = bind("target_group", connection, targetGroupPrefix(cfg))
	}
	if connection, configured := cfg.DynamicConfigRedis(); configured {
		options.DynamicConfig = bind("dynamic_config", connection, cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix)
	}
	// alarmd's own workload, read through this Pod's ServiceAccount. Any
	// replica answers, so a crashing one is read from one that is up.
	// The owner chain starts at this Pod: its hostname is its name, whatever
	// the worker id is configured to.
	podName, _ := os.Hostname()
	// Every binding reports an unanswered read by its reason.
	for _, binding := range []*obevidence.RedisBinding{&options.SourceStrategy, &options.CMDBCache, &options.Published,
		&options.QueryProgress, &options.QueryCooldown, &options.TargetGroup, &options.DynamicConfig} {
		binding.OnFailure = failed
	}
	return obchannel.StoreOperations(obevidence.New(options)),
		obchannel.K8sOperations(k8sread.New(k8sread.Options{PodName: podName}), workloadDependencies(cfg), config.ObservedNamespaces),
		diagnosticRuntime
}

// workloadDependencies is every address this process connects to, taken
// from the same endpoint list the first screen shows, one entry per host:
// the workload read derives a namespace from each and connects to none of
// them. A sentinel deployment's address is its master name followed by the
// sentinels; each sentinel is an entry. An endpoint the configuration does
// not name adds nothing.
func workloadDependencies(cfg config.Config) []k8sread.Dependency {
	var dependencies []k8sread.Dependency
	// The same endpoints the bundle resolves, the compatibility output's
	// service Redis included: every address this process connects to.
	for _, endpoint := range resolveEndpoints(cfg, endpointSharing{compatOutputPresent: true}) {
		if !endpoint.Configured || endpoint.Address == "" {
			continue
		}
		address := endpoint.Address
		if at := strings.Index(address, "@"); endpoint.Kind == "redis" && at >= 0 {
			address = address[at+1:]
		}
		for _, host := range strings.Split(address, ",") {
			if host = strings.TrimSpace(host); host != "" {
				dependencies = append(dependencies, k8sread.Dependency{Name: endpoint.Role, Address: host})
			}
		}
	}
	return dependencies
}

// publicSurfaceStanding is what the replica says about its public surface
// once the CLI is built. Both are named degradations and a startup warning;
// neither stops the process, since detection matters more than either.
type publicSurfaceStanding struct {
	// MetricsUnexported: the surface is restricted and there is no internal
	// listener, so /metrics is served nowhere.
	MetricsUnexported bool
	// CLIUnavailable: the configuration asks for a restricted surface and the
	// CLI did not come up, so the surface stays open -- restricting it would
	// leave no way in -- and the coordinates it carries are public.
	CLIUnavailable bool
}

func publicSurfaceStandingOf(cfg config.Config, restricted bool) publicSurfaceStanding {
	return publicSurfaceStanding{
		MetricsUnexported: restricted && cfg.HTTP.InternalListen == "",
		CLIUnavailable:    cfg.PublicSurfaceRestrictionRequested() && !restricted,
	}
}

func (standing publicSurfaceStanding) warn(logger *observability.Logger) {
	if standing.MetricsUnexported {
		logger.Warn(observability.StageStartup, observability.ResultDegraded, 0, 0,
			slog.String("reason_code", string(fleet.DegradationMetricsUnexported)),
			slog.String("detail", "the public listener is restricted and http.internal_listen is not set: /metrics is served nowhere"))
	}
	if standing.CLIUnavailable {
		logger.Warn(observability.StageStartup, observability.ResultDegraded, 0, 0,
			slog.String("reason_code", string(fleet.DegradationCLIAuthUnavailable)),
			slog.String("detail", "a CLI admin key is configured and the CLI did not come up: the public surface stays unrestricted"))
	}
}

// publicAPI is what a restricted public surface serves of the API: the
// summary without deployment coordinates and the observation windows; every
// other route is refused as moved behind the CLI session, with the way in
// named. The CLI routes -- the page's, issuing, exchange, pairing, renewal --
// are composed in front of it and stay public, so a caller whose every
// credential has expired can still get a new one.
func publicAPI(native, windows http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			publicHealth(w, r, native)
		case "/api/windows":
			if windows == nil {
				http.NotFound(w, r)
				return
			}
			windows.ServeHTTP(w, r)
		default:
			publicsurface.Refuse(w, r)
		}
	})
}

// publicHealth answers the health route with fleet.PublicHealth of what the
// API answers. Anything but a readable answer is reported in fixed words:
// the API's own error text is not public.
func publicHealth(w http.ResponseWriter, r *http.Request, native http.Handler) {
	captured := &capturedResponse{header: http.Header{}}
	native.ServeHTTP(captured, r)
	var full fleet.HealthResponse
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if captured.code != 0 && captured.code != http.StatusOK || json.Unmarshal(captured.body.Bytes(), &full) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "health summary unavailable"})
		return
	}
	_ = json.NewEncoder(w).Encode(fleet.PublicHealth(full))
}

// capturedResponse holds a handler's answer so it can be reduced before it
// is sent.
type capturedResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *capturedResponse) Header() http.Header         { return c.header }
func (c *capturedResponse) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *capturedResponse) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func composeCLI(native, channel, auth http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var handler http.Handler
		switch {
		case r.URL.Path == "/api/cli/channel":
			handler = channel
		case slices.Contains(cliauth.Paths, r.URL.Path):
			handler = auth
		default:
			native.ServeHTTP(w, r)
			return
		}
		if handler != nil {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": map[string]string{"code": "cli_not_configured", "message": "CLI configuration is invalid or unavailable; check environment identity, HTTP(S) public URL and administrator key configuration."}})
	})
}
