// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evidenceroute"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/rollout"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// ChannelBinding contains process facts and optional business providers. The
// combination root may open the channel before a Worker Bundle is ready, then
// Bind the local providers after its business dependencies become available.
// Native must preserve Fleet's control-Leader forwarding contract.
type ChannelBinding struct {
	Native                    http.Handler
	Catalog                   *controlplane.RedisCatalogRepository
	Progress                  *progress.Store
	Settings                  *platformsettings.Cache
	Facts                     func() *observability.RuntimeConfigFacts
	Control                   cliControlBinding
	Roles                     roles.Set
	InstanceID, EnvironmentID string
	// SREAudit hears every SRE execution and management call; nil hears none.
	SREAudit func(obchannel.SREAction)
	// SRERolloutEnded hears every image change this process ended, and
	// SRERolloutsWatched how many it watches; nil hears none.
	SRERolloutEnded    func(rollout.Record)
	SRERolloutsWatched func(int)
	// SREReceiptWritten hears whether each receipt write landed; nil hears
	// none.
	SREReceiptWritten func(ok bool)
}

type roleChannelRuntime struct {
	Handler         http.Handler
	Executor        *obchannel.EvidenceExecutor
	EvidenceHandler func(context.Context, *pb.EvidenceRequest) (*pb.EvidenceResult, error)
	Restricted      bool
	HTTPConfigured  bool
	// Ready probes the same authorization store contract as the HTTP
	// surface; HTTPConfigured alone only records its local construction.
	Ready func(context.Context) error
	Bind  func(ChannelBinding)
	Close func() error
}

type channelProviders struct {
	mu      sync.RWMutex
	binding ChannelBinding
}

func (p *channelProviders) read() ChannelBinding {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.binding
}

func (p *channelProviders) bind(binding ChannelBinding) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if binding.Native == nil {
		binding.Native = p.binding.Native
	}
	if binding.Facts == nil {
		binding.Facts = p.binding.Facts
	}
	if binding.Settings == nil {
		binding.Settings = p.binding.Settings
	}
	if binding.Control.Metrics == nil {
		binding.Control.Metrics = p.binding.Control.Metrics
	}
	if binding.Control.PublicWindows == nil {
		binding.Control.PublicWindows = p.binding.Control.PublicWindows
	}
	if binding.Control.RedisFailures == nil {
		binding.Control.RedisFailures = p.binding.Control.RedisFailures
	}
	if binding.Control.RedisDialRetries == nil {
		binding.Control.RedisDialRetries = p.binding.Control.RedisDialRetries
	}
	// Roles and identity belong to this process for its whole incarnation.
	binding.Roles = p.binding.Roles
	binding.InstanceID, binding.EnvironmentID = p.binding.InstanceID, p.binding.EnvironmentID
	binding.Control.Incarnation, binding.Control.StreamToken = p.binding.Control.Incarnation, p.binding.Control.StreamToken
	p.binding = binding
}

func (p *channelProviders) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if native := p.read().Native; native != nil {
		native.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "business_waiting", "message": "Local business providers are not bound; target the control Leader or a business Worker."})
}

// openProductionRoleChannel opens lazy diagnostic and auth pools only. It
// performs no startup Ping, business refresh, Worker construction or lease
// admission. Internal evidence is configured independently of the HTTP key.
func openProductionRoleChannel(cfg config.Config, binding ChannelBinding) (_ *roleChannelRuntime, err error) {
	if binding.Roles == nil {
		binding.Roles = cfg.EffectiveRoles()
	}
	if binding.InstanceID == "" {
		binding.InstanceID = cfg.PhaseTwo.Worker.ID
	}
	if binding.EnvironmentID == "" {
		binding.EnvironmentID = cfg.CLI.EnvironmentID
	}
	if binding.EnvironmentID == "" {
		binding.EnvironmentID = cfg.Redis.StatePrefix
	}
	providers := &channelProviders{binding: binding}
	var clients []redis.UniversalClient
	var closeQuery func()
	var closeSRE func()
	var closeOnce sync.Once
	var closeErr error
	closeClients := func() error {
		closeOnce.Do(func() {
			if closeSRE != nil {
				closeSRE()
			}
			if closeQuery != nil {
				closeQuery()
			}
			var errs []error
			for _, client := range clients {
				errs = append(errs, client.Close())
			}
			closeErr = errors.Join(errs...)
		})
		return closeErr
	}
	defer func() {
		if err != nil {
			_ = closeClients()
		}
	}()
	clientFor := func(name string) func(config.RedisConnectionConfig) redis.UniversalClient {
		return func(connection config.RedisConnectionConfig) redis.UniversalClient {
			client := redis.NewUniversalClient(cliRedisOptions(connection, name, func(client, reason string) {
				if retry := providers.read().Control.RedisDialRetries; retry != nil {
					retry(client, reason)
				}
			}))
			clients = append(clients, client)
			return client
		}
	}
	failed := func(reason string) {
		if failure := providers.read().Control.RedisFailures; failure != nil {
			failure("evidence", reason, "")
		}
	}
	store, workload, diagnosticRuntime := deploymentReads(cfg, binding.Catalog, binding.Progress, clientFor("evidence"), failed)
	routingStore, err := ownership.NewRedisStoreWithClient(diagnosticRuntime, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"))
	if err != nil {
		return nil, err
	}
	catalog := binding.Catalog
	if catalog == nil {
		catalog, err = controlplane.NewRedisCatalogRepository(diagnosticRuntime, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg))
		if err != nil {
			return nil, err
		}
	}
	queryTransport := &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: obchannel.RequestTimeout}).DialContext,
		MaxConnsPerHost: 1, MaxIdleConns: 1, MaxIdleConnsPerHost: 1,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: obchannel.RequestTimeout, ResponseHeaderTimeout: obchannel.MaxExecutionTimeout}
	closeQuery = queryTransport.CloseIdleConnections
	queryClient, _ := accessuq.NewDiagnosticClient(cfg.PhaseTwo.Access.UQEndpoint, cfg.PhaseTwo.Access.QuerySource,
		&http.Client{Transport: queryTransport, Timeout: obchannel.MaxExecutionTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	// The operation schemas stay the same across roles; only their currently
	// bound providers and the ingress's default routing differ.
	ops := obchannel.NativeOperations(providers)
	for i := range ops {
		ops[i].DefaultControlLeader = true
		// object.list already has a replica domain filter. Preserve that
		// field and use only its internal default-Leader route.
		if _, domainReplica := ops[i].Fields["replica"]; !domainReplica {
			ops[i].Targetable = true
		}
	}
	ops = append(ops, store...)
	ops = append(ops, dynamicChannelOperation(cliRuntimeOperation(nil, nil), func() obchannel.Operation {
		current := providers.read()
		facts := current.Facts
		if facts == nil {
			facts = func() *observability.RuntimeConfigFacts { return nil }
		}
		return cliRuntimeOperation(facts, current.Settings)
	}))
	ops = append(ops, dynamicChannelOperation(cliLookbackOperation(nil, lookbackStanding{}), func() obchannel.Operation {
		current := providers.read()
		if current.Native == nil {
			return waitingChannelOperation("lookback.get")
		}
		return cliLookbackOperation(current.Control.Lookback, current.Control.LookbackStanding, current.Control.ReadHolds)
	}))
	ops = append(ops, dynamicChannelOperation(cliMaintenanceOperation(nil), func() obchannel.Operation {
		current := providers.read()
		if current.Native == nil {
			return waitingChannelOperation("maintenance.get")
		}
		return cliMaintenanceOperation(current.Control.Maintenance)
	}))
	ops = append(ops, cliLifecycleOperation(diagnosticRuntime, lifecycleRecordKey(cfg)))
	for i := range workload {
		if workload[i].ID != "k8s.workloads" {
			workload[i].Targetable = true
		}
	}
	ops = append(ops, workload...)
	for index, metricOp := range obchannel.MetricsOperations(nil) {
		ops = append(ops, dynamicChannelOperation(metricOp, func() obchannel.Operation {
			return obchannel.MetricsOperations(providers.read().Control.Metrics)[index]
		}))
	}
	ops = append(ops, obchannel.SlotOperations(obchannel.SlotOptions{Resolve: newCLISlotResolver(cfg, diagnosticRuntime), Evidence: newCLISlotEvidenceReader(cfg, diagnosticRuntime), UQ: queryClient, LatestPublication: cliLatestPublication(catalog)})...)
	if binding.Roles.Has(roles.Channel) {
		var sreOps []obchannel.Operation
		sreOps, closeSRE, err = channelSREOperations(cfg, binding, clientFor("sre")(cfg.RuntimeStoreRedis()))
		if err != nil {
			return nil, err
		}
		ops = append(ops, sreOps...)
	}
	diagnose := obchannel.DiagnoseOperation(providers, ops)
	diagnose.Targetable, diagnose.DefaultControlLeader = true, true
	ops = append(ops, diagnose)
	workerOnly := map[string]bool{"lookback.get": true, "maintenance.get": true, "k8s.pods": true, "k8s.events": true, "k8s.logs": true}
	if !binding.Roles.Has(roles.Worker) {
		for i := range ops {
			if workerOnly[ops[i].ID] {
				ops[i].Availability = func() obchannel.Availability {
					return obchannel.Availability{Reason: "worker_role_required: this process does not host a business Worker"}
				}
			}
		}
	}
	executor, err := obchannel.NewEvidenceExecutor(obchannel.ExecutorOptions{EnvironmentID: binding.EnvironmentID, Replica: binding.InstanceID,
		Incarnation: binding.Control.Incarnation, Build: version + "/" + commit, Concurrency: 1, Operations: ops, Roles: binding.Roles})
	if err != nil {
		return nil, err
	}
	runtime := &roleChannelRuntime{Executor: executor, Close: closeClients, Ready: func(context.Context) error {
		return &cliauth.Error{Code: "channel_disabled", Message: "The HTTP evidence authorization surface is not configured for this process.", HTTPStatus: http.StatusServiceUnavailable}
	}}
	var router *evidenceroute.Router
	if binding.Control.StreamToken != "" && binding.InstanceID != "" {
		router, err = evidenceroute.New(evidenceroute.Options{Store: routingStore, WorkerID: binding.InstanceID, StreamToken: binding.Control.StreamToken,
			EnvironmentID: binding.EnvironmentID, Build: version + "/" + commit, Incarnation: binding.Control.Incarnation, CatalogRevision: executor.CatalogRevision(), Execute: executor.ExecuteEvidence, AllowOperation: executor.AllowsTargetedOperation, Roles: binding.Roles})
		if err != nil {
			return nil, err
		}
		runtime.EvidenceHandler = router.Handle
		if binding.Control.Server != nil {
			binding.Control.Server.SetEvidenceHandler(router.Handle)
		}
	}
	runtime.Bind = func(binding ChannelBinding) {
		providers.bind(binding)
		if binding.Control.Server != nil {
			binding.Control.Server.SetEvidenceHandler(runtime.EvidenceHandler)
		}
	}
	publicNative := obchannel.WithDeploymentSection(providers, append(store, workload...))
	restrictPublic := func() {
		runtime.Restricted = true
		windows := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if handler := providers.read().Control.PublicWindows; handler != nil {
				handler.ServeHTTP(w, r)
			} else {
				http.NotFound(w, r)
			}
		})
		publicNative = publicAPI(providers, windows)
	}
	// Explicit Worker/Control processes serve business evidence through
	// authenticated internal RPC regardless of a local HTTP key. Shared
	// deployments retain their existing HTTP auth fallback.
	if !binding.Roles.Has(roles.Channel) {
		restrictPublic()
	}
	runtime.Handler = composeCLI(publicNative, nil, nil)
	if !binding.Roles.Has(roles.Channel) || !cfg.CLI.Enabled() {
		return runtime, nil
	}
	manager, authErr := cliauth.New(cliauth.Options{Redis: clientFor("auth")(cfg.RuntimeStoreRedis()), Prefix: cfg.Redis.StatePrefix,
		EnvironmentID: cfg.CLI.EnvironmentID, EnvironmentName: cfg.CLI.EnvironmentName, PublicBaseURL: cfg.CLI.PublicBaseURL, AdminKey: cfg.CLI.AdminKey,
		OnStoreFailure: func(reason, detail string) {
			if failure := providers.read().Control.RedisFailures; failure != nil {
				failure("auth", reason, detail)
			}
		}})
	if authErr != nil {
		return runtime, nil
	}
	options := obchannel.Options{Auth: manager, EnvironmentID: binding.EnvironmentID, Replica: binding.InstanceID, Incarnation: binding.Control.Incarnation,
		Build: version + "/" + commit, Executor: executor, RouteControlDefaults: !binding.Roles.Has(roles.Worker)}
	if !binding.Roles.Has(roles.Worker) {
		options.RequireWorkerTarget = workerOnly
	}
	if router != nil {
		options.Route = router.Invoke
	}
	channel, err := obchannel.New(options)
	if err != nil {
		return nil, err
	}
	if cfg.PublicSurfaceRestrictionRequested() {
		restrictPublic()
	}
	runtime.Handler = composeCLI(publicNative, channel, manager.Handler())
	runtime.HTTPConfigured = true
	runtime.Ready = func(ctx context.Context) error {
		// ActivePairings uses the auth pool's real Lua/read contract within
		// its existing one-second budget, including only expired-ledger
		// housekeeping. It issues and renews no credentials.
		_, err := manager.ActivePairings(ctx)
		return err
	}
	return runtime, nil
}

func dynamicChannelOperation(prototype obchannel.Operation, current func() obchannel.Operation) obchannel.Operation {
	prototype.Run = func(ctx context.Context, params obchannel.Params) obchannel.Outcome {
		return current().Run(ctx, params)
	}
	prototype.Availability = func() obchannel.Availability {
		operation := current()
		if operation.Availability != nil {
			return operation.Availability()
		}
		return obchannel.Availability{Available: true}
	}
	return prototype
}

func waitingChannelOperation(id string) obchannel.Operation {
	return obchannel.Operation{ID: id, Run: func(context.Context, obchannel.Params) obchannel.Outcome {
		return obchannel.Outcome{Error: &obchannel.Failure{Code: "business_waiting", Message: "Business Worker providers are waiting for their runtime binding."}}
	}}
}
