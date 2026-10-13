// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet/ui"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/storecensus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

var errPhaseTwoWorkerBundleNotAssembled = errors.New(
	"phase-two Go Access worker bundle is not assembled",
)

var errPhaseTwoWorkerDraining = errors.New("phase-two Go Access worker is draining")

var errPhaseTwoWorkerStopped = errors.New("phase-two Go Access worker stopped before application shutdown")

// phaseTwoControlDependencyReason is the fixed low-cardinality readiness
// reason reported while the control plane or the Ownership Store cannot be
// reached. Both are Redis-backed shared dependencies: the Worker stays alive,
// keeps already-owned Query Groups running and retries on the next tick.
var phaseTwoControlDependencyReason = observability.ReasonCode(contract.ReasonRedisUnavailable)

// phaseTwoInvariantError marks a programming or shared-runtime invariant
// violation inside the control loop. It is the only reconcile error class
// that may stop Run. Every other refresh, reconcile or Assignment error is a
// transient dependency failure by default: it degrades readiness with
// phaseTwoControlDependencyReason and is retried on the next tick.
type phaseTwoInvariantError struct{ err error }

func newPhaseTwoInvariantError(message string) error {
	return &phaseTwoInvariantError{err: errors.New(message)}
}

func (err *phaseTwoInvariantError) Error() string { return err.err.Error() }

func (err *phaseTwoInvariantError) Unwrap() error { return err.err }

func isPhaseTwoInvariantError(err error) bool {
	var invariant *phaseTwoInvariantError
	return errors.As(err, &invariant)
}

type phaseTwoApplicationDependencies struct {
	configureCPU func() (string, error)
	run          func(context.Context, config.Config, *metric.Recorder, *observability.Logger) error
	openBundle   func(
		context.Context,
		config.Config,
		*metric.Recorder,
		*observability.Logger,
		*phaseTwoApplicationHealth,
	) (*phaseTwoWorkerBundle, error)
	openRoles      func(config.Config, *metric.Recorder, *observability.RuntimeConfigFacts, *observability.Logger) (*phaseTwoRoleRuntime, error)
	openRoleBundle func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth, *phaseTwoRoleRuntime) (*phaseTwoWorkerBundle, error)
	newHTTP        func(*metric.Recorder, observability.HealthSource, httpSurface) (httpRuntime, error)
	// lifecycle opens the process's start/stop record; nil records nothing.
	lifecycle func(config.Config) *lifecycleRecord
}

// httpSurface is what the listener needs from the configuration: where the
// side listeners bind, whether the public surface is restricted, and where
// the login page says the administrator key is kept (names only).
type httpSurface struct {
	Diagnostics    string
	Internal       string
	Restricted     bool
	AdminKeySecret ui.AdminKeySecret
}

func httpSurfaceOf(cfg config.Config) httpSurface {
	secret := cfg.CLI.AdminKeySecret
	return httpSurface{Diagnostics: cfg.HTTP.DiagnosticsListen, Internal: cfg.HTTP.InternalListen, Restricted: cfg.PublicSurfaceRestrictionRequested(),
		AdminKeySecret: ui.AdminKeySecret{Namespace: secret.Namespace, Name: secret.Name, Key: secret.Key}}
}

type runtimeModeDependencies struct {
	logger   *observability.Logger
	phaseTwo phaseTwoApplicationDependencies
}

func defaultPhaseTwoApplicationDependencies() phaseTwoApplicationDependencies {
	return phaseTwoApplicationDependencies{
		configureCPU: configurePhaseTwoCPU, lifecycle: newLifecycleRecord,
		run: runPhaseTwoApplication, openBundle: openProductionPhaseTwoBundle,
		openRoles: openPhaseTwoRoleRuntime, openRoleBundle: openProductionRoleBundle,
		newHTTP: func(recorder *metric.Recorder, source observability.HealthSource, surface httpSurface) (httpRuntime, error) {
			options := []httpservice.Option{httpservice.WithDiagnosticsAddress(surface.Diagnostics), httpservice.WithInternalAddress(surface.Internal),
				httpservice.WithAdminKeySecret(surface.AdminKeySecret)}
			if surface.Restricted {
				options = append(options, httpservice.WithRestrictedPublicSurface())
			}
			return httpservice.NewWithHealth(recorder, source, options...)
		},
	}
}

// phaseTwoApplication is the construction and health boundary around the one
// production Worker Bundle.
type phaseTwoApplication struct {
	health *phaseTwoApplicationHealth
}

func (a *phaseTwoApplication) HealthSnapshot() observability.HealthSnapshot {
	if a == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{PhaseTwo: true})
	}
	return a.health.HealthSnapshot()
}

func newPhaseTwoApplication(cfg config.Config) (*phaseTwoApplication, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate phase-two application configuration: %w", err)
	}
	health := newPhaseTwoApplicationHealth()
	health.selectedRoles = cfg.EffectiveRoles()
	return &phaseTwoApplication{health: health}, nil
}

func runPhaseTwoApplication(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
) error {
	return runPhaseTwoApplicationWithDependencies(ctx, cfg, recorder, logger, defaultPhaseTwoApplicationDependencies())
}

func runPhaseTwoApplicationWithDependencies(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	dependencies phaseTwoApplicationDependencies,
) error {
	if ctx == nil || recorder == nil {
		return errors.New("phase-two application requires context and metric recorder")
	}
	if dependencies.openBundle == nil && dependencies.openRoleBundle == nil {
		return errPhaseTwoWorkerBundleNotAssembled
	}
	if dependencies.newHTTP == nil {
		return errors.New("phase-two application requires HTTP service factory")
	}
	application, err := newPhaseTwoApplication(cfg)
	if err != nil {
		return err
	}
	cpuSource := observability.CPUSourceRuntimeDefault
	if dependencies.configureCPU != nil {
		cpuSource, err = dependencies.configureCPU()
		if err != nil {
			return err
		}
	}
	// The collector's budget comes from the same container as every other
	// budget, and is installed here rather than at configuration load because
	// --check-config reports the derived pair without being the process that
	// has to run under it.
	applyPhaseTwoGoRuntime(config.DeriveGoRuntime(config.DetectCapacityInputs()), runtimeGoBudgetSetters())
	// Resolve the pool before the profile is derived so the startup facts carry
	// the concrete size rather than the zero that means "derive".
	cfg = cfg.WithResolvedRedisPoolSize()
	profile, err := phaseTwoRuntimeProfile(cfg, cpuSource, runtime.GOMAXPROCS(0))
	if err != nil {
		return fmt.Errorf("derive phase-two runtime profile: %w", err)
	}
	// The same profile, published as metrics rather than only printed once at
	// startup. Every per-Slot budget is derived from the container's memory
	// limit, and until now a rejection could be observed without any way to see
	// the ceiling it hit short of getting into the Pod.
	if err := recorder.BindCapacityLoad(capacityLoadSource(profile)); err != nil {
		return fmt.Errorf("bind capacity load metrics: %w", err)
	}
	if logger == nil {
		logger = observability.Discard(observability.ComponentRuntime)
	}
	// Report the diagnostics surface at startup. An unset address serves no
	// pprof, and losing it without a single line would be the same silent
	// capability loss the split exists to prevent.
	//
	// The collector budget rides on the same line. Nothing outside the process
	// can set it, so this and the facts table are the only two places anyone
	// reading a running Pod can find out what the collector was told.
	logger.Info(observability.StageStartup, observability.ResultStarted, 0, 0,
		slog.String("diagnostics_listen", cfg.HTTP.DiagnosticsFact()),
		slog.Int64("go_memory_limit_bytes", profile.Capacity.GoMemoryLimitBytes),
		slog.Int("go_gc_percent", profile.Capacity.GoGCPercent),
		slog.String("memory_source", profile.MemorySource))
	// A series delivery digest assembled from its shared parts that differed
	// from the generic one: counted on canonical_encoding_records_shadow_total
	// and said here at most once a minute. The generic digest was returned.
	contract.SetRecordsDigestDivergenceReporter(func(divergence contract.RecordsDigestDivergence) {
		logger.Warn("canonical_encoder", "records_digest_differed", divergence.Records, 0,
			slog.String("domain", divergence.Domain), slog.String("served", divergence.Served),
			slog.String("established", divergence.Established))
	})

	server, err := dependencies.newHTTP(recorder, application, httpSurfaceOf(cfg))
	if err != nil {
		return err
	}
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	httpContext, cancelHTTP := context.WithCancel(runtimeContext)
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Run(httpContext, cfg.HTTP.Listen, cfg.ShutdownTimeout.Duration()) }()

	var lifecycle *lifecycleRecord
	if dependencies.lifecycle != nil {
		lifecycle = dependencies.lifecycle(cfg)
	}
	if lifecycle != nil {
		lifecycle.failed = func(reason string) { recorder.ObserveDiagnosticRedisFailure("lifecycle", reason) }
	}
	defer lifecycle.close()
	lifecycle.start()
	var roleRuntime *phaseTwoRoleRuntime
	if dependencies.openRoles != nil {
		roleRuntime, err = dependencies.openRoles(cfg, recorder, &profile, logger)
		if err == nil {
			defer roleRuntime.Close()
			server.SetPublicSurfaceRestricted(roleRuntime.Channel.Restricted)
			server.SetAPI(roleRuntime.Channel.Handler)
			server.SetGRPC(roleRuntime.EvidenceServer)
			roleRuntime.Start(runtimeContext, application.health)
		}
	}
	var bundle *phaseTwoWorkerBundle
	if err == nil {
		if dependencies.openRoleBundle != nil {
			bundle, err = dependencies.openRoleBundle(runtimeContext, cfg, recorder, logger, application.health, roleRuntime)
		} else {
			bundle, err = dependencies.openBundle(runtimeContext, cfg, recorder, logger, application.health)
		}
	}
	if err == nil && bundle != nil {
		// Publish startup facts before making the evidence handler reachable.
		bundle.runtimeConfig = &profile
	}
	if err == nil && bundle != nil && bundle.dependencies.FleetAPI != nil {
		// The listener starts before this runtime does, so the API answers
		// "not ready" until here rather than pretending to have no data.
		server.SetPublicSurfaceRestricted(bundle.dependencies.PublicSurfaceRestricted)
		server.SetAPI(bundle.dependencies.FleetAPI)
	}
	if err == nil && bundle != nil && bundle.dependencies.ControlStream != nil {
		// The same for the control stream: a Worker that connects before
		// this point is told the stream is not ready and tries again.
		server.SetGRPC(bundle.dependencies.ControlStream)
	}
	if err == nil && bundle != nil {
		bundle.liveness.logger = logger
		server.SetLiveness(bundle.liveness)
	}
	if err != nil {
		lifecycle.stop(lifecycleStopStartFailed, err, nil)
		cancelRuntime()
		cancelHTTP()
		httpErr := waitRuntimeComponent(httpDone, time.Now().Add(cfg.ShutdownTimeout.Duration()))
		return errors.Join(err, normalizeRuntimeShutdownError(httpErr, false))
	}
	if bundle == nil && roleRuntime == nil {
		cancelRuntime()
		cancelHTTP()
		_ = waitRuntimeComponent(httpDone, time.Now().Add(cfg.ShutdownTimeout.Duration()))
		return errPhaseTwoWorkerBundleNotAssembled
	}
	bundleDone := make(chan error, 1)
	go func() {
		if bundle != nil {
			bundleDone <- bundle.Run(runtimeContext)
			return
		}
		bundleDone <- roleRuntime.Run(runtimeContext, application.health)
	}()

	var runErr, httpErr error
	bundleFinished := false
	httpFinished := false
	bundleStoppedEarly := false
	httpStoppedEarly := false
	select {
	case <-ctx.Done():
	case runErr = <-bundleDone:
		bundleFinished = true
		bundleStoppedEarly = ctx.Err() == nil
		if runErr == nil && bundleStoppedEarly {
			runErr = errPhaseTwoWorkerStopped
		}
		if bundleStoppedEarly {
			markPhaseTwoFatal(runtimeContext, bundle, application.health, runErr)
		}
	case httpErr = <-httpDone:
		httpFinished = true
		httpStoppedEarly = ctx.Err() == nil
		if httpErr == nil && httpStoppedEarly {
			httpErr = errHTTPServiceStopped
		}
		if httpStoppedEarly {
			markPhaseTwoFatal(runtimeContext, bundle, application.health, httpErr)
		}
	}
	stopReason := lifecycleStopReason(ctx.Err() != nil, bundleStoppedEarly, httpStoppedEarly)
	stopErr := errors.Join(runErr, httpErr)
	cancelRuntime()
	cancelHTTP()
	// The bundle drains its Slots within one shutdown timeout and releases
	// its leases within another (phaseTwoWorkerBundle.Run).
	bundleDeadline := time.Now().Add(2 * cfg.ShutdownTimeout.Duration())
	httpDeadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
	if !bundleFinished {
		runErr = waitRuntimeComponent(bundleDone, bundleDeadline)
	}
	if !httpFinished {
		httpErr = waitRuntimeComponent(httpDone, httpDeadline)
	}
	// Written once the bundle has stopped, so the stop says how its Slots
	// drained: a stop that cancelled Slots at the deadline is one whose
	// events may be sent again by the next owner, and this record is the
	// one reading of it that outlives the Pod.
	var drain *observability.SlotDrainFacts
	if bundle != nil {
		drain = bundle.shutdownDrain()
	}
	lifecycle.stop(stopReason, stopErr, drain)
	result := errors.Join(
		normalizeRuntimeShutdownError(runErr, bundleStoppedEarly),
		normalizeRuntimeShutdownError(httpErr, httpStoppedEarly),
	)
	if result == nil {
		logger.Info(observability.StageShutdown, observability.ResultSuccess, 0, 0)
	} else {
		logger.Error(observability.StageShutdown, observability.ResultFailed, 0, 0)
	}
	return result
}

type phaseTwoControlRefreshStatus string

const (
	phaseTwoControlHealthy          phaseTwoControlRefreshStatus = "HEALTHY"
	phaseTwoControlDegradedLastGood phaseTwoControlRefreshStatus = "DEGRADED_LAST_GOOD"
)

type phaseTwoControlRefreshResult struct {
	QueryGroups           []execution.QueryGroupIdentity
	Status                phaseTwoControlRefreshStatus
	SourceRefreshObserved bool
	SourceKind            observability.SourceKind
	ReasonCode            observability.ReasonCode
	Cause                 error
	// QueryGroupsRetained says this round could not read the active set, so
	// the receiver keeps the one it already has. It is not an empty set: an
	// empty set is an answer, and applying one here would take every Query
	// Group off this replica on a round that learned nothing -- which is the
	// same local failure toppling the whole that the degraded status exists
	// to prevent. Only a degraded round may set it; a healthy round that
	// does not know the set is not healthy.
	QueryGroupsRetained bool
	// Composition is what the Catalog the round built is made of, when the
	// round built one. Absent on a degraded round and on an activation load,
	// neither of which composed a Catalog: the process then keeps the
	// composition it last had rather than reporting an empty one, because
	// empty and "the Catalog has nothing in it" are not the same answer.
	Composition *controlplane.CatalogComposition
	// ChangeSignalPresent and ChangeSignalAgeSeconds are the source's change
	// marker as the round read it, delivered with the composition so the
	// fleet can say how old the writer's content is beside what it withheld.
	ChangeSignalPresent    bool
	ChangeSignalAgeSeconds int64
	// Activation is what this round did about bringing the activation to the
	// publication the source produced, when it tried. Absent on a round that
	// did not try: a follower's load, a source failure before any publication
	// existed. The bundle keeps a standing from these, because a round that
	// fails here leaves the source fresh and the fleet executing content that
	// is no longer the current publication -- and the source clock alone
	// cannot see that.
	Activation *phaseTwoActivationOutcome
}

// phaseTwoActivationOutcome is one activation attempt's result. Published is
// the publication the round tried to bring the activation to; Applied is the
// one the activation is actually on afterwards -- equal to Published on
// success, the last good one on failure, and zero when even that could not
// be read. Failure is the bounded classification the attempt failed with.
type phaseTwoActivationOutcome struct {
	Published controlplane.SnapshotPublicationRef
	Applied   controlplane.SnapshotPublicationRef
	Failure   *controlplane.ActivationFailure
	Cause     error
}

type phaseTwoControlRuntime interface {
	InitialRefresh(context.Context) (phaseTwoControlRefreshResult, error)
	Refresh(context.Context) (phaseTwoControlRefreshResult, error)
	LoadActive(context.Context) (phaseTwoControlRefreshResult, error)
	// ControlVersion reads the activation header live. Every publication stamps
	// it, so one read answers "did any Query Group's schedule change" for the
	// whole replica. The second result is false when there is no header, which
	// is as untrustworthy as a failed read: with no header nothing downstream is
	// anchored to a published version.
	ControlVersion(context.Context) (string, bool, error)
	// SourceRefreshSuccessAt reads the persisted time of the last refresh
	// round that succeeded under this store, by any process; false when none
	// is known to have. Every replica reads it, so the age it yields does not
	// depend on which process is the leader or on how long any has run.
	SourceRefreshSuccessAt(context.Context) (time.Time, bool, error)
	Close() error
}

type phaseTwoOwnershipRuntime interface {
	RegisterWorker(context.Context, ownership.WorkerRegistration) error
	TryAcquireControlLeader(context.Context, time.Time, time.Duration) (bool, error)
	PublishAssignments(context.Context, []execution.QueryGroupIdentity, time.Time) error
	AssignedQueryGroups(context.Context, []execution.QueryGroupIdentity) ([]execution.QueryGroupIdentity, error)
	MaintainControlLeader(context.Context, time.Duration, time.Duration) error
	OpenQueryGroup(
		context.Context,
		execution.QueryGroupIdentity,
		time.Time,
		time.Duration,
	) (phaseTwoQueryGroupRuntime, error)
	// LoadOpened reads back what the Query Groups a round has just opened
	// keep in the ownership store - their read holds - once their leases are
	// held and before their Runners take a Slot, in one batched read for the
	// round. Required: read before the lease, a hold could be a lease TTL
	// old, and nothing read it again until the new owner's own write
	// conflicted.
	LoadOpened(context.Context, []execution.QueryGroupIdentity)
	Close() error
}

// phaseTwoRebalanceSource is what an ownership runtime that plans rebalance
// rounds reports about its latest one. Optional rather than on the interface
// above: a runtime that plans nothing publishes no plan, which the fleet
// keeps apart from a plan that moves nothing.
type phaseTwoRebalanceSource interface {
	LastRebalance() *fleet.RebalanceFacts
	// LastAssignmentScope is the same round's census of the content scope
	// on the records it settled. On the same interface as the plan, so a
	// runtime that plans reports both or is a compile error.
	LastAssignmentScope() *fleet.AssignmentScopeFacts
	// LastAssignmentSweep is the latest sweep of retired records, success or
	// failure, for the same reason.
	LastAssignmentSweep() *fleet.AssignmentSweepFacts
}

// rebalanceFleetFacts is the latest rebalance planning round on this
// process, for the fleet snapshot; nil on a follower and on a runtime that
// does not plan.
func (bundle *phaseTwoWorkerBundle) rebalanceFleetFacts() *fleet.RebalanceFacts {
	if bundle == nil {
		return nil
	}
	source, ok := bundle.dependencies.Ownership.(phaseTwoRebalanceSource)
	if !ok {
		return nil
	}
	return source.LastRebalance()
}

// assignmentScopeFleetFacts is the latest reconcile round's content-scope
// census on this process, for the fleet snapshot; nil on a follower and on a
// runtime that does not plan.
func (bundle *phaseTwoWorkerBundle) assignmentScopeFleetFacts() *fleet.AssignmentScopeFacts {
	if bundle == nil {
		return nil
	}
	source, ok := bundle.dependencies.Ownership.(phaseTwoRebalanceSource)
	if !ok {
		return nil
	}
	return source.LastAssignmentScope()
}

// assignmentSweepFleetFacts is the latest sweep of retired Assignment records
// on this process, for the fleet snapshot; nil on a follower.
func (bundle *phaseTwoWorkerBundle) assignmentSweepFleetFacts() *fleet.AssignmentSweepFacts {
	if bundle == nil {
		return nil
	}
	source, ok := bundle.dependencies.Ownership.(phaseTwoRebalanceSource)
	if !ok {
		return nil
	}
	return source.LastAssignmentSweep()
}

// phaseTwoLeaderRoundSource is what an ownership runtime that runs the
// control leader's reconcile rounds reports about them. Its own interface,
// so a runtime that runs none -- every test fake among them -- needs no
// stub.
type phaseTwoLeaderRoundSource interface {
	LastLeaderRound() *fleet.LeaderRoundFacts
	LeaderRoundStats() metric.LeaderRoundStats
}

var _ phaseTwoLeaderRoundSource = (*productionPhaseTwoOwnership)(nil)

// leaderRoundFleetFacts is the latest leader round on this process, for the
// fleet snapshot; nil on a follower.
func (bundle *phaseTwoWorkerBundle) leaderRoundFleetFacts() *fleet.LeaderRoundFacts {
	if bundle == nil {
		return nil
	}
	source, ok := bundle.dependencies.Ownership.(phaseTwoLeaderRoundSource)
	if !ok {
		return nil
	}
	return source.LastLeaderRound()
}

// leaderRoundStats is what the leader round collector scrapes; empty on a
// runtime that runs no rounds.
func (bundle *phaseTwoWorkerBundle) leaderRoundStats() metric.LeaderRoundStats {
	if bundle == nil {
		return metric.LeaderRoundStats{}
	}
	source, ok := bundle.dependencies.Ownership.(phaseTwoLeaderRoundSource)
	if !ok {
		return metric.LeaderRoundStats{}
	}
	return source.LeaderRoundStats()
}

type phaseTwoQueryGroupLifecycle struct {
	runner phaseTwoQueryGroupRuntime
	cancel context.CancelFunc
	done   chan struct{}
	// inflight counts this Query Group's Slots that are executing, and the
	// other work acting on its lease (enterOwnedWork), and idle is closed
	// when it falls back to zero; both under the bundle's lock. Each is
	// counted in the same step that finds its lifecycle current, so once a
	// lifecycle is detached nothing can start on it, and a release that
	// waits on idle waits for exactly what is already running.
	inflight int
	idle     chan struct{}
}

// declinedQueryGroup is a Query Group this replica will not run while the
// execution of it that hung here has not returned (design 02 section 6.5).
type declinedQueryGroup struct {
	lifecycle *phaseTwoQueryGroupLifecycle
	stage     string
	deadline  time.Time
	since     time.Time
}

type phaseTwoQueryGroupRuntime interface {
	RunOne(context.Context) (execution.SlotExecutionResult, bool, error)
	RunOneAdmitted(context.Context, scheduler.ExecutionAdmission) (execution.SlotExecutionResult, bool, bool, error)
	NextReadyAt() time.Time
	// DueBound is what the round that has just returned concluded about when
	// this Query Group is worth running again. It is read at the same point
	// NextReadyAt is, because the two answers belong to the same moment.
	DueBound() scheduler.RunnerDueBound
	// NextDeadline is when the next Slot this Query Group would run stops
	// being worth running; zero when nothing is known. It is required rather
	// than optional: as an optional interface the production Runtime never
	// implemented it, every production Query Group was queued with no
	// deadline, and the deadline order shipped twice without ever having run.
	// A method the compiler does not demand is a method one implementation
	// silently lacks.
	NextDeadline() time.Time
	MaintainLease(context.Context, time.Duration, time.Duration) error
	Release(context.Context) error
	// DeclineHung claims the execution in flight as hung when it is past
	// its own deadline by more than grace and has not begun to commit, and
	// returns the stage it is stuck in (execution.SlotStages) and the
	// deadline it overran; a claimed execution writes nothing more. The
	// deadline is the execution's own, set as it runs - a normal Slot's
	// query deadline, a recovery's from its arrival - and is the only one
	// the watchdog judges by: the dispatcher's deadline is the frozen Slot's
	// first attempt's, minutes gone for a replay. Required, like
	// NextDeadline.
	DeclineHung(now time.Time, grace time.Duration) (string, time.Time, bool)
	// ExecutionDeadline is the deadline the execution in flight runs under,
	// as DeclineHung judges it, without claiming anything; zero when none is
	// in flight or it has none yet. The liveness probe reads it beside the
	// dispatcher's deadline.
	ExecutionDeadline() time.Time
}

type phaseTwoWorkerBundleDependencies struct {
	Config config.Config
	// Flights is the process's query coordinator, whose budget the bundle
	// sizes from the work it owns (schedulerShaper); nil keeps the startup
	// shape.
	Flights    *scheduler.FlightCoordinator
	Health     *phaseTwoApplicationHealth
	Control    phaseTwoControlRuntime
	Ownership  phaseTwoOwnershipRuntime
	Recorder   *metric.Recorder
	Observer   observability.Observer
	TargetFlow *observability.TargetFlow
	// FleetAPI serves the object facts once this runtime is open, and
	// RefreshOpenAlerts reads the consumer's open alert publication into the
	// process copy; run once at start and then on its own cadence.
	RefreshOpenAlerts func(context.Context)
	// RunOpenAlerts owns subscription/reconciliation for the current protocol.
	RunOpenAlerts    func(context.Context) error
	RunEffectiveTime func(context.Context)
	// RunAbsentClose is the control leader's difference against the
	// strategies that no longer exist. Same shape as RunEffectiveTime: one
	// goroutine for the process, which decides per round whether it is the
	// leader.
	RunAbsentClose func(context.Context)
	// RunTargetScopeClose decides the closes of alerts whose target left
	// the strategy's scope, from what this replica's own admission step
	// turned away. Every replica runs its own.
	RunTargetScopeClose func(context.Context)
	// Lookback is the late-data lookback on the query path, nil when this
	// process does not run it. The bundle runs its rechecks and drops the
	// samples of a Query Group the moment it stops owning it.
	Lookback  *lookback.Engine
	ReadHolds *productionReadHolds
	// RefreshPlatformSettings reads the platform's dynamic configuration
	// into the process copy and brings what evaluates by it up to date; run
	// once at start and then once a minute.
	RefreshPlatformSettings func(context.Context)
	// PublishFleet writes this replica's contribution to them.
	FleetAPI http.Handler
	// PublicSurfaceRestricted is whether the listener's public surface is
	// restricted: asked for by the configuration and the CLI came up.
	PublicSurfaceRestricted bool
	// ControlStream serves decision-016's view stream over the HTTP
	// listener (gRPC over h2c), and StreamIdentity is what this process
	// writes into its registration for it. ViewStreamStats is the Leader's
	// account of the stream for the metrics and the page. All absent for a
	// runtime without the stream.
	ControlStream   http.Handler
	StreamIdentity  viewStreamIdentity
	ViewStreamStats func() viewstream.Stats
	// ViewClient is this Worker's side of the stream, run with the
	// maintenance goroutines and read by nothing in execution.
	ViewClient   *viewstream.Client
	PublishFleet func(context.Context)
	// MeasureStores takes a census of what the stores this process writes to
	// hold, by key family (package storecensus), while it is the Control
	// Leader, and forgets the last one when it is not: one census for the
	// deployment, not one per replica.
	MeasureStores func(ctx context.Context, leading bool)
	// ApplyObservationWindows makes the windows opened through that API take
	// effect on this replica. It runs on the reconcile tick rather than on a
	// timer of its own, so opening a window is bounded by a cadence the
	// deployment already reasons about.
	ApplyObservationWindows func(context.Context)
	// ProbeControlRedis issues one zero-payload round trip. Every other command
	// carries server work or a payload, so their latency cannot be separated
	// into transport cost and work; this one has neither and therefore measures
	// the floor directly. It is recorded by the same command hook, so it needs
	// no metric of its own. A nil probe disables the measurement.
	ProbeControlRedis func(context.Context) error
	// ActivationBlocked is the Control Leader's last cutover as far as the
	// Query Groups it held back go; nil where there is no repository.
	ActivationBlocked func() controlplane.ActivationBlockedReading
	// ActivationSkipped is the timelines the Control Leader's activation
	// read left out because they did not decode; nil leaves the row without
	// them.
	ActivationSkipped func() controlplane.SkippedTimelinesReading
	CloseResources    func(context.Context) error
	Now               func() time.Time

	// ActivationHeader is the Control Leader's standing on a missing
	// activation header; nil where there is no repository.
	ActivationHeader func() controlplane.ActivationHeaderReading
}

type phaseTwoWorkerBundle struct {
	runtimeConfig *observability.RuntimeConfigFacts
	dependencies  phaseTwoWorkerBundleDependencies
	// liveness is what the liveness probe judges; see phaseTwoLiveness.
	liveness *phaseTwoLiveness
	// workerPorts is what this runtime actually handed the coordinator.
	//
	// Kept so a test can read it. A port that may be nil is a port a production
	// runtime can be missing while every fake has it, and the only sign is the
	// capability quietly not happening -- which is how a deadline port went two
	// releases implemented by every test double and by nothing that shipped.
	// The check is a scan of the whole struct rather than of one field, so the
	// next optional port is covered without anybody remembering to add it.
	workerPorts worker.Ports
	// applied and capacity feed the heartbeat's acknowledgement and load.
	// Both are set at assembly and may be nil, in which case the heartbeat
	// carries neither, which readers take as unknown.
	applied  func() uint64
	capacity func() *fleet.Capacity

	registrationMu sync.Mutex
	mu             sync.RWMutex
	queryGroups    []execution.QueryGroupIdentity
	assigned       map[execution.QueryGroupIdentity]struct{}
	// assignmentRead is set by the first Assignment applied; see updateReadiness.
	assignmentRead bool
	runners        map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
	// scheduledRunners is the owned Runner set in Query Group order, rebuilt
	// only after that set changes. The dispatcher reads it on every pass of its
	// loop, and a pass advances at most one Slot. runnersRevision names the set
	// itself, so the dispatcher can tell that it has already walked this one.
	scheduledRunners []phaseTwoScheduledRunner
	runnersRevision  uint64
	started          bool
	draining         bool
	closed           bool
	controlLeader    bool
	controlRunning   bool
	controlEpoch     uint64
	controlDegraded  bool
	// candidacy is the class this process stands for the Control Leader
	// lease in (phaseTwoControlCandidacy), fixed at assembly; empty is no
	// candidate. candidacyReady is set once Start has read the control facts
	// it can, and with draining and closed it decides whether the process
	// contends: one still starting, or already stopping, does not.
	candidacy      string
	candidacyReady bool
	// handback is a fallback Leader's memory of the preferred processes it
	// has seen ready, round to round. The reconcile loop alone touches it.
	handback controlHandbackWatch
	// controlFactsSeen says a control round has told this replica which Query
	// Groups exist at least once. Until then the replica registers as
	// starting rather than ready and reports not ready: it is up, its
	// diagnostics answer, and it retries the read every tick, but it must not
	// be given a share of Query Groups it cannot name. A round that could not
	// read the set (QueryGroupsRetained) does not set it.
	controlFactsSeen      bool
	controlSourceKind     observability.SourceKind
	controlReason         observability.ReasonCode
	lastControlRecoveryAt time.Time
	// outputSinkReady is whether the output sink is open. A replica whose
	// sink is not open registers as starting and reports not ready, for the
	// same reason as one that has not read the control facts: it is up and
	// answers, but must not be handed Query Groups whose decisions it cannot
	// publish. Set by outputSinkChanged from the sink's own record; a bundle
	// assembled without a lazy sink (the tests' fakes open theirs before the
	// bundle exists) keeps the initial true.
	outputSinkReady bool
	// controlSource is the state of the control source refresh as this
	// process reports it, read at scrape and at publish rather than on a
	// transition. See runtime_phase_two_control_source.go.
	controlSource controlSourceState
	// activation is the standing of bringing the fleet's activation to the
	// current publication, kept apart from controlSource because the two
	// clocks disagree in exactly the case that matters: a source that
	// publishes every round while the activation fails to follow it reads
	// fresh on the source clock and stuck on this one.
	activation activationStanding
	// rotation is the dispatcher's own view of whether it is still getting
	// round everything it owns, published once per rotation rather than read
	// out of the walk.
	rotation       atomic.Pointer[fleet.Rotation]
	maintenanceCtx context.Context
	cancelMaintain context.CancelFunc
	cancelControl  context.CancelFunc
	// cancelViewClient stops this Worker's side of the view stream alone,
	// under the maintenance context that Shutdown cancels with the rest.
	cancelViewClient context.CancelFunc
	maintenanceWG    sync.WaitGroup
	inflightWG       sync.WaitGroup
	// flightReleased carries the Query Groups whose flight, held by a
	// supplement or maintenance, turned a Slot away, once the hold ends
	// (scheduler.FlightCoordinator.OnTurnedAwayReleased): the dispatcher runs
	// the Slot then instead of at its next turn.
	flightReleased chan execution.QueryGroupIdentity
	shutdownOnce   sync.Once
	shutdownErr    error
	drainOnce      sync.Once
	drainErr       error
	// shutdownDrainFacts is the stop's wait, for the lifecycle record and the
	// tests; under mu.
	shutdownDrainFacts *observability.SlotDrainFacts
	slotsRunning       int
	// declined is the Query Groups whose execution hung here past its
	// deadline and grace; under mu. Each holds one execution slot until its
	// execution returns, when it is lifted, so it never has more entries
	// than the dispatcher has slots.
	declined map[execution.QueryGroupIdentity]*declinedQueryGroup
	// unreadableTimelines is the owned Query Groups whose last round that
	// asked for a Slot met a Schedule timeline that does not decode; under mu.
	// The registration names them so a leader reads them again
	// (ownership.WorkerRegistration.UnreadableTimelines). An entry goes with
	// the first later round of its Query Group that runs a Slot, and with its
	// Runner, so the set is never larger than the owned set.
	unreadableTimelines map[execution.QueryGroupIdentity]struct{}
	// dependencyDegraded is set while a control or Ownership Store call fails
	// transiently. dependencyFailureSeq counts those failures so a reconcile
	// pass only clears the flag when no new failure happened during the pass.
	dependencyDegraded   bool
	dependencyFailureSeq uint64
	// dueIndex is this replica's view of when each owned Query Group can next
	// become due. It lives on the bundle rather than on the dispatcher so a
	// reader outside the dispatch loop can ask it which objects are overdue.
	dueIndex *phaseTwoDueIndex
	// addressDiffers is the latest records whose address is not their
	// host's, as the fleet snapshot publishes them.
	addressDiffers *fleet.AddressDiffersSamples
	// shaper sizes the query budget, execution slots and queues from the
	// work this bundle owns; nil keeps the startup shape.
	shaper *schedulerShaper
	// workerCosts is the census reading the heartbeat carries and the
	// observation line's retained reservation is sized by; kept so a test
	// can drive it through the production assembly.
	workerCosts *workerCostSource
}

type phaseTwoScheduledRunner struct {
	queuedAt   time.Time
	queryGroup execution.QueryGroupIdentity
	lifecycle  *phaseTwoQueryGroupLifecycle
	// predictedDue is what the due index said when this Query Group was offered
	// a place, and dueEpoch is the publication the prediction was made under.
	// Both are carried on the entry rather than looked up again on return: the
	// index is what the prediction has to be checked against, and reading it a
	// second time would be checking the answer against itself.
	predictedDue bool
	dueEpoch     uint64
	// predictedHeldFor is how much longer the index's bound would have kept this
	// Query Group back at the moment it was consulted. It is only meaningful
	// beside a prediction of not-due, and it is what separates a bound that is
	// late from a clock that crossed the boundary between the prediction and the
	// verdict -- two situations the count of violations alone reports the same.
	predictedHeldFor time.Duration
	// place is the place in the queue this dispatch was given: the deadline it
	// was ordered by, its sequence among equals and the cohort its turn-aways
	// count under. It travels with the dispatch and comes back with the
	// result, so a Slot that returns unfinished is requeued in the place it
	// had rather than a new one. Empty until the Runner is first queued.
	place phaseTwoQueuePlace
}

// phaseTwoQueuePlace is one Slot's standing in the queue, given when the
// Slot is first offered a place and kept for as long as that Slot is the one
// the Runner is due for. A Slot that came back deferred -- for readiness,
// admission or backoff -- is the same work with the same deadline, and
// giving it a fresh sequence would move it behind everything queued while it
// was out; reading no deadline for it would rank it behind every Slot that
// has one, which is where the short cohort's five-second Slot went.
type phaseTwoQueuePlace struct {
	deadline time.Time
	sequence uint64
	cohort   string
}

type phaseTwoScheduledResult struct {
	scheduled       phaseTwoScheduledRunner
	attempted       bool
	admissionDenied bool
	// outcome is what the round's Runner returned; read for the words that
	// say what the round met (noteTimelineReading).
	outcome execution.SlotExecutionResult
	// ran says the Runner was actually entered. A dispatch that found the
	// context cancelled or the lifecycle replaced returns without running, and
	// the bound such a return would carry belongs to the previous round.
	ran bool
	err error
}

type phaseTwoQueuedRunner struct {
	scheduled phaseTwoScheduledRunner
	readyAt   time.Time
	// deadline is when the next Slot stops being worth running, as the
	// Runner knew it when queued; zero when it did not. sequence is the
	// order of queueing, the tie-break among equal deadlines so the ready
	// queue stays first-come among equals. cohort labels what this entry's
	// turn-aways are counted under.
	deadline time.Time
	sequence uint64
	cohort   string
}

// deadlineBefore orders two queued Runners by when their work expires:
// the earlier deadline first, a known deadline before an unknown one, and
// among equals the one that was ready first, then the one queued first,
// then by identity. It is the one order both queues and the choice between
// them use; a second order anywhere is where the short cohort lost its
// place before.
func deadlineBefore(left, right phaseTwoQueuedRunner) bool {
	switch {
	case left.deadline.IsZero() != right.deadline.IsZero():
		return !left.deadline.IsZero()
	case !left.deadline.Equal(right.deadline):
		return left.deadline.Before(right.deadline)
	case !left.readyAt.Equal(right.readyAt):
		return left.readyAt.Before(right.readyAt)
	case left.sequence != right.sequence:
		return left.sequence < right.sequence
	}
	return left.scheduled.queryGroup < right.scheduled.queryGroup
}

type phaseTwoRunnerGeneration struct {
	lifecycle  *phaseTwoQueryGroupLifecycle
	generation uint64
}

type phaseTwoRunnerDispatcher struct {
	// executing counts Runner invocations between dispatch and return. Worker
	// goroutines only add to it; the dispatcher loop is the sole publisher, so
	// no worker ever waits on the observer to record its own return.
	executing atomic.Int64
	bundle    *phaseTwoWorkerBundle
	fanout    int

	jobs    chan phaseTwoScheduledRunner
	results chan phaseTwoScheduledResult
	workers sync.WaitGroup

	// dueIndex predicts which Query Groups are worth dispatching. controlVersion
	// serves the latest activation header sample; it is read off the dispatch
	// loop because a loop waiting on Redis stops collecting results, and results
	// are what free the queue. A nil sample means no reading has come back yet,
	// which is not the same as a failed reading and is not treated as one.
	dueIndex        *phaseTwoDueIndex
	controlVersion  atomic.Pointer[phaseTwoControlVersionSample]
	versionStop     chan struct{}
	versionStopOnce sync.Once
	versionPolling  sync.WaitGroup

	generation uint64
	cursor     execution.QueryGroupIdentity
	lastQueued map[execution.QueryGroupIdentity]phaseTwoRunnerGeneration
	queued     map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
	active     map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
	// turnedAway is each Query Group whose round a supplement or maintenance
	// turned away from its flight, waiting for that hold to end; released
	// each whose hold ended before its round's return was handled. Both are
	// cleared at every generation, whose walk offers any round left.
	turnedAway map[execution.QueryGroupIdentity]phaseTwoScheduledRunner
	released   map[execution.QueryGroupIdentity]struct{}
	normal     []phaseTwoQueuedRunner
	delayed    []phaseTwoQueuedRunner
	// queueSequence numbers queue entries in order of queueing; normalDirty
	// says the ready queue took entries since it was last ordered.
	queueSequence uint64
	normalDirty   bool

	// preferDelayed alternates the two queues only when neither candidate
	// carries a deadline; whenever one does, the deadline decides.
	preferDelayed  bool
	oneShot        bool
	oneShotTargets map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle

	// A pass of the dispatcher loop advances at most one Slot, and it used to
	// walk every owned Query Group on every pass. Within one generation a
	// Runner may be queued at most once, so the walk is carried across passes
	// instead: each Runner is considered once per generation, and a pass that
	// finds the walk finished does no work at all.
	walkGeneration uint64
	walkRunners    uint64
	walkIndex      int
	walked         int
	// walkSize is how many Query Groups the walk in progress has to reach. It is
	// kept because the owned set changes underneath the walk, and "did the last
	// rotation finish" has to be asked against the set that rotation was walking
	// -- asking it against the new set calls a finished rotation truncated every
	// time the set grows, which is every rebalance and every added strategy.
	walkSize int
	// rotation records how the walk over the owned set is going. One generation
	// is one rotation: the walk considers every owned Query Group exactly once
	// before it finishes, so "did everything get a turn, and how long did that
	// take" is answerable here and nowhere else. Without it the page can say an
	// object is degraded but not whether an object is simply never reached.
	rotation phaseTwoRotationFacts
	// auditCursor and auditGeneration carry the audit round described on
	// claimAuditDispatch: one parked Query Group per generation is dispatched
	// anyway, rotating by name so every object is checked in turn.
	auditCursor     execution.QueryGroupIdentity
	auditGeneration uint64
	// prunedRunners names the owned set the queues were last cleaned against.
	// A lifecycle only stops being current when that set changes, so cleaning
	// again for an unchanged set walks every queued entry and every remembered
	// generation to decide nothing.
	prunedRunners uint64
}

// phaseTwoRotationFacts is what one Worker can say about its own rotation.
//
// The counts are cumulative so a rate can be taken over any window; the last
// duration is an instant because a rotation either finished or it did not, and
// averaging a finished one with an unfinished one would describe neither.
type phaseTwoRotationFacts struct {
	startedAt time.Time
	// completed counts rotations that reached every owned Query Group. A
	// rotation that is cut short by a full ready queue does not count: it left
	// part of the owned set unoffered, which is the condition worth seeing.
	completed uint64
	// truncated counts rotations that stopped before reaching everyone.
	truncated uint64
	// offered and queued count Query Groups: how many the walk reached, and how
	// many of those got a place. deferred counts turn-aways, so one Query Group
	// stuck behind a full queue adds one per pass -- the repetition is what
	// distinguishes an object running late from one nothing will reach.
	offered  uint64
	queued   uint64
	deferred uint64
	// deferredQueueFull and deferredNotBetter split that total by which of the
	// two branches produced it, because they are different conditions with
	// different answers and one number cannot carry both.
	//
	// queueFull is the ready queue having no place: the walk stops there, so
	// everything behind this Query Group is unoffered this pass too, and more
	// room would change it.
	//
	// notBetter is the recovery queue being full of Query Groups that are all
	// due sooner than this one. The code's own comment at that branch says it:
	// "a decision, not a lack of room". More room does not change it, and a
	// deployment where it dominates is not short of queue.
	//
	// Summed into one count, the page told a reader to grow a queue for a
	// number that is mostly the second.
	deferredQueueFull uint64
	deferredNotBetter uint64
	// lastSeconds is how long the most recent completed rotation took. It is
	// the number an alert on "the deployment stopped covering its objects"
	// needs, and until now nothing measured it.
	lastSeconds float64
	// generation is the rotation the counts above belong to, so a reader can
	// tell a stalled walk from a slow one.
	generation uint64
}

// flightReleasedNotices is how many released holds wait for the dispatcher
// at once. A notice that does not fit is dropped: the Slot it was for runs
// at its next turn, one scheduler tick later, as it did before notices.
const flightReleasedNotices = 256

// noticeFlightReleased tells the dispatcher a Query Group's flight is free
// again after it turned a Slot away. It never blocks.
func (bundle *phaseTwoWorkerBundle) noticeFlightReleased(queryGroup execution.QueryGroupIdentity) {
	select {
	case bundle.flightReleased <- queryGroup:
	default:
	}
}

func newPhaseTwoWorkerBundle(dependencies phaseTwoWorkerBundleDependencies) (*phaseTwoWorkerBundle, error) {
	if dependencies.Health == nil || dependencies.Control == nil || dependencies.Ownership == nil ||
		dependencies.Observer == nil || dependencies.Now == nil {
		return nil, errors.New("phase-two worker requires complete lifecycle dependencies")
	}
	if err := dependencies.Config.Validate(); err != nil {
		return nil, err
	}
	bundle := &phaseTwoWorkerBundle{dependencies: dependencies, outputSinkReady: true,
		candidacy:      phaseTwoControlCandidacy(dependencies.Config),
		flightReleased: make(chan execution.QueryGroupIdentity, flightReleasedNotices),
		assigned:       make(map[execution.QueryGroupIdentity]struct{}),
		runners:        make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		liveness:       newPhaseTwoLiveness(dependencies.Now, dependencies.Recorder)}
	if dependencies.Flights != nil {
		bundle.shaper = newSchedulerShaper(dependencies.Flights, dependencies.Config, dependencies.Now)
	}
	if dependencies.Recorder != nil {
		dependencies.Recorder.SetOwnedQueryGroups(0)
		dependencies.Recorder.SetControlSourceSource(bundle.controlSourceStats)
		dependencies.Recorder.SetLeaderRoundSource(bundle.leaderRoundStats)
		dependencies.Recorder.SetCatalogCompositionSource(bundle.catalogComposition)
		if dependencies.Lookback != nil {
			dependencies.Recorder.SetLookbackSource(func() lookback.Stats { return productionLookbackStats(dependencies.Lookback, dependencies.ReadHolds) })
		}
	}
	return bundle, nil
}

func (bundle *phaseTwoWorkerBundle) Start(ctx context.Context) error {
	if bundle == nil || ctx == nil {
		return errors.New("phase-two worker requires an initialized bundle and context")
	}
	bundle.mu.Lock()
	if bundle.started {
		bundle.mu.Unlock()
		return errors.New("phase-two worker is already started")
	}
	bundle.started = true
	bundle.maintenanceCtx, bundle.cancelMaintain = context.WithCancel(context.Background())
	bundle.mu.Unlock()
	bundle.observe(ctx, observability.ComponentRuntime, observability.Stage(observability.StageStartup), observability.ResultStarted, nil)
	bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentRuntime, Stage: observability.StageConfigLoaded,
		Result: observability.ResultSuccess, RuntimeConfig: bundle.runtimeConfig,
	})
	if err := bundle.registerAtStartup(ctx, ownership.WorkerStarting); err != nil {
		return err
	}
	// The starting read is a follower's, whatever this process will become.
	// Only once it is made does the process count as having finished
	// starting and contend for the Control Leader lease, so a replica still
	// opening its dependencies never leads (006, section 2.1). It contends
	// here rather than on the first reconcile tick, so a deployment nobody
	// leads has a Leader as soon as its first process is up. A process that
	// takes the lease starts from its own first round instead, exactly as
	// before: the follower's read only stood in until it knew which it was.
	followerResult, followerErr := bundle.dependencies.Control.LoadActive(ctx)
	if followerErr != nil && ctx.Err() != nil {
		return followerErr
	}
	bundle.mu.Lock()
	bundle.candidacyReady = true
	bundle.mu.Unlock()
	bundle.publishControlCandidacy()
	leader, err := bundle.tryAcquireControlLeader(ctx)
	if err != nil {
		// Not knowing whether this replica holds the lease is not the same as
		// not holding it, but the work is: go on with what was read above and
		// ask again on the reconcile tick. Ending startup here turned a store
		// that was still loading its dataset into a crash loop, and a crash
		// loop is the one state from which this replica can do nothing at all.
		bundle.dependencies.Recorder.RecordControlFactUnavailable("leader_lease", "read_failed")
		leader = false
	} else {
		bundle.dependencies.Recorder.RecordControlFactRead("leader_lease")
	}
	controlResult, err := followerResult, followerErr
	if leader {
		controlResult, err = bundle.dependencies.Control.InitialRefresh(ctx)
	}
	controlFactsAvailable := true
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		// Every failed read of the starting control facts lands here, not only
		// the one shape a previous version named. A replica that cannot read
		// them knows less than one that can; it does not know less than a
		// replica that is not running, which is what exiting made it. It waits
		// under a named reason, registers as starting rather than ready so no
		// Query Group is assigned to it meanwhile, and joins on the first tick
		// that reads the facts.
		controlFactsAvailable = false
		controlResult = degradedControlFacts(err)
	}
	if leader || controlResult.Status == phaseTwoControlDegradedLastGood {
		if err := bundle.applyControlRefresh(ctx, controlResult); err != nil {
			return err
		}
	} else if err := bundle.applyFollowerControlLoad(ctx, controlResult); err != nil {
		return err
	}
	bundle.readPersistedSourceSuccess(ctx)
	if err := bundle.registerAtStartup(ctx, ownership.WorkerReady); err != nil {
		return err
	}
	if !controlFactsAvailable {
		// Nothing to publish and nothing to be assigned: this replica does not
		// know which Query Groups exist. The assignment steps are skipped
		// rather than run against an empty set, which would read as "there is
		// no work" and, on the Control Leader, publish that answer to everyone
		// else. The reconcile tick does both once the facts are readable.
		bundle.startMaintenance()
		bundle.updateReadiness()
		return nil
	}
	bundle.mu.RLock()
	queryGroups := append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
	bundle.mu.RUnlock()
	if leader {
		if err := bundle.dependencies.Ownership.PublishAssignments(ctx, queryGroups, bundle.dependencies.Now()); err != nil {
			if !errors.Is(err, ownership.ErrStaleFence) {
				return bundle.startWithoutAssignment(ctx, fmt.Errorf("phase-two publish Assignment: %w", err))
			}
			bundle.markControlFollower(err)
		}
	}
	if !bundle.dependencies.Config.HasRole(roles.Worker) {
		bundle.startMaintenance()
		bundle.updateReadiness()
		return nil
	}
	assigned, err := bundle.dependencies.Ownership.AssignedQueryGroups(ctx, queryGroups)
	if err != nil {
		return bundle.startWithoutAssignment(ctx, fmt.Errorf("phase-two read Assignment: %w", err))
	}
	if err := bundle.applyAssignment(ctx, assigned); err != nil {
		return err
	}
	bundle.startMaintenance()
	bundle.updateReadiness()
	return nil
}

// registerAtStartup is the reconcile tick's handling of a registration write,
// applied at startup: a write that fails is the renewal loop's to retry, and
// the replica carries on unregistered, which is how it is given no Query
// Group meanwhile. Ending startup here turned a Redis failover that crossed a
// rollout into a crash loop.
func (bundle *phaseTwoWorkerBundle) registerAtStartup(ctx context.Context, readiness ownership.AssignmentReadiness) error {
	err := bundle.register(ctx, readiness)
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case isPhaseTwoInvariantError(err):
		return err
	}
	bundle.observeRegistrationRenewal(observability.ResultFailed, err)
	bundle.markControlDependencyDegraded()
	return nil
}

// startWithoutAssignment ends startup without an Assignment when the
// Assignment could not be published or read, as the reconcile tick does: the
// replica is not ready while it holds none, and the first tick publishes and
// reads it again. Invariant violations still end startup.
func (bundle *phaseTwoWorkerBundle) startWithoutAssignment(ctx context.Context, err error) error {
	if err := bundle.scopeControlError(ctx, observability.ComponentOwnership, observability.StageAssignmentAcquired, err); err != nil {
		return err
	}
	bundle.startMaintenance()
	bundle.updateReadiness()
	return nil
}

// degradedControlFacts is the health fact a replica runs under when a control
// round could not read the facts it runs from, at startup or on a tick. The
// active set is retained, never replaced by an empty one. The reason names
// which fact was unreadable, because "degraded" alone sends a reader to look at
// all of them and the two that happen here have different answers: a missing
// activation is written back by the Control Leader, an unreadable snapshot is
// retried.
func degradedControlFacts(err error) phaseTwoControlRefreshResult {
	// Each reason is one the readiness page keeps as it is; contract_retryable
	// is not, and folded to _other there, so the one word a reader had was
	// gone at the page.
	reason := phaseTwoControlDependencyReason
	switch {
	case errors.Is(err, controlplane.ErrActivationUnavailable):
		reason = observability.ReasonCode(contract.ReasonActivationMissing)
	case errors.Is(err, controlplane.ErrSnapshotUnavailable):
		reason = observability.ReasonCode(contract.ReasonSnapshotUnavailable)
	}
	return phaseTwoControlRefreshResult{
		Status: phaseTwoControlDegradedLastGood, QueryGroupsRetained: true,
		SourceKind: observability.SourceKindCompiledSnapshot,
		ReasonCode: reason, Cause: err,
	}
}

// probeControlRedis measures the round trip floor on the reconcile cadence. A
// failure is left to the paths that actually depend on Redis: this call exists
// to be timed, and failing it here would report the same outage twice.
func (bundle *phaseTwoWorkerBundle) probeControlRedis(ctx context.Context) {
	if bundle == nil || bundle.dependencies.ProbeControlRedis == nil || ctx.Err() != nil {
		return
	}
	_ = bundle.dependencies.ProbeControlRedis(ctx)
}

func (bundle *phaseTwoWorkerBundle) Run(ctx context.Context) error {
	if err := bundle.Start(ctx); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
		defer cancel()
		return errors.Join(err, bundle.Shutdown(shutdownCtx))
	}
	if !bundle.dependencies.Config.HasRole(roles.Worker) {
		return bundle.runControlRole(ctx)
	}
	scheduleTicker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Scheduler.TickInterval.Duration())
	refreshTicker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Control.RefreshInterval.Duration())
	reconcileTicker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Control.ReconcileInterval.Duration())
	defer scheduleTicker.Stop()
	defer refreshTicker.Stop()
	defer reconcileTicker.Stop()
	// Slots execute under a context of their own that the stop signal does
	// not reach: a stop ends dispatch and leaves the Slots already running to
	// finish within the drain deadline, which alone cancels them (design 02
	// §6.6). The values of ctx are kept.
	executionCtx, cancelExecution := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelExecution()
	dispatchStop := make(chan struct{})
	schedulerWake := make(chan struct{}, 1)
	schedulerDone := make(chan error, 1)
	ticksDone := make(chan struct{})
	// Control refresh and reconcile can wait on dependencies. Keep normal
	// generations advancing independently, with at most one pending wake.
	go func() {
		defer close(ticksDone)
		for {
			select {
			case <-dispatchStop:
				return
			case <-scheduleTicker.C:
				select {
				case schedulerWake <- struct{}{}:
				default:
				}
			}
		}
	}()
	go func() {
		schedulerDone <- bundle.runScheduler(executionCtx, schedulerWake, dispatchStop, false)
	}()

	var runErr error
	schedulerRunning := true
	bundle.liveness.start(livenessLoopControl, controlLoopStallBound)
	for runErr == nil {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case <-refreshTicker.C:
			began := bundle.liveness.clock()
			runErr = bundle.refreshAndReconcile(ctx, true)
			bundle.liveness.turned(livenessLoopControl, began)
		case <-reconcileTicker.C:
			began := bundle.liveness.clock()
			bundle.probeControlRedis(ctx)
			// Applied before the reconcile rather than after it: a failure here
			// must not decide whether the pipeline reconciles, and the applier
			// swallows its own errors for the same reason.
			if bundle.dependencies.ApplyObservationWindows != nil {
				bundle.dependencies.ApplyObservationWindows(ctx)
			}
			runErr = bundle.refreshAndReconcile(ctx, false)
			bundle.liveness.turned(livenessLoopControl, began)
		case schedulerErr := <-schedulerDone:
			schedulerRunning = false
			if schedulerErr == nil {
				schedulerErr = errPhaseTwoWorkerStopped
			}
			runErr = schedulerErr
		}
	}
	// The drain deadline (design 02 §6.6) runs from here. DRAINING is
	// registered first, so the leader stops placing work here while the last
	// Slots finish. The releases that follow get a shutdown timeout of their
	// own: a drain that used its whole deadline must not leave every lease to
	// expire by TTL, which would hold each Query Group's next owner out for
	// the rest of it.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancelDrain()
	drainErr := bundle.beginDraining(drainCtx)
	close(dispatchStop)
	<-ticksDone
	if schedulerRunning {
		schedulerErr := bundle.drainSlots(drainCtx, schedulerDone, cancelExecution)
		if schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
			runErr = errors.Join(runErr, schedulerErr)
		}
	}
	cancelExecution()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	shutdownErr := errors.Join(drainErr, bundle.Shutdown(shutdownCtx))
	if errors.Is(runErr, context.Canceled) && ctx.Err() != nil {
		runErr = nil
	}
	return errors.Join(runErr, shutdownErr)
}

// oneShotTargetsUnqueued says no target of a one-shot run is waiting in a
// queue or running: what is left will not run in this generation.
func (dispatcher *phaseTwoRunnerDispatcher) oneShotTargetsUnqueued() bool {
	for queryGroup, lifecycle := range dispatcher.oneShotTargets {
		if dispatcher.queued[queryGroup] == lifecycle || dispatcher.active[queryGroup] == lifecycle {
			return false
		}
	}
	return true
}

// oneShotGenerationLimit bounds the ticks a one-shot run issues itself: a
// target still not dispatched after this many is one the dispatcher never
// reaches, which the run reports rather than waits on.
const oneShotGenerationLimit = 16

func (bundle *phaseTwoWorkerBundle) runScheduledOnce(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	return bundle.runScheduler(ctx, wake, nil, true)
}

// runScheduler runs the dispatcher until ctx ends or stop is closed. Slots
// execute under ctx. A closed stop ends dispatch without touching ctx: no
// Slot starts after it, the Slots already running go on under ctx, and the
// call returns context.Canceled once they have all returned. That is how a
// stop lets the commit boundaries in flight finish (design 02 §6.6); a nil
// stop is never closed.
func (bundle *phaseTwoWorkerBundle) runScheduler(
	ctx context.Context,
	wake <-chan struct{},
	stop <-chan struct{},
	oneShot bool,
) error {
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return errPhaseTwoWorkerDraining
	}
	// Shutdown takes the same lock before waiting, so this sentinel prevents
	// WaitGroup Add/Wait races while the dispatcher can still run QG work.
	bundle.inflightWG.Add(1)
	bundle.mu.Unlock()
	defer bundle.inflightWG.Done()

	dispatcher := newPhaseTwoRunnerDispatcher(bundle, oneShot)
	dispatcher.start(ctx)
	defer dispatcher.stop()
	return dispatcher.run(ctx, wake, stop)
}

func newPhaseTwoRunnerDispatcher(
	bundle *phaseTwoWorkerBundle,
	oneShot bool,
) *phaseTwoRunnerDispatcher {
	schedulerConfig := bundle.dependencies.Config.PhaseTwo.Scheduler
	fanout := phaseTwoDispatcherFanout(bundle)
	// A fresh dispatcher starts with no bounds. Entries carried over from a
	// previous one would be answers about a schedule nobody has looked at since,
	// and starting empty reproduces exactly what the first tick does today.
	dueIndex := bundle.ensureDueIndex()
	dueIndex.Clear()
	return &phaseTwoRunnerDispatcher{
		bundle: bundle, fanout: fanout, dueIndex: dueIndex,
		jobs: make(chan phaseTwoScheduledRunner), results: make(chan phaseTwoScheduledResult, schedulerConfig.ReadyQueueCapacity),
		lastQueued:    make(map[execution.QueryGroupIdentity]phaseTwoRunnerGeneration),
		queued:        make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		active:        make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		turnedAway:    make(map[execution.QueryGroupIdentity]phaseTwoScheduledRunner),
		released:      make(map[execution.QueryGroupIdentity]struct{}),
		preferDelayed: true, oneShot: oneShot,
	}
}

// phaseTwoControlVersionSample is one reading of the activation header. A
// failed or absent reading is published too, and published as unknown: the
// index then falls back to the behaviour of a deployment with no index, which
// is a load this deployment already carries, rather than holding Slots back on
// bounds it can no longer vouch for.
type phaseTwoControlVersionSample struct {
	tag   string
	known bool
}

// pollControlVersion keeps one activation header reading current, off the
// dispatch loop.
//
// Each reading is bounded by the tick it belongs to. A reading that cannot
// finish inside its own cadence is abandoned and published as unknown, so a
// slow control plane degrades into the no-index behaviour instead of leaving
// the dispatcher acting on an anchor it cannot re-confirm.
func (dispatcher *phaseTwoRunnerDispatcher) pollControlVersion(ctx context.Context, interval time.Duration) {
	defer dispatcher.versionPolling.Done()
	control := dispatcher.bundle.dependencies.Control
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		readCtx, cancel := context.WithTimeout(ctx, interval)
		tag, known, err := control.ControlVersion(readCtx)
		cancel()
		if err != nil {
			tag, known = "", false
		}
		dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: tag, known: known})
		select {
		case <-ctx.Done():
			return
		case <-dispatcher.versionStop:
			return
		case <-ticker.C:
		}
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) start(ctx context.Context) {
	if dispatcher.bundle.dependencies.Control != nil {
		interval := dispatcher.bundle.dependencies.Config.PhaseTwo.Scheduler.TickInterval.Duration()
		if interval <= 0 {
			interval = time.Second
		}
		dispatcher.versionStop = make(chan struct{})
		dispatcher.versionPolling.Add(1)
		go dispatcher.pollControlVersion(ctx, interval)
	}
	// One goroutine per slot, each running its Query Group to completion. The
	// alternative this replaces spawned a goroutine per dispatch instead, which
	// made the fanout whatever the loop could reach; the dispatcher active map
	// still admits each owned Query Group once, so the bound here is on how
	// many distinct Query Groups may be in flight, not on repeated waiters.
	for range dispatcher.fanout {
		dispatcher.workers.Add(1)
		go func() {
			defer dispatcher.workers.Done()
			for scheduled := range dispatcher.jobs {
				dispatcher.executeScheduled(ctx, scheduled)
			}
		}()
	}
}

// dispatchQueueFacts reports how long a round waited for an execution slot.
//
// The queued-at stamp is only taken for objects that were already being
// observed when they were queued, so an object that entered the queue before
// its window was opened carries the zero time. Measuring from that overflows
// the duration and saturates, which is how a window opened on a busy object
// reported a wait of 9223372036 seconds -- a number nothing marks as wrong, and
// that no reader can tell from a real one without recognising MaxInt64.
//
// Both fields are left out instead. There is no wait to report for a round that
// was already queued before anyone was watching, and saying nothing is the
// honest form of that; both are omitempty, so the record simply has no wait.
func dispatchQueueFacts(queuedAt, at time.Time) observability.TargetFlowFacts {
	facts := observability.TargetFlowFacts{Decision: "execution_slot_acquired"}
	if queuedAt.IsZero() || at.Before(queuedAt) {
		return facts
	}
	facts.QueuedAtMS = queuedAt.UnixMilli()
	facts.QueueWaitNS = at.Sub(queuedAt).Nanoseconds()
	return facts
}

func (dispatcher *phaseTwoRunnerDispatcher) executeScheduled(ctx context.Context, scheduled phaseTwoScheduledRunner) {
	dispatcher.changeExecuting(1)
	result := phaseTwoScheduledResult{scheduled: scheduled}
	runCtx := dispatcher.bundle.dependencies.TargetFlow.Context(ctx, string(scheduled.queryGroup))
	if observability.TargetFlowEnabled(runCtx) {
		runCtx = observability.ContextWithTraceFields(runCtx, observability.TraceFields{QueryGroupKey: string(scheduled.queryGroup)})
		observability.EmitTargetFlow(runCtx, "runner_dispatch", observability.TraceFields{},
			dispatchQueueFacts(scheduled.queuedAt, time.Now()))
	}
	func() {
		defer dispatcher.changeExecuting(-1)
		token := dispatcher.bundle.liveness.executionStarted(scheduled.place.deadline, scheduled.lifecycle.runner.ExecutionDeadline)
		defer dispatcher.bundle.liveness.executionReturned(token)
		if ctx.Err() == nil && dispatcher.bundle.enterScheduledRunner(scheduled) {
			result.ran = true
			defer dispatcher.bundle.shaper.executionStarted()()
			func() {
				defer dispatcher.bundle.exitScheduledRunner(scheduled.lifecycle)
				defer startSlotTiming(runCtx, dispatcher.bundle.dependencies.Observer, observability.StageRunnerCompleted, time.Now)()
				result.outcome, result.attempted, result.admissionDenied, result.err =
					scheduled.lifecycle.runner.RunOneAdmitted(runCtx, func(execution.Operation) (func(), bool) {
						// A positive F is an emergency guard; zero has no Runner gate.
						// P/R belong only to actual Query admission in Access.
						return func() {}, ctx.Err() == nil
					})
			}()
		}
	}()
	observability.EmitTargetFlow(runCtx, "runner_return", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "returned", Attempted: result.attempted})
	dispatcher.results <- result
}

func (dispatcher *phaseTwoRunnerDispatcher) stop() {
	if dispatcher.versionStop != nil {
		dispatcher.versionStopOnce.Do(func() { close(dispatcher.versionStop) })
		dispatcher.versionPolling.Wait()
	}
	close(dispatcher.jobs)
	dispatcher.workers.Wait()
}

func (dispatcher *phaseTwoRunnerDispatcher) run(ctx context.Context, wake <-chan struct{}, stop <-chan struct{}) error {
	var canceled error
	ctxDone := ctx.Done()
	liveness := dispatcher.bundle.liveness
	if !dispatcher.oneShot {
		liveness.setSlots(dispatcher.fanout)
		liveness.start(livenessLoopDispatch, dispatchLoopStallBound)
	}
	for {
		began := liveness.clock()
		dispatcher.observeOccupancy(ctx)
		if canceled != nil && len(dispatcher.active) == 0 {
			return canceled
		}
		if dispatcher.oneShot && dispatcher.generation > 0 && len(dispatcher.oneShotTargets) == 0 &&
			len(dispatcher.active) == 0 {
			return nil
		}
		if dispatcher.oneShot && dispatcher.generation > 0 && len(dispatcher.active) == 0 && len(dispatcher.normal) == 0 &&
			dispatcher.walkGeneration == dispatcher.generation && dispatcher.walked >= dispatcher.walkSize &&
			dispatcher.oneShotTargetsUnqueued() {
			// Targets left neither queued nor running were turned away this
			// generation, by a full queue that evicted them as it does in
			// production, and production gives them their turn at the next
			// tick: the one-shot run issues that tick itself.
			if dispatcher.generation >= oneShotGenerationLimit {
				return errors.New("phase-two one-shot dispatch: a target was never dispatched")
			}
			dispatcher.beginGeneration()
			continue
		}

		// Consume completed work before admitting another normal item. This
		// makes a newly ready recovery visible to the fairness decision.
		select {
		case result := <-dispatcher.results:
			dispatcher.handleResult(ctx, result, canceled == nil)
			continue
		default:
		}

		runners, revision := dispatcher.bundle.snapshotScheduledRunners()
		dispatcher.dropStaleQueued(revision)
		dispatcher.fillQueues(runners, revision)
		now := dispatcher.bundle.schedulerNow()
		dispatcher.sortDelayed()
		dispatcher.sortNormal()
		dispatcher.observeOccupancy(ctx)
		delayedDue := len(dispatcher.delayed) > 0 && !dispatcher.delayed[0].readyAt.After(now)
		normalReady := len(dispatcher.normal) > 0
		// Earliest deadline first, across both queues. The ready queue is
		// ordered, so its head is its earliest; the recovery queue is ordered
		// by readiness, so its due prefix is scanned for the earliest. A Slot
		// that became ready at :10 and expires at :25 used to wait behind the
		// minute's thousand ready Slots that expire at :55, alternating one
		// for one with them, and started at :24.
		delayedIndex := -1
		if canceled == nil && delayedDue {
			delayedIndex = dispatcher.earliestDueDelayedIndex(now)
		}
		selectDelayed, selectNormal := false, false
		switch {
		case canceled != nil:
		case delayedIndex >= 0 && normalReady:
			candidate, head := dispatcher.delayed[delayedIndex], dispatcher.normal[0]
			if candidate.deadline.IsZero() && head.deadline.IsZero() {
				selectDelayed = dispatcher.preferDelayed
			} else {
				selectDelayed = deadlineBefore(candidate, head)
			}
			selectNormal = !selectDelayed
		case delayedIndex >= 0:
			selectDelayed = true
		case normalReady:
			selectNormal = true
		}

		// The execution slots in force: the dispatcher runs the ceiling's
		// worth of goroutines and offers work only while fewer than the
		// derived bound are executing. A bound lowered under what is running
		// takes nothing back; the next offer waits for a result.
		slotsFull := dispatcher.slotsFull()
		if slotsFull {
			selectDelayed, selectNormal = false, false
		}
		var dispatch chan phaseTwoScheduledRunner
		var scheduled phaseTwoScheduledRunner
		if selectDelayed {
			dispatch = dispatcher.jobs
			scheduled = dispatcher.delayed[delayedIndex].scheduled
		} else if selectNormal {
			dispatch = dispatcher.jobs
			scheduled = dispatcher.normal[0].scheduled
		}

		var retryTimer *time.Timer
		var retryReady <-chan time.Time
		// With every slot in use a due retry cannot run either: the next
		// result wakes the loop, and a timer here would spin it.
		if canceled == nil && dispatch == nil && !slotsFull && len(dispatcher.delayed) > 0 {
			delay := dispatcher.delayed[0].readyAt.Sub(now)
			if delay < 0 {
				delay = 0
			}
			retryTimer = time.NewTimer(delay)
			retryReady = retryTimer.C
		}

		// The turn is the work up to the wait; the wait itself is idle.
		if !dispatcher.oneShot {
			liveness.turned(livenessLoopDispatch, began)
		}
		select {
		case dispatch <- scheduled:
			dispatcher.markDispatched(scheduled, selectDelayed, delayedIndex, delayedDue)
		case result := <-dispatcher.results:
			dispatcher.handleResult(ctx, result, canceled == nil)
		case <-wake:
			if canceled == nil {
				dispatcher.beginGeneration()
			}
		case queryGroup := <-dispatcher.bundle.flightReleased:
			if canceled == nil {
				dispatcher.flightReleased(queryGroup)
			}
		case <-retryReady:
		case <-ctxDone:
			canceled = ctx.Err()
			ctxDone = nil
		case <-stop:
			if canceled == nil {
				canceled = context.Canceled
			}
			stop = nil
		}
		if retryTimer != nil && !retryTimer.Stop() {
			select {
			case <-retryTimer.C:
			default:
			}
		}
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) beginGeneration() {
	if dispatcher.auditGeneration != dispatcher.generation {
		// The generation that is ending found no parked Query Group after the
		// cursor, so the rotation has reached the end of the owned set and starts
		// again. Without this the audit would stop at the last name and never
		// come back round.
		dispatcher.auditCursor = ""
	}
	dispatcher.generation++
	clear(dispatcher.turnedAway)
	clear(dispatcher.released)
	// One header comparison for the whole replica, once per tick. It is here
	// rather than per Query Group because the header is global: a publication
	// stamps it regardless of which Query Groups it touched, so one reading
	// answers the question for all of them.
	if sample := dispatcher.controlVersion.Load(); sample != nil {
		dispatcher.dueIndex.ObserveControlVersion(sample.tag, sample.known, dispatcher.bundle.schedulerNow())
	}
	dispatcher.bundle.dependencies.Recorder.SetDueIndexEntries(dispatcher.dueIndex.Len())
	if dispatcher.oneShot && dispatcher.generation == 1 {
		dispatcher.oneShotTargets = make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle)
		runners, _ := dispatcher.bundle.snapshotScheduledRunners()
		for _, scheduled := range runners {
			dispatcher.oneShotTargets[scheduled.queryGroup] = scheduled.lifecycle
		}
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) fillQueues(runners []phaseTwoScheduledRunner, revision uint64) {
	if dispatcher.generation == 0 {
		return
	}
	if len(runners) == 0 {
		return
	}
	// A rotation publishes when it ends, and a walk held up by a full queue
	// never ends -- which is the one condition this measurement exists to
	// surface. Without this, a stuck deployment keeps showing the last finished
	// rotation's numbers and reads as healthy for as long as it stays stuck. A
	// pass that turned anyone away therefore publishes on its way out, so
	// "deferred climbing while completed stays flat" is readable while it is
	// happening. It is once per pass rather than per deferral: the pass is the
	// dispatcher loop's own unit, and the walk itself must stay clean.
	// One reading of the clock for the whole pass. The walk carries across
	// passes, so a later pass takes its own reading; within one pass every Query
	// Group must be judged against the same instant or the order of the walk
	// would decide who counts as due.
	now := dispatcher.bundle.schedulerNow()
	deferredAtEntry := dispatcher.rotation.deferred
	defer func() {
		if dispatcher.rotation.deferred != deferredAtEntry {
			dispatcher.publishRotation()
		}
	}()
	// A new generation, or a changed owned set, starts a walk from the rotation
	// cursor. The walk then carries across passes, so a Query Group is looked
	// at once per generation rather than once per pass.
	if dispatcher.walkGeneration != dispatcher.generation || dispatcher.walkRunners != revision {
		dispatcher.walkGeneration, dispatcher.walkRunners = dispatcher.generation, revision
		dispatcher.walkIndex = sort.Search(len(runners), func(index int) bool {
			return runners[index].queryGroup > dispatcher.cursor
		})
		if dispatcher.walked > 0 && dispatcher.walked < dispatcher.walkSize {
			// The previous rotation is being replaced before it reached
			// everyone. That is not the same as finishing, and counting it as
			// one would hide a deployment that never completes a pass.
			//
			// Measured against the set that rotation was walking, not the one
			// replacing it. Against the new set, a rotation that reached all ten
			// of its objects counts as truncated the moment an eleventh arrives,
			// and a rotation that stopped at five of twenty counts as complete
			// the moment the set shrinks to four -- wrong in both directions, on
			// the ordinary event of the owned set changing.
			dispatcher.rotation.truncated++
			dispatcher.publishRotation()
		}
		dispatcher.walked = 0
		dispatcher.walkSize = len(runners)
		dispatcher.rotation.startedAt = time.Now()
		dispatcher.rotation.generation = dispatcher.generation
	}
	// advance moves the walk past the Query Group it just looked at. A Query
	// Group turned away because a queue is full does not advance: it keeps its
	// place and the next pass offers it again, once a dispatch has made room.
	// Consuming its turn instead would leave it waiting for the next
	// generation, and in a one-shot run it would never be offered at all.
	advance := func() {
		dispatcher.walkIndex++
		dispatcher.walked++
		dispatcher.rotation.offered++
		if dispatcher.walked == len(runners) {
			// Everyone in the owned set has now been considered once. This is
			// the only point at which a full rotation is known to have happened,
			// so it is where its duration comes from.
			dispatcher.rotation.completed++
			dispatcher.rotation.lastSeconds = time.Since(dispatcher.rotation.startedAt).Seconds()
			dispatcher.publishRotation()
		}
	}
	for dispatcher.walked < len(runners) {
		scheduled := runners[dispatcher.walkIndex%len(runners)]
		if dispatcher.active[scheduled.queryGroup] != nil || dispatcher.queued[scheduled.queryGroup] != nil {
			// The object was reached and lost its turn to a round that has not
			// been collected yet. This is not one of the three skip reasons:
			// those say the object was not supposed to run. Counted here rather
			// than inferred later, because this branch is the only place that
			// knows the turn was taken, and which of the two holders took it
			// decides where an operator looks.
			holder := "queued"
			if dispatcher.active[scheduled.queryGroup] != nil {
				holder = "active"
			}
			dispatcher.bundle.dependencies.Recorder.RecordDispatchCrowdedOut(holder)
			advance()
			continue
		}
		last := dispatcher.lastQueued[scheduled.queryGroup]
		if last.lifecycle == scheduled.lifecycle && last.generation == dispatcher.generation {
			advance()
			continue
		}
		// What the index would decide, recorded but not yet acted on. Reading it
		// here rather than anywhere else is deliberate: this is the point at
		// which a Query Group is either offered a place or not, so a prediction
		// taken here is the prediction that suppressing dispatch would act on,
		// and the comparison against what the round actually finds is a
		// comparison of the real decision rather than a re-derivation of it.
		var parkedOnBackoff bool
		scheduled.predictedDue, parkedOnBackoff, scheduled.dueEpoch, scheduled.predictedHeldFor =
			dispatcher.dueIndex.Predict(scheduled.queryGroup, scheduled.lifecycle, now)
		if !scheduled.predictedDue && (parkedOnBackoff || !dispatcher.claimAuditDispatch(scheduled.queryGroup)) {
			// Neither queue. A parked Query Group in the ready queue would be
			// dispatched to be told what the bound already said, and one in the
			// recovery queue would be worse: that queue is sized for recovery and
			// alternates with the ready queue, so parking the idle majority there
			// would have them competing with actual recovery for its places and
			// re-sorting them on every pass of the loop.
			//
			// The walk still advances. The Query Group was considered and a
			// decision was made about it, which is what a turn is; not advancing
			// would stop the walk on the first parked object and never reach the
			// ones behind it.
			dispatcher.dueIndex.RecordSkip(dispatcher.bundle.dependencies.Recorder, parkedOnBackoff, scheduled.queryGroup)
			advance()
			continue
		}
		if dispatcher.bundle.dependencies.TargetFlow.Selected(string(scheduled.queryGroup)) {
			scheduled.queuedAt = time.Now()
		}
		readyAt := scheduled.lifecycle.runner.NextReadyAt()
		queued := dispatcher.queueEntry(scheduled, readyAt)
		recorder := dispatcher.bundle.dependencies.Recorder
		// The recovery queue may not turn a Query Group away while it holds fewer
		// entries than this Worker owns.
		//
		// Its capacity is derived from the CPU budget - query permits times a
		// depth per permit - and what fills it is the owned Query Group count,
		// which that derivation knows nothing about. On an 8 CPU container it
		// lands at 1024 while a Worker owns 1,059, so the queue cannot hold the
		// set it exists to hold, and the shortfall is whatever the arithmetic
		// happened to produce.
		//
		// It is not hypothetical. Sampled every 7 seconds - so the readings are
		// not locked to the 60 second evaluation cadence the way a 15 or 60
		// second interval is - the recovery queue fills to 991, 997, 1007 and
		// 1024 once per cycle, the bound exactly, and the queue-full counter
		// advances while it is there. The container is at 16% of its CPU and a
		// third of its memory throughout.
		//
		// A place is a reference and one Query Group can hold at most one, so the
		// owned set is the natural bound, at about 80 bytes an entry. The derived
		// number stays as the floor rather than being replaced: it is what the
		// permit budget wants kept fed.
		//
		// The turn-away this removes is written as a decision - this Query Group
		// is not earlier than the one it would displace, so it does not belong in
		// the queue at all - and that is true, but the decision only has to be
		// made because the queue cannot hold everyone. A queue that fits the
		// owned set never displaces anyone, so the ordering question does not
		// arise and the O(n) scan that answers it never runs.
		//
		// What that also does, and what was not said when it was made: the branch
		// below becomes unreachable rather than rare, by the same argument spelled
		// out for the ready queue further down. Its counter is therefore an
		// invariant guard and not a measure of queue pressure. Non-zero has
		// exactly two meanings - dropStaleQueued did not run before this walk, or
		// this floor was removed - and both are defects in this file rather than
		// load. A metric whose zero is structural needs to say so; "zero is the
		// success case" is the weaker statement and invites someone to go looking
		// for the load that would make it move.
		//
		// The ready queue has the same defect and does not have the same fix
		// applied. This is a known open item, written here rather than in a list
		// because this is the line that would change.
		//
		// It was first left alone on the argument that it is sized never to be
		// the binding side of its pair with ActiveExecutions, and that
		// production agreed: scheduler_ready_runners peaked at 164 against 1024.
		// That argument was made from a gauge and the gauge cannot see this one.
		// The queue fills and the walk stops inside a single sampling interval,
		// so every sample reads zero - including every sample taken at an
		// interval chosen specifically not to divide the evaluation cadence.
		//
		// The counter sees it, and the two replicas make the control for each
		// other. On the one owning 1,146 Query Groups the ready queue's turn-away
		// advances once per evaluation cadence, in steps of 57 to 82, and does
		// not decay: 155, 212, 266, 325, 407 over seven minutes. On the one
		// owning 929 it is zero for the whole window. The split is the capacity,
		// 1024, exactly.
		//
		// A full ready queue is also the harsher of the two, because the walk
		// stops rather than moving on: every Query Group behind that one goes
		// unoffered for the rest of the pass, including ones the recovery queue
		// had room for. What bounds the harm is that one dispatch frees one place
		// and the walk resumes inside the same generation.
		//
		// The same floor is NOT the fix here, which is worth stating because it
		// is the obvious next move and it was proposed before anyone checked what
		// it costs.
		//
		// The walk is driven as dropStaleQueued then fillQueues, and
		// dropStaleQueued removes every queued entry that is no longer a current
		// scheduled runner. So len(normal) <= len(runners) always holds, and a
		// floor at len(runners) makes this branch require that every owned Query
		// Group is already queued - at which point the Query Group being
		// considered is queued too and the crowded-out branch above catches it
		// first. The branch becomes unreachable, not rare. Driven through that
		// real order, including across an owned-set shrink, the walk completes
		// every pass and all three deferral counters stay at zero.
		//
		// Unreachable here does not mean one dead branch. A full ready queue is
		// the only reason this walk ever stops, so removing it also retires
		// rotation.truncated, the cross-pass walk carry-over, the publish-on-the-
		// way-out that exists because a stuck walk never publishes, and both
		// deferral counters. That is a subsystem, against a measured 0.508 turn-
		// aways a second that are self-correcting inside the generation - one
		// dispatch frees one place and the walk resumes - with truncated at zero
		// throughout, meaning every rotation still covered every owned object.
		//
		// The change that would remove the measured harm without any of that is
		// smaller and is not this: stop returning here and keep walking, without
		// consuming this Query Group's turn, so the objects behind it still get
		// offered - including the ones the recovery queue has room for, which is
		// the actual cost of stopping. The comment below argues against scanning
		// past on the grounds that it re-reads the owned set every pass; that is
		// a cost argument, and the cost has since been measured at nothing - this
		// walk does not appear anywhere in a 60 second CPU profile.
		//
		// This does not scale by itself. sortDelayed re-sorts the whole recovery
		// queue on every pass of the dispatcher loop; at this size it does not
		// appear anywhere in a 60 second CPU profile, and at a Worker owning tens
		// of thousands it would. The queue becoming the owned set is what makes
		// that the next thing to measure, not a reason to keep it too small.
		limits := dispatcher.bundle.schedulerShape()
		recoveryCapacity := max(limits.RecoveryQueueCapacity, len(runners))
		if readyAt.IsZero() {
			if len(dispatcher.normal) >= limits.ReadyQueueCapacity {
				// A full ready queue gives up the entry that expires last for an
				// arrival that expires earlier: at the minute's tide the queue
				// is a thousand Slots expiring at :55, and a short-period Slot
				// arriving behind them must not wait for a place. Otherwise the
				// walk stops here rather than scanning past this Query Group
				// for one the recovery queue could still take. Scanning past it
				// would read the whole owned set again on every pass for as long
				// as the ready queue stays full, which is the per-pass sweep this
				// walk exists to remove, and the queue stays full at exactly the
				// load where that sweep costs the most. What is behind this Query
				// Group is deferred, not dropped: one dispatch frees one place,
				// and the walk resumes from here within the same generation.
				latest := dispatcher.latestNormalIndex()
				if latest < 0 || queued.deadline.IsZero() || !deadlineBefore(queued, dispatcher.normal[latest]) {
					dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(scheduled.queryGroup), observability.TargetFlowFacts{Decision: "normal_queue_full"})
					recorder.RecordDispatchTurnaway("normal_queue_full", queued.cohort)
					if latest >= 0 {
						dispatcher.observeTurnaway("normal_queue_full", queued, dispatcher.normal[latest], readyQueueVerdict(queued, dispatcher.normal[latest]), len(dispatcher.normal), limits.ReadyQueueCapacity)
					}
					dispatcher.rotation.deferred++
					dispatcher.rotation.deferredQueueFull++
					dispatcher.dueIndex.MarkHeldBack(scheduled.queryGroup)
					return
				}
				evicted := dispatcher.normal[latest]
				dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(evicted.scheduled.queryGroup), observability.TargetFlowFacts{Decision: "normal_queue_evicted"})
				recorder.RecordDispatchTurnaway("normal_queue_evicted", evicted.cohort)
				dispatcher.observeTurnaway("normal_queue_evicted", evicted, queued, "displaced", len(dispatcher.normal), limits.ReadyQueueCapacity)
				dispatcher.rotation.deferred++
				dispatcher.rotation.deferredQueueFull++
				dispatcher.dueIndex.MarkHeldBack(evicted.scheduled.queryGroup)
				if dispatcher.queued[evicted.scheduled.queryGroup] == evicted.scheduled.lifecycle {
					delete(dispatcher.queued, evicted.scheduled.queryGroup)
				}
				dispatcher.normal[latest] = queued
			} else {
				dispatcher.normal = append(dispatcher.normal, queued)
			}
			dispatcher.normalDirty = true
			dispatcher.bundle.shaper.recordReady()
		} else {
			if len(dispatcher.delayed) >= recoveryCapacity {
				latest := dispatcher.latestDelayedIndex()
				if latest < 0 || !delayedBefore(queued, dispatcher.delayed[latest]) {
					// Not better than the Query Group it would displace, so it
					// does not belong in the queue at all. That is a decision,
					// not a lack of room, and the walk moves on.
					dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(scheduled.queryGroup), observability.TargetFlowFacts{Decision: "delayed_queue_full", ReadyAtMS: diagnosticTimeMS(readyAt)})
					recorder.RecordDispatchTurnaway("delayed_not_better", queued.cohort)
					if latest >= 0 {
						dispatcher.observeTurnaway("delayed_not_better", queued, dispatcher.delayed[latest], "ready_at_not_before", len(dispatcher.delayed), recoveryCapacity)
					}
					dispatcher.rotation.deferred++
					dispatcher.rotation.deferredNotBetter++
					dispatcher.dueIndex.MarkHeldBack(scheduled.queryGroup)
					advance()
					continue
				}
				evicted := dispatcher.delayed[latest].scheduled
				dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(evicted.queryGroup), observability.TargetFlowFacts{Decision: "delayed_queue_evicted"})
				recorder.RecordDispatchTurnaway("delayed_evicted", dispatcher.delayed[latest].cohort)
				dispatcher.observeTurnaway("delayed_evicted", dispatcher.delayed[latest], queued, "ready_at_displaced", len(dispatcher.delayed), recoveryCapacity)
				dispatcher.dueIndex.MarkHeldBack(evicted.queryGroup)
				if dispatcher.queued[evicted.queryGroup] == evicted.lifecycle {
					delete(dispatcher.queued, evicted.queryGroup)
				}
				dispatcher.delayed[latest] = queued
			} else {
				dispatcher.delayed = append(dispatcher.delayed, queued)
			}
		}
		dispatcher.bundle.dependencies.TargetFlow.Record("runner_queued", string(scheduled.queryGroup), observability.TargetFlowFacts{Decision: "queued", QueuedAtMS: scheduled.queuedAt.UnixMilli(), ReadyAtMS: diagnosticTimeMS(readyAt)})
		dispatcher.queued[scheduled.queryGroup] = scheduled.lifecycle
		dispatcher.lastQueued[scheduled.queryGroup] = phaseTwoRunnerGeneration{
			lifecycle: scheduled.lifecycle, generation: dispatcher.generation,
		}
		dispatcher.cursor = scheduled.queryGroup
		dispatcher.rotation.queued++
		advance()
	}
}

// claimAuditDispatch decides whether this parked Query Group is dispatched
// anyway, to keep checking the prediction that is now being acted on.
//
// Suppressing dispatch on the index's word would otherwise retire the one
// counter that can prove the index wrong. A Query Group predicted not due is
// never run, so it never reports what it actually found, so the violation
// series can no longer be produced at all - and it would read as a permanent
// zero, which is exactly what success looks like. The falsifier would go silent
// at the moment it started to matter.
//
// So one parked Query Group per generation is dispatched regardless, rotating
// by name so the whole owned set is checked in turn. It is one extra round per
// generation against the hundreds the index removes, and it is the only thing
// that keeps "predicted not due, actually due" a reading rather than an
// assumption.
//
// Only objects parked on a schedule bound are audited. The claim being
// falsified is about the schedule - that no publication can pull a Slot in
// front of the bound - and a Runner's own backoff is not a claim about the
// schedule at all, so auditing one would prove nothing. It would also cost
// something real: an audited backoff joins the recovery queue and holds a place
// there until its instant arrives, and over a long backoff that is one place
// per generation taken from the queue recovery actually needs.
func (dispatcher *phaseTwoRunnerDispatcher) claimAuditDispatch(
	queryGroup execution.QueryGroupIdentity,
) bool {
	if dispatcher.auditGeneration == dispatcher.generation || queryGroup <= dispatcher.auditCursor {
		return false
	}
	dispatcher.auditGeneration, dispatcher.auditCursor = dispatcher.generation, queryGroup
	return true
}

// rotationFacts is the last rotation snapshot the dispatcher published.
//
// The dispatcher publishes rather than the reader reaching in: the walk is the
// hot loop, and a reader taking its lock would slow the thing it is measuring.
// Nil means no rotation has finished yet, which is a real answer on a replica
// that has just started.
func (bundle *phaseTwoWorkerBundle) rotationFacts() *fleet.Rotation {
	if bundle == nil {
		return nil
	}
	return bundle.rotation.Load()
}

// publishRotation copies the counts out for readers. It runs at most once per
// rotation, so it is nowhere near the per-object path.
func (dispatcher *phaseTwoRunnerDispatcher) publishRotation() {
	dispatcher.bundle.rotation.Store(rotationView(dispatcher.rotation))
}

// rotationView carries the walk's counts onto the view the page reads.
//
// Named and lifted out of the store call for the reason the worker's coverage
// handoff is a named function: it is a hand-written copy between two structs
// that have to hold the same numbers, and a count added on one side and
// forgotten on the other reaches the page as a zero -- which reads as "this
// never happened" rather than "nobody carried it". Inline, nothing could
// execute the translation on its own, so nothing could see the gap.
func rotationView(facts phaseTwoRotationFacts) *fleet.Rotation {
	return &fleet.Rotation{
		Completed: facts.completed, Truncated: facts.truncated,
		Offered: facts.offered, Queued: facts.queued, Deferred: facts.deferred,
		DeferredQueueFull: facts.deferredQueueFull, DeferredNotBetter: facts.deferredNotBetter,
		LastSeconds: facts.lastSeconds,
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) markDispatched(
	scheduled phaseTwoScheduledRunner,
	delayed bool,
	delayedIndex int,
	delayedDue bool,
) {
	delete(dispatcher.queued, scheduled.queryGroup)
	dispatcher.active[scheduled.queryGroup] = scheduled.lifecycle
	if delayed {
		dispatcher.delayed = append(dispatcher.delayed[:delayedIndex], dispatcher.delayed[delayedIndex+1:]...)
		dispatcher.preferDelayed = false
		return
	}
	dispatcher.normal = dispatcher.normal[1:]
	if delayedDue {
		dispatcher.preferDelayed = true
	}
}

// earliestDueDelayedIndex is the entry of the recovery queue's due prefix
// with the earliest deadline, under the same order the ready queue keeps.
// The queue is sorted by readiness, so the prefix is contiguous.
func (dispatcher *phaseTwoRunnerDispatcher) earliestDueDelayedIndex(now time.Time) int {
	best := -1
	for index := range dispatcher.delayed {
		if dispatcher.delayed[index].readyAt.After(now) {
			break
		}
		if best < 0 || deadlineBefore(dispatcher.delayed[index], dispatcher.delayed[best]) {
			best = index
		}
	}
	return best
}

// sortNormal orders the ready queue by deadline when entries were added
// since it was last ordered; removing the head keeps the order.
func (dispatcher *phaseTwoRunnerDispatcher) sortNormal() {
	if !dispatcher.normalDirty {
		return
	}
	sort.SliceStable(dispatcher.normal, func(left, right int) bool {
		return deadlineBefore(dispatcher.normal[left], dispatcher.normal[right])
	})
	dispatcher.normalDirty = false
}

// latestNormalIndex is the ready queue entry that expires last, the one a
// full queue gives up for an arrival that expires earlier.
func (dispatcher *phaseTwoRunnerDispatcher) latestNormalIndex() int {
	latest := -1
	for index := range dispatcher.normal {
		if latest < 0 || deadlineBefore(dispatcher.normal[latest], dispatcher.normal[index]) {
			latest = index
		}
	}
	return latest
}

func (dispatcher *phaseTwoRunnerDispatcher) handleResult(
	ctx context.Context,
	result phaseTwoScheduledResult,
	requeue bool,
) {
	scheduled := result.scheduled
	if dispatcher.active[scheduled.queryGroup] == scheduled.lifecycle {
		delete(dispatcher.active, scheduled.queryGroup)
	}
	if result.ran {
		dispatcher.bundle.noteTimelineReading(scheduled.queryGroup, result.attempted, result.outcome)
	}
	dispatcher.recordDueBound(result)
	if dispatcher.oneShotTargets[scheduled.queryGroup] == scheduled.lifecycle {
		delete(dispatcher.oneShotTargets, scheduled.queryGroup)
	}
	if result.err != nil {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(result.err, ownership.ErrStaleFence) || errors.Is(result.err, ownership.ErrNotDesired) ||
			errors.Is(result.err, scheduler.ErrSlotOwnershipChanged) {
			dispatcher.bundle.stopLostQueryGroup(scheduled.queryGroup, scheduled.lifecycle, result.err)
			return
		}
		if !result.attempted {
			var outcome observability.Result = observability.ResultFailed
			reason := observability.ReasonInternalUnknown
			yielded := heldByYield(result.err)
			switch yielded {
			case scheduler.FlightHeldBySupplement:
				outcome, reason = observability.ResultSkipped, observability.ReasonHeldBySupplement
			case scheduler.FlightHeldByMaintenance:
				outcome, reason = observability.ResultSkipped, observability.ReasonHeldByMaintenance
			}
			observeRuntime(ctx, dispatcher.bundle.dependencies.Observer, observability.Observation{
				Component: observability.ComponentScheduler, Stage: observability.StageScheduleDue,
				Result: outcome, ReasonCode: reason,
				Direction: observability.DirectionInternal,
				Trace:     observability.TraceFields{QueryGroupKey: string(scheduled.queryGroup)},
				Err:       result.err,
			})
			if yielded != "" && requeue {
				dispatcher.turnedAwayByHold(scheduled)
				return
			}
		}
	}
	if result.admissionDenied {
		// A scheduler tick received while this attempt was still active cannot
		// authorize an immediate denied retry. Only a later tick may requeue it.
		dispatcher.lastQueued[scheduled.queryGroup] = phaseTwoRunnerGeneration{
			lifecycle: scheduled.lifecycle, generation: dispatcher.generation,
		}
		return
	}
	if !requeue || !dispatcher.bundle.isCurrentScheduledRunner(scheduled) {
		return
	}
	if dispatcher.bundle.dependencies.TargetFlow.Selected(string(scheduled.queryGroup)) {
		scheduled.queuedAt = time.Now()
	}
	readyAt := scheduled.lifecycle.runner.NextReadyAt()
	if readyAt.IsZero() {
		// A Runner that returns is not queued here. The walk is what hands out
		// places in the ready queue, and a Runner that just returned is always
		// the first one able to ask for one: it asks before the walk resumes,
		// and before every Query Group the walk has not reached. Granting it
		// there gives the queue to whichever Query Groups finish fastest and
		// starves the tail of the rotation.
		//
		// Nothing is lost by waiting. A Runner the walk passed over as active
		// spent that generation's turn without being offered a place, and the
		// next generation's walk offers it again when its turn comes round;
		// waiting for your next turn is what a rotation means. A one-shot run
		// has only the one generation, but there the walk visits each Query
		// Group exactly once and a Query Group can only be active after the
		// walk has already queued it, so it is never passed over as active in
		// the first place.
		return
	}
	if len(dispatcher.delayed) >= dispatcher.bundle.schedulerShape().RecoveryQueueCapacity {
		return
	}
	// The same entry the walk would build, not a bare one. This used to write
	// only the Runner and its readiness, so a deferred Slot re-entered the
	// recovery queue with no deadline, no sequence and no cohort: the order
	// ranked it behind every Slot with a deadline, and a ten-second Slot with
	// five seconds left waited behind the minute's Slots with fifty-five.
	dispatcher.delayed = append(dispatcher.delayed, dispatcher.queueEntry(scheduled, readyAt))
	dispatcher.queued[scheduled.queryGroup] = scheduled.lifecycle
}

// heldByYield is what held the flight a round was turned away from, when
// that is a supplement or maintenance -- a yield by design -- and empty
// otherwise.
func heldByYield(err error) string {
	var inFlight *scheduler.SlotInFlightError
	if !errors.As(err, &inFlight) {
		return ""
	}
	switch inFlight.HeldBy {
	case scheduler.FlightHeldBySupplement, scheduler.FlightHeldByMaintenance:
		return inFlight.HeldBy
	}
	return ""
}

// turnedAwayByHold takes a round a supplement or maintenance turned away.
// It is not queued again at once, as a deferred return is: its Runner still
// says it is ready, and it would be turned away again for as long as the
// hold lasts. It runs when the hold ends -- at once, if the end was heard
// before this return -- or at its next turn.
func (dispatcher *phaseTwoRunnerDispatcher) turnedAwayByHold(scheduled phaseTwoScheduledRunner) {
	if _, ended := dispatcher.released[scheduled.queryGroup]; ended {
		delete(dispatcher.released, scheduled.queryGroup)
		dispatcher.queueNow(scheduled)
		return
	}
	dispatcher.turnedAway[scheduled.queryGroup] = scheduled
}

// flightReleased takes the end of a hold that turned a round away: the round
// is queued now, or, when its return has not been handled yet, the end is
// kept for it.
func (dispatcher *phaseTwoRunnerDispatcher) flightReleased(queryGroup execution.QueryGroupIdentity) {
	scheduled, waiting := dispatcher.turnedAway[queryGroup]
	if !waiting {
		dispatcher.released[queryGroup] = struct{}{}
		return
	}
	delete(dispatcher.turnedAway, queryGroup)
	dispatcher.queueNow(scheduled)
}

// queueNow puts a round in the recovery queue ready now, unless its Runner
// is no longer the current one, it is queued or running already, or the
// queue is full; its next turn offers it then.
func (dispatcher *phaseTwoRunnerDispatcher) queueNow(scheduled phaseTwoScheduledRunner) {
	if !dispatcher.bundle.isCurrentScheduledRunner(scheduled) || dispatcher.queued[scheduled.queryGroup] != nil ||
		dispatcher.active[scheduled.queryGroup] != nil ||
		len(dispatcher.delayed) >= dispatcher.bundle.schedulerShape().RecoveryQueueCapacity {
		return
	}
	dispatcher.delayed = append(dispatcher.delayed, dispatcher.queueEntry(scheduled, dispatcher.bundle.schedulerNow()))
	dispatcher.queued[scheduled.queryGroup] = scheduled.lifecycle
}

// queueEntry is the one way a Runner becomes a queue entry, from the walk
// and from a deferred return alike.
//
// The deadline is read from the Runner every time: it holds the frozen
// Slot's deadline until that Slot completes, so an unfinished Slot reads the
// same value it was queued with and a completed one reads the next Slot's.
// That is also how the two are told apart. A Runner whose deadline is the
// one its place was given for is still due for that Slot and keeps its
// sequence and cohort; one whose deadline moved is being queued for a new
// Slot and is given the next sequence, as any first queueing is. A Runner
// with no deadline either time keeps its place: it ranks after every dated
// entry regardless, and the tie-break is all a new sequence would change.
//
// One consequence, stated so nobody reads the rule as stronger than it is:
// the walk usually queues a Runner before it has frozen its next Slot, and
// the deadline it reads then is the Runner's estimate from its due bound
// (scheduler.Runner.NextDeadline), not the frozen value. The first deferred
// return of that Slot reads the frozen deadline, which differs from the
// estimate, so that return is given a new place. "The same Slot keeps its
// sequence" therefore holds from the second deferral on; the first deferral
// of a Slot queued on an estimate refreshes it once. Telling an estimate
// from a frozen deadline would need the Runner to say which it gave, which
// the interface does not carry; the deadline itself is right in both cases.
func (dispatcher *phaseTwoRunnerDispatcher) queueEntry(scheduled phaseTwoScheduledRunner, readyAt time.Time) phaseTwoQueuedRunner {
	deadline := scheduled.lifecycle.runner.NextDeadline()
	if scheduled.place.sequence == 0 || !deadline.Equal(scheduled.place.deadline) {
		dispatcher.queueSequence++
		scheduled.place = phaseTwoQueuePlace{
			deadline: deadline, sequence: dispatcher.queueSequence,
			cohort: scheduler.ShortPeriodCohortForInterval(scheduled.lifecycle.runner.DueBound().IntervalSeconds),
		}
	}
	return phaseTwoQueuedRunner{
		scheduled: scheduled, readyAt: readyAt,
		deadline: deadline, sequence: scheduled.place.sequence, cohort: scheduled.place.cohort,
	}
}

// recordDueBound rewrites the index entry from the round that has just
// returned, and checks the prediction the dispatcher made about it.
//
// A dispatch that never entered the Runner is skipped: the bound such a return
// would carry belongs to the previous round, and writing it again would restate
// an old answer as if it were fresh.
//
// The prediction is only checked when the round reached a verdict on dueness. A
// round that lost a single-flight race, was cancelled, or failed its ownership
// check says nothing about the schedule, and counting its silence as either
// answer would put non-violations into the one counter whose whole job is to
// prove the index wrong.
func (dispatcher *phaseTwoRunnerDispatcher) recordDueBound(result phaseTwoScheduledResult) {
	if !result.ran {
		return
	}
	scheduled := result.scheduled
	bound := scheduled.lifecycle.runner.DueBound()
	dispatcher.dueIndex.Record(scheduled.queryGroup, scheduled.lifecycle, scheduled.dueEpoch,
		bound, dispatcher.bundle.schedulerNow())
	if bound.Verdict == scheduler.DueVerdictUnknown {
		return
	}
	actuallyDue := bound.Verdict == scheduler.DueVerdictDue
	dispatcher.bundle.dependencies.Recorder.RecordDueIndexPrediction(scheduled.predictedDue, actuallyDue)
	// Only the violation, and only its magnitude. The count of these has been
	// readable for a while and cannot answer why: a bound that reaches minutes
	// past an object that is already due and a clock that crossed the boundary
	// while the round was in flight produce the same tally and need opposite
	// responses.
	if !scheduled.predictedDue && actuallyDue {
		// Split by whether this round was in query cooldown. Cooldown is a
		// deliberate backing-off from a backend that keeps failing, and a
		// cooldown round returns without advancing the cursor -- so the Slot
		// stays due and the prediction is recorded as wrong. Those are a
		// suppression working as intended counted as an index defect, and
		// reading them together puts the two on one curve.
		//
		// Taken from the bound this round returned rather than from the index
		// entry: the entry is about to be rewritten from this same bound, and
		// reading it back would be reading this value through one more step
		// that can drift.
		dispatcher.bundle.dependencies.Recorder.RecordDueIndexAuditOvershoot(
			scheduled.predictedHeldFor, bound.QueryCooldown)
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) sortDelayed() {
	sort.SliceStable(dispatcher.delayed, func(left, right int) bool {
		return delayedBefore(dispatcher.delayed[left], dispatcher.delayed[right])
	})
}

func (dispatcher *phaseTwoRunnerDispatcher) latestDelayedIndex() int {
	latest := -1
	for index := range dispatcher.delayed {
		if latest < 0 || delayedBefore(dispatcher.delayed[latest], dispatcher.delayed[index]) {
			latest = index
		}
	}
	return latest
}

func delayedBefore(left, right phaseTwoQueuedRunner) bool {
	if left.readyAt.Equal(right.readyAt) {
		return left.scheduled.queryGroup < right.scheduled.queryGroup
	}
	return left.readyAt.Before(right.readyAt)
}

// readyQueueVerdict names which clause of deadlineBefore kept an arrival out
// of a full ready queue, so a turned-away short-period Query Group says what
// it lost to rather than only that it lost.
func readyQueueVerdict(arrival, tail phaseTwoQueuedRunner) string {
	switch {
	case arrival.deadline.IsZero():
		return "deadline_unknown"
	case tail.deadline.IsZero() || tail.deadline.Before(arrival.deadline):
		return "tail_earlier"
	}
	return "tail_equal"
}

// observeTurnaway writes the facts of one turnaway for a short-period cohort.
// The counter is recorded for every cohort at the call site; this is the
// explanation, and only the cohorts whose turnaways are a finding get one.
// The observer is scoped by Query Group, so one Query Group turned away every
// pass is rate limited on its own and cannot push out another's line.
func (dispatcher *phaseTwoRunnerDispatcher) observeTurnaway(outcome string, turnedAway, kept phaseTwoQueuedRunner, verdict string, queueLength, queueCapacity int) {
	if !observability.IsShortPeriodCohort(turnedAway.cohort) {
		return
	}
	observeRuntime(context.Background(), dispatcher.bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageDispatchTurnaway,
		Result: observability.ResultSuccess,
		Trace:  observability.TraceFields{QueryGroupKey: string(turnedAway.scheduled.queryGroup)},
		DispatchTurnaway: &observability.DispatchTurnawayFacts{
			Outcome: outcome, Cohort: turnedAway.cohort, Verdict: verdict,
			DeadlineUnixMilli: diagnosticTimeMS(turnedAway.deadline), ReadyAtUnixMilli: diagnosticTimeMS(turnedAway.readyAt),
			KeptQueryGroup: string(kept.scheduled.queryGroup), KeptCohort: kept.cohort,
			KeptDeadlineUnixMilli: diagnosticTimeMS(kept.deadline), KeptReadyAtUnixMilli: diagnosticTimeMS(kept.readyAt),
			QueueLength: queueLength, QueueCapacity: queueCapacity,
		},
	})
}

func (dispatcher *phaseTwoRunnerDispatcher) dropStaleQueued(revision uint64) {
	if dispatcher.prunedRunners == revision {
		return
	}
	dispatcher.prunedRunners = revision
	dispatcher.dueIndex.DropLostLifecycles(func(
		queryGroup execution.QueryGroupIdentity,
		lifecycle *phaseTwoQueryGroupLifecycle,
	) bool {
		return dispatcher.bundle.isCurrentScheduledRunner(phaseTwoScheduledRunner{
			queryGroup: queryGroup, lifecycle: lifecycle,
		})
	})
	drop := func(queue []phaseTwoQueuedRunner) []phaseTwoQueuedRunner {
		kept := queue[:0]
		for _, queued := range queue {
			if dispatcher.bundle.isCurrentScheduledRunner(queued.scheduled) {
				kept = append(kept, queued)
				continue
			}
			if dispatcher.queued[queued.scheduled.queryGroup] == queued.scheduled.lifecycle {
				delete(dispatcher.queued, queued.scheduled.queryGroup)
			}
			if dispatcher.oneShotTargets[queued.scheduled.queryGroup] == queued.scheduled.lifecycle {
				delete(dispatcher.oneShotTargets, queued.scheduled.queryGroup)
			}
		}
		return kept
	}
	dispatcher.normal = drop(dispatcher.normal)
	dispatcher.delayed = drop(dispatcher.delayed)
	for queryGroup, generation := range dispatcher.lastQueued {
		if !dispatcher.bundle.isCurrentScheduledRunner(phaseTwoScheduledRunner{
			queryGroup: queryGroup,
			lifecycle:  generation.lifecycle,
		}) {
			delete(dispatcher.lastQueued, queryGroup)
		}
	}
}

// snapshotScheduledRunners returns the owned Runners in Query Group order. The
// slice is shared and must not be written through: callers copy the entry they
// take. Building it costs a sort of every owned Query Group, and the dispatcher
// asks for it on every pass of a loop that advances at most one Slot, so it is
// rebuilt only after the owned set changes.
func (bundle *phaseTwoWorkerBundle) snapshotScheduledRunners() ([]phaseTwoScheduledRunner, uint64) {
	bundle.mu.RLock()
	ordered, revision := bundle.scheduledRunners, bundle.runnersRevision
	bundle.mu.RUnlock()
	if ordered != nil {
		return ordered, revision
	}
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	if bundle.scheduledRunners != nil {
		return bundle.scheduledRunners, bundle.runnersRevision
	}
	runners := make([]phaseTwoScheduledRunner, 0, len(bundle.runners))
	for queryGroup, lifecycle := range bundle.runners {
		runners = append(runners, phaseTwoScheduledRunner{queryGroup: queryGroup, lifecycle: lifecycle})
	}
	sort.Slice(runners, func(left, right int) bool {
		return runners[left].queryGroup < runners[right].queryGroup
	})
	bundle.scheduledRunners = runners
	return runners, bundle.runnersRevision
}

// ensureDueIndex returns this replica's due index, building it on first use.
// It is built here rather than at assembly so a bundle put together field by
// field - which is how the dispatcher is exercised - still has one.
func (bundle *phaseTwoWorkerBundle) ensureDueIndex() *phaseTwoDueIndex {
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	if bundle.dueIndex == nil {
		bundle.dueIndex = newPhaseTwoDueIndex(bundle.dependencies.Recorder)
	}
	return bundle.dueIndex
}

// dispatchSuppressionFacts is what this replica states about the due index in
// its own snapshot. The index is built if it does not exist yet, so the field
// is present from the very first publish rather than appearing once a
// dispatcher has run: a field that arrives late would read as a build that does
// not suppress at all for as long as it was missing.
func (bundle *phaseTwoWorkerBundle) dispatchSuppressionFacts() *fleet.DispatchSuppression {
	if bundle == nil {
		return nil
	}
	return bundle.ensureDueIndex().SuppressionFacts(bundle.schedulerNow())
}

func (bundle *phaseTwoWorkerBundle) isCurrentScheduledRunner(scheduled phaseTwoScheduledRunner) bool {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return !bundle.draining && !bundle.closed && bundle.runners[scheduled.queryGroup] == scheduled.lifecycle
}

// enterScheduledRunner is isCurrentScheduledRunner for a Slot about to run:
// the same answer, and when it is yes the Slot is counted on its lifecycle
// under the same lock. A check followed by a separate count would leave a
// window in which the Query Group is detached and released between the two,
// and the Slot then runs on a lease its owner has already let go.
func (bundle *phaseTwoWorkerBundle) enterScheduledRunner(scheduled phaseTwoScheduledRunner) bool {
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	if bundle.draining || bundle.closed || bundle.runners[scheduled.queryGroup] != scheduled.lifecycle {
		return false
	}
	lifecycle := scheduled.lifecycle
	if lifecycle.inflight == 0 {
		lifecycle.idle = make(chan struct{})
	}
	lifecycle.inflight++
	bundle.slotsRunning++
	return true
}

func (bundle *phaseTwoWorkerBundle) exitScheduledRunner(lifecycle *phaseTwoQueryGroupLifecycle) {
	bundle.mu.Lock()
	bundle.slotsRunning--
	bundle.leaveLifecycleAndUnlock(lifecycle)
}

// enterOwnedWork is enterScheduledRunner for the work other than a Slot
// that acts on the Query Group's lease: a lookback supplement, which writes
// events, and an inactive-time close, which sends closes. It is counted on
// the Query Group's current lifecycle under the same lock that finds it
// current, refused once the lifecycle is detached or the replica draining,
// and a handover's drain waits for it before the lease is released. Left
// uncounted, a handover released the lease while such a send was in flight
// and the next owner, free to acquire at once, could send the same window
// again. Not a Slot: runningSlots does not count it.
func (bundle *phaseTwoWorkerBundle) enterOwnedWork(queryGroup execution.QueryGroupIdentity) (*phaseTwoQueryGroupLifecycle, bool) {
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	lifecycle, owned := bundle.runners[queryGroup]
	if bundle.draining || bundle.closed || !owned || lifecycle == nil {
		return nil, false
	}
	if lifecycle.inflight == 0 {
		lifecycle.idle = make(chan struct{})
	}
	lifecycle.inflight++
	return lifecycle, true
}

func (bundle *phaseTwoWorkerBundle) exitOwnedWork(lifecycle *phaseTwoQueryGroupLifecycle) {
	bundle.mu.Lock()
	bundle.leaveLifecycleAndUnlock(lifecycle)
}

// leaveLifecycleAndUnlock takes one piece of work off its lifecycle, with
// the bundle's lock held, and releases the lock. The last one out closes
// idle, and lifts the Query Group's decline when that lifecycle's hung
// execution was what it waited for.
func (bundle *phaseTwoWorkerBundle) leaveLifecycleAndUnlock(lifecycle *phaseTwoQueryGroupLifecycle) {
	lifecycle.inflight--
	var lifted execution.QueryGroupIdentity
	var entry *declinedQueryGroup
	if lifecycle.inflight == 0 {
		close(lifecycle.idle)
		lifecycle.idle = nil
		// The hung execution has returned: the replica no longer has a
		// reason to decline its Query Group.
		for queryGroup, declined := range bundle.declined {
			if declined.lifecycle == lifecycle {
				lifted, entry = queryGroup, declined
				delete(bundle.declined, queryGroup)
				break
			}
		}
	}
	bundle.mu.Unlock()
	if entry != nil {
		bundle.recordHungExecution(lifted, entry, observability.ExecutionHungReturned, nil)
	}
}

// slotsInFlight is how many Slots of a lifecycle are running, with the
// other work counted on it (enterOwnedWork), and the channel closed when
// none is; nil when none is now.
func (bundle *phaseTwoWorkerBundle) slotsInFlight(lifecycle *phaseTwoQueryGroupLifecycle) (int, <-chan struct{}) {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	if lifecycle.inflight == 0 {
		return 0, nil
	}
	return lifecycle.inflight, lifecycle.idle
}

// runningSlots is how many Slots are running in this process, detached
// lifecycles included.
func (bundle *phaseTwoWorkerBundle) runningSlots() int {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return bundle.slotsRunning
}

func (bundle *phaseTwoWorkerBundle) schedulerNow() time.Time {
	if bundle.dependencies.Now != nil {
		return bundle.dependencies.Now()
	}
	return time.Now()
}

func (bundle *phaseTwoWorkerBundle) Shutdown(ctx context.Context) error {
	if bundle == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("phase-two worker shutdown context is required")
	}
	bundle.shutdownOnce.Do(func() {
		var result []error
		result = append(result, bundle.beginDraining(ctx))
		bundle.mu.RLock()
		cancelMaintain := bundle.cancelMaintain
		bundle.mu.RUnlock()
		if cancelMaintain != nil {
			cancelMaintain()
		}
		// The scheduler's group is waited for unless the stop's drain has
		// already cancelled Slots that did not return: waiting again would
		// spend the whole of this context on them and leave no time for the
		// releases below, and every lease would expire by TTL instead.
		if drain := bundle.shutdownDrain(); drain == nil || drain.Unreturned == 0 {
			result = append(result, waitPhaseTwoGroup(ctx, &bundle.inflightWG))
		}
		bundle.mu.Lock()
		runners := make([]*phaseTwoQueryGroupLifecycle, 0, len(bundle.runners))
		queryGroups := make([]execution.QueryGroupIdentity, 0, len(bundle.runners))
		for queryGroup, lifecycle := range bundle.runners {
			bundle.removeRunnerLocked(queryGroup)
			lifecycle.cancel()
			runners = append(runners, lifecycle)
			queryGroups = append(queryGroups, queryGroup)
		}
		bundle.setOwnedQueryGroupsLocked()
		bundle.mu.Unlock()
		maintenanceErr := waitPhaseTwoGroup(ctx, &bundle.maintenanceWG)
		result = append(result, maintenanceErr)
		// What is counted on a lifecycle besides the dispatcher's Slots - a
		// supplement, a close send - is waited for before its lease goes, as
		// a handover waits for it; skipped, as the scheduler's group is, when
		// the drain already left Slots it cancelled unreturned.
		if drain := bundle.shutdownDrain(); drain == nil || drain.Unreturned == 0 {
			for _, lifecycle := range runners {
				if _, idle := bundle.slotsInFlight(lifecycle); idle != nil {
					select {
					case <-idle:
					case <-ctx.Done():
					}
				}
			}
		}
		for index, lifecycle := range runners {
			releaseErr := lifecycle.runner.Release(ctx)
			result = append(result, releaseErr)
			transitionResult := observability.Result(observability.ResultSuccess)
			if releaseErr != nil {
				transitionResult = observability.ResultFailed
			}
			bundle.observeOwnership(ctx, observability.StageAssignmentLost, transitionResult, queryGroups[index], releaseErr)
		}
		// The Control Leader lease goes last among the leases, and only when
		// every leader task has stopped: a wait that timed out may leave one
		// still writing, and releasing then would let the next leader start
		// while the old one writes - the overlap the lease exists to prevent.
		// Left to expire, as it always was, in that case.
		if releaser, ok := bundle.dependencies.Ownership.(phaseTwoHeldControlLeaderReleaser); ok && maintenanceErr == nil {
			released, releaseErr := releaser.ReleaseHeldControlLeader(ctx)
			if released {
				bundle.dependencies.Recorder.RecordControlLeaderTransition("none", "release")
			}
			result = append(result, releaseErr)
		} else if releaser, ok := bundle.dependencies.Ownership.(phaseTwoControlLeaderReleaser); ok && maintenanceErr == nil {
			result = append(result, releaser.ReleaseControlLeader(ctx))
		}
		if bundle.dependencies.CloseResources != nil {
			result = append(result, bundle.dependencies.CloseResources(ctx))
		}
		result = append(result, bundle.dependencies.Control.Close(), bundle.dependencies.Ownership.Close())
		bundle.mu.Lock()
		bundle.closed = true
		bundle.mu.Unlock()
		bundle.shutdownErr = errors.Join(result...)
		shutdownResult := observability.Result(observability.ResultSuccess)
		if bundle.shutdownErr != nil {
			shutdownResult = observability.ResultFailed
		}
		bundle.mu.RLock()
		drain := bundle.shutdownDrainFacts
		bundle.mu.RUnlock()
		observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
			Component: observability.ComponentRuntime, Stage: observability.StageShutdown, Result: shutdownResult,
			Direction: observability.DirectionInternal, Err: bundle.shutdownErr, SlotDrain: drain,
		})
	})
	return bundle.shutdownErr
}

// beginDraining is the first step of every stop, once: the replica stops
// being judged for liveness, refuses new Slots and new Assignments, reports
// DRAINING and registers it, so the leader stops placing work here while the
// Slots in flight finish.
func (bundle *phaseTwoWorkerBundle) beginDraining(ctx context.Context) error {
	bundle.drainOnce.Do(func() {
		bundle.liveness.stopJudging()
		bundle.mu.Lock()
		bundle.draining = true
		bundle.mu.Unlock()
		bundle.publishControlCandidacy()
		bundle.dependencies.Health.Update(phaseTwoReadiness{State: observability.HealthDraining})
		bundle.drainErr = bundle.register(ctx, ownership.WorkerDraining)
	})
	return bundle.drainErr
}

// drainSlots waits for the dispatcher, already told to stop, to return: it
// returns once every Slot in flight has. Past the drain deadline the Slots
// still running are cancelled, and the dispatcher is waited for again --
// they return on cancellation. What happened is kept for the shutdown line
// and the lifecycle record.
func (bundle *phaseTwoWorkerBundle) drainSlots(
	ctx context.Context,
	schedulerDone <-chan error,
	cancelExecution context.CancelFunc,
) error {
	began := time.Now()
	drain := observability.SlotDrainFacts{Site: observability.SlotDrainSiteShutdown, Waited: bundle.runningSlots()}
	var schedulerErr error
	select {
	case schedulerErr = <-schedulerDone:
		drain.Outcome = observability.SlotDrainFinished
		if drain.Waited == 0 {
			drain.Outcome = observability.SlotDrainIdle
		}
	case <-ctx.Done():
		drain.Outcome, drain.Cancelled = observability.SlotDrainDeadline, bundle.runningSlots()
		cancelExecution()
		// Bounded too: a Slot that honours its context returns within
		// moments of the cancellation, and one that has not is in a call
		// that does not watch it, whose own timeout would only spend what the
		// releases after this need. The stop goes on without it.
		select {
		case schedulerErr = <-schedulerDone:
		case <-time.After(drainCancelGrace):
			drain.Outcome, drain.Unreturned = observability.SlotDrainDeadlineUnreturned, bundle.runningSlots()
		}
	}
	drain.WaitMS = time.Since(began).Milliseconds()
	bundle.mu.Lock()
	bundle.shutdownDrainFacts = &drain
	bundle.mu.Unlock()
	return schedulerErr
}

// drainCancelGrace is how long a stop waits, after cancelling the Slots still
// running at its drain deadline, for them to return. A Slot that honours its
// context returns within milliseconds; one still running a second later is
// blocked in a call that ignores it.
const drainCancelGrace = time.Second

// shutdownDrain is the stop's wait for its Slots; nil before a stop has run
// one.
func (bundle *phaseTwoWorkerBundle) shutdownDrain() *observability.SlotDrainFacts {
	if bundle == nil {
		return nil
	}
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	if bundle.shutdownDrainFacts == nil {
		return nil
	}
	drain := *bundle.shutdownDrainFacts
	return &drain
}

func markPhaseTwoFatal(
	ctx context.Context,
	bundle *phaseTwoWorkerBundle,
	health *phaseTwoApplicationHealth,
	err error,
) {
	health.Update(phaseTwoReadiness{
		State: observability.HealthFatal, Reasons: []observability.ReasonCode{observability.ReasonInternalUnknown},
	})
	if bundle != nil {
		bundle.observe(ctx, observability.ComponentRuntime, observability.Stage(observability.StageFatal), observability.ResultFailed, err)
	}
}

func (bundle *phaseTwoWorkerBundle) register(ctx context.Context, readiness ownership.AssignmentReadiness) error {
	if !bundle.dependencies.Config.HasRole(roles.Worker) {
		return nil
	}
	bundle.registrationMu.Lock()
	defer bundle.registrationMu.Unlock()
	if readiness == ownership.WorkerReady {
		bundle.mu.RLock()
		draining := bundle.draining
		factsSeen := bundle.controlFactsSeen
		sinkReady := bundle.outputSinkReady
		bundle.mu.RUnlock()
		if draining {
			return nil
		}
		if !factsSeen {
			// A replica that has never read the control facts cannot say which
			// Query Groups exist, so it must not be given a share of them.
			// Registering as starting is how it says so while keeping its
			// registration alive: the renewal loop runs either way, so waiting
			// costs nothing and the replica joins on the round that reads the
			// facts. This is the half of "do not exit on a control read" that
			// is easy to leave out -- a replica that stays up but reports
			// ready would be handed a share of Query Groups it cannot open.
			readiness = ownership.WorkerStarting
		}
		if !sinkReady {
			// The same half for the output sink: a replica that cannot
			// publish must not be handed Query Groups to decide. The renewal
			// loop registers it ready on the first renewal after the sink
			// opens, within one renewal interval.
			readiness = ownership.WorkerStarting
		}
	}
	registration, err := phaseTwoWorkerRegistration(
		bundle.dependencies.Config, readiness, bundle.dependencies.Now(), bundle.appliedFacts(), bundle.loadFacts(),
		bundle.dependencies.StreamIdentity,
	)
	if err != nil {
		return err
	}
	registration.Declined = bundle.declinedRegistration()
	registration.UnreadableTimelines, registration.UnreadableTimelinesTotal = bundle.unreadableTimelinesRegistration()
	if err := bundle.dependencies.Ownership.RegisterWorker(ctx, registration); err != nil {
		return fmt.Errorf("phase-two register worker as %s: %w", readiness, err)
	}
	return nil
}

// appliedFacts is what this worker reports executing by. Nil when the bundle
// has no source for it, so the heartbeat carries no acknowledgement rather
// than a zero one.
func (bundle *phaseTwoWorkerBundle) appliedFacts() *ownership.AppliedControlFacts {
	if bundle.applied == nil {
		return nil
	}
	return &ownership.AppliedControlFacts{ActivationRecordRevision: bundle.applied()}
}

// loadFacts copies the occupancy the fleet snapshot already measures into the
// heartbeat, plus the owned count. Nil when the bundle has no capacity source
// or the source has nothing to say.
func (bundle *phaseTwoWorkerBundle) loadFacts() *ownership.WorkerLoad {
	if bundle.capacity == nil {
		return nil
	}
	capacity := bundle.capacity()
	if capacity == nil {
		return nil
	}
	return &ownership.WorkerLoad{
		OwnedQueryGroups: len(bundle.ownedQueryGroups()),
		PermitsHeld:      capacity.PermitsHeld, PermitBudget: capacity.PermitBudget,
		PermitSeconds: capacity.PermitSeconds, Waiting: capacity.Waiting,
		MemoryUsedBytes: capacity.MemoryUsed, MemoryLimitBytes: capacity.MemoryLimit,
		// The pool the Leader judges this replica's byte constraint against
		// (decision-020 section 5.7): the same number the coordinator holds
		// Slots under, so the Leader never re-derives it from the memory
		// limit with a divisor of its own.
		RetainedPoolBytes: bundle.dependencies.Config.PhaseTwo.Coordinator.MaxRetainedBytes,
	}
}

func phaseTwoWorkerRegistration(
	cfg config.Config,
	readiness ownership.AssignmentReadiness,
	at time.Time,
	applied *ownership.AppliedControlFacts,
	load *ownership.WorkerLoad,
	stream viewStreamIdentity,
) (ownership.WorkerRegistration, error) {
	capabilitiesDigest, err := phaseTwoCapabilitiesDigest(cfg)
	if err != nil {
		return ownership.WorkerRegistration{}, fmt.Errorf("phase-two derive worker capabilities: %w", err)
	}
	executionDigest, err := phaseTwoExecutionContractDigest()
	if err != nil {
		return ownership.WorkerRegistration{}, err
	}
	registration := ownership.WorkerRegistration{
		WorkerID: cfg.PhaseTwo.Worker.ID, AssignmentReadiness: readiness,
		DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: cfg.DeploymentProfile(),
		CapabilitiesDigest:      capabilitiesDigest,
		ExecutionContractDigest: executionDigest,
		ExpiresAt:               at.Add(cfg.PhaseTwo.Worker.RegistrationTTL.Duration()),
		Applied:                 applied, Load: load,
		// The control contracts this binary takes part in. The leader
		// starts a contract only when every ready worker declares it.
		Capabilities: []string{ownership.CapabilityContentScope, ownership.CapabilityShardAware},
		// Where this process serves the view stream and what a Worker must
		// present to it (decision-016); empty for a process without one.
		Endpoint: stream.Endpoint, StreamToken: stream.Token,
	}
	if err := registration.Validate(); err != nil {
		return ownership.WorkerRegistration{}, err
	}
	return registration, nil
}

func (bundle *phaseTwoWorkerBundle) startMaintenance() {
	if bundle.dependencies.RunOpenAlerts != nil {
		bundle.maintenanceWG.Add(1)
		go func() {
			defer bundle.maintenanceWG.Done()
			_ = bundle.dependencies.RunOpenAlerts(bundle.maintenanceCtx)
		}()
	}
	if bundle.dependencies.RunAbsentClose != nil {
		bundle.maintenanceWG.Add(1)
		go func() { defer bundle.maintenanceWG.Done(); bundle.dependencies.RunAbsentClose(bundle.maintenanceCtx) }()
	}
	if bundle.dependencies.RunTargetScopeClose != nil {
		bundle.maintenanceWG.Add(1)
		go func() {
			defer bundle.maintenanceWG.Done()
			bundle.dependencies.RunTargetScopeClose(bundle.maintenanceCtx)
		}()
	}
	if bundle.dependencies.RunEffectiveTime != nil {
		bundle.maintenanceWG.Add(1)
		go func() { defer bundle.maintenanceWG.Done(); bundle.dependencies.RunEffectiveTime(bundle.maintenanceCtx) }()
	}
	if bundle.dependencies.Lookback != nil {
		bundle.maintenanceWG.Add(1)
		go func() {
			defer bundle.maintenanceWG.Done()
			runLookback(bundle.maintenanceCtx, bundle.dependencies.Lookback)
		}()
	}
	if bundle.dependencies.Config.HasRole(roles.Worker) {
		bundle.maintenanceWG.Add(1)
		go bundle.maintainRegistration()
	}
	if bundle.dependencies.PublishFleet != nil {
		bundle.maintenanceWG.Add(1)
		go bundle.publishFleetSnapshots()
	}
	if bundle.dependencies.RefreshOpenAlerts != nil {
		bundle.maintenanceWG.Add(1)
		go bundle.refreshOpenAlerts()
	}
	if bundle.dependencies.RefreshPlatformSettings != nil {
		bundle.maintenanceWG.Add(1)
		go bundle.refreshPlatformSettings()
	}
	if bundle.dependencies.MeasureStores != nil {
		bundle.maintenanceWG.Add(1)
		go bundle.measureStores()
	}
	if bundle.dependencies.ViewClient != nil {
		var viewCtx context.Context
		viewCtx, bundle.cancelViewClient = context.WithCancel(bundle.maintenanceCtx)
		bundle.maintenanceWG.Add(1)
		go bundle.runViewClient(viewCtx)
	}
}

// measureStores takes a census of the stores once every storecensus.Interval,
// on the Control Leader. The first is an interval after the start, not at
// it: a starting process has rounds to catch up on first.
func (bundle *phaseTwoWorkerBundle) measureStores() {
	defer bundle.maintenanceWG.Done()
	ticker := time.NewTicker(storecensus.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-bundle.maintenanceCtx.Done():
			return
		case <-ticker.C:
			bundle.mu.RLock()
			leading := bundle.controlLeader
			bundle.mu.RUnlock()
			bundle.dependencies.MeasureStores(bundle.maintenanceCtx, leading)
		}
	}
}

// refreshPlatformSettings keeps the process copy of the platform's settings
// current, once a minute. The copy was read once at assembly; this is the
// cadence the platform's changes reach evaluation at. A read that fails is
// the copy's own state to report; nothing here retries or stops anything.
func (bundle *phaseTwoWorkerBundle) refreshPlatformSettings() {
	defer bundle.maintenanceWG.Done()
	ticker := time.NewTicker(platformsettings.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-bundle.maintenanceCtx.Done():
			return
		case <-ticker.C:
			bundle.dependencies.RefreshPlatformSettings(bundle.maintenanceCtx)
		}
	}
}

// refreshOpenAlerts keeps the process copy of the consumer's open alert set
// current. The first read is immediate rather than a cycle away, so the
// first evaluations after a start are not answered from an empty copy when
// the publication is there to read. A read that fails is the copy's own
// state to report (it goes self-maintained and says why); nothing here
// retries or stops the pipeline over it.
func (bundle *phaseTwoWorkerBundle) refreshOpenAlerts() {
	defer bundle.maintenanceWG.Done()
	bundle.dependencies.RefreshOpenAlerts(bundle.maintenanceCtx)
	ticker := time.NewTicker(openalerts.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-bundle.maintenanceCtx.Done():
			return
		case <-ticker.C:
			bundle.dependencies.RefreshOpenAlerts(bundle.maintenanceCtx)
		}
	}
}

// publishFleetSnapshots republishes this replica's object facts on the reconcile
// cadence. A publish failure is left to the next tick: the snapshot is
// diagnostics, and diagnostics must not be able to stop the pipeline whose
// facts they describe.
func (bundle *phaseTwoWorkerBundle) publishFleetSnapshots() {
	defer bundle.maintenanceWG.Done()
	interval := bundle.dependencies.Config.PhaseTwo.Control.ReconcileInterval.Duration()
	if interval <= 0 {
		interval = time.Second * 5
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-bundle.maintenanceCtx.Done():
			return
		case <-ticker.C:
			bundle.dependencies.PublishFleet(bundle.maintenanceCtx)
		}
	}
}

func (bundle *phaseTwoWorkerBundle) tryAcquireControlLeader(ctx context.Context) (bool, error) {
	if bundle.candidacy == "" {
		return false, nil
	}
	bundle.mu.RLock()
	if bundle.controlLeader {
		bundle.mu.RUnlock()
		return true, nil
	}
	if bundle.controlRunning {
		bundle.mu.RUnlock()
		return false, nil
	}
	ready := bundle.candidacyReady && !bundle.draining && !bundle.closed
	bundle.mu.RUnlock()
	if !controlContentionAllowed(bundle.candidacy, ready, false, false) {
		return false, nil
	}
	if bundle.candidacy == ownership.ControlCandidacyPreferred {
		allowed, err := bundle.preferredMayContend(ctx)
		if err != nil {
			bundle.noteControlRole(false, err)
			return false, err
		}
		if !allowed {
			bundle.noteControlRole(false, nil)
			return false, nil
		}
	}
	if bundle.candidacy == ownership.ControlCandidacyFallback {
		allowed, err := bundle.fallbackMayContend(ctx)
		if err != nil {
			bundle.noteControlRole(false, err)
			return false, err
		}
		if !allowed {
			bundle.noteControlRole(false, nil)
			return false, nil
		}
	}
	leader, err := bundle.dependencies.Ownership.TryAcquireControlLeader(
		ctx, bundle.dependencies.Now(), bundle.dependencies.Config.PhaseTwo.Ownership.ControlLeaderTTL.Duration(),
	)
	bundle.noteControlRole(leader, err)
	if err != nil || !leader {
		return leader, err
	}
	bundle.recordControlTakeover()
	bundle.handback.reset()
	bundle.startControlMaintenance()
	// A new term: publish the stored view before this round's refresh, so a
	// Worker without a view does not wait for the whole refresh to get one.
	if publisher, ok := bundle.dependencies.Ownership.(phaseTwoStoredViewPublisher); ok {
		publisher.PublishStoredView(ctx)
	}
	return true, nil
}

// phaseTwoStoredViewPublisher publishes a new term's first view from stored
// facts; optional, see productionPhaseTwoOwnership.PublishStoredView.
type phaseTwoStoredViewPublisher interface {
	PublishStoredView(context.Context)
}

// phaseTwoControlLeaderReleaser gives up the Control Leader lease at
// shutdown; optional, see productionPhaseTwoOwnership.ReleaseControlLeader.
type phaseTwoControlLeaderReleaser interface {
	ReleaseControlLeader(context.Context) error
}

func (bundle *phaseTwoWorkerBundle) startControlMaintenance() {
	cfg := bundle.dependencies.Config.PhaseTwo
	bundle.mu.Lock()
	if bundle.draining || bundle.closed || bundle.controlRunning {
		bundle.mu.Unlock()
		return
	}
	controlCtx, cancel := context.WithCancel(bundle.maintenanceCtx)
	bundle.controlLeader = true
	bundle.controlRunning = true
	bundle.setControlRoleLocked(observability.ControlSourceRoleLeader)
	bundle.controlEpoch++
	controlEpoch := bundle.controlEpoch
	bundle.cancelControl = cancel
	bundle.maintenanceWG.Add(1)
	bundle.mu.Unlock()
	go func() {
		defer bundle.maintenanceWG.Done()
		err := bundle.dependencies.Ownership.MaintainControlLeader(
			controlCtx, cfg.Ownership.ControlLeaderRenewInterval.Duration(), cfg.Ownership.ControlLeaderTTL.Duration(),
		)
		bundle.mu.Lock()
		if bundle.controlEpoch == controlEpoch {
			bundle.controlLeader = false
			bundle.controlRunning = false
			bundle.cancelControl = nil
		}
		bundle.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			bundle.observe(context.Background(), observability.ComponentControlPlane, observability.StageSnapshotUnavailable, observability.ResultFailed, err)
		}
	}()
}

// maintainRegistration renews the READY registration every
// RegistrationRenewInterval for as long as the Worker runs. A failure to
// reach the Ownership Store is transient: it is observed with the shared
// retryable dependency reason, marks the Worker degraded, is retried inside
// the same interval first and the loop keeps running, so one short store
// outage never lets the registration lapse for the rest of the process
// lifetime. Only cancellation of the maintenance context or a phase-two
// invariant error ends the loop; the invariant case is the one that marks
// ownership unsafe.
func (bundle *phaseTwoWorkerBundle) maintainRegistration() {
	defer bundle.maintenanceWG.Done()
	interval := bundle.dependencies.Config.PhaseTwo.Worker.RegistrationRenewInterval.Duration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-bundle.maintenanceCtx.Done():
			return
		case <-ticker.C:
		}
		if holds := bundle.dependencies.ReadHolds; holds != nil {
			ctx, cancel := context.WithTimeout(bundle.maintenanceCtx, interval)
			holds.renew(ctx)
			cancel()
		}
		err := renewPhaseTwoWithinInterval(bundle.maintenanceCtx, interval, func(attemptCtx context.Context) error {
			return bundle.register(attemptCtx, ownership.WorkerReady)
		}, func(err error) {
			failing = true
			bundle.observeRegistrationRenewal(observability.ResultFailed, err)
			bundle.markControlDependencyDegraded()
		}, nil)
		switch {
		case err == nil:
			if failing {
				failing = false
				bundle.observeRegistrationRenewal(observability.ResultResumed, nil)
			}
		case bundle.maintenanceCtx.Err() != nil:
			return
		case isPhaseTwoInvariantError(err):
			bundle.markOwnershipUnsafe(err)
			return
		}
	}
}

func (bundle *phaseTwoWorkerBundle) applyAssignment(
	ctx context.Context,
	assigned []execution.QueryGroupIdentity,
) error {
	desired := make(map[execution.QueryGroupIdentity]struct{}, len(assigned))
	for _, queryGroup := range assigned {
		if queryGroup == "" {
			return newPhaseTwoInvariantError("phase-two Assignment contains empty Query Group")
		}
		if _, duplicate := desired[queryGroup]; duplicate {
			return newPhaseTwoInvariantError("phase-two Assignment contains duplicate Query Group")
		}
		desired[queryGroup] = struct{}{}
	}

	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return errPhaseTwoWorkerDraining
	}
	bundle.assigned = desired
	bundle.assignmentRead = true
	removed := make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle)
	for queryGroup, lifecycle := range bundle.runners {
		if _, keep := desired[queryGroup]; keep {
			continue
		}
		bundle.removeRunnerLocked(queryGroup)
		removed[queryGroup] = lifecycle
		// The handover is counted here, under the lock that saw the bundle
		// not draining, so Shutdown's wait on the group cannot begin before it.
		bundle.maintenanceWG.Add(1)
	}
	bundle.setOwnedQueryGroupsLocked()
	missing := make([]execution.QueryGroupIdentity, 0, len(desired))
	for queryGroup := range desired {
		_, open := bundle.runners[queryGroup]
		// A declined Query Group stays desired until a leader places it
		// elsewhere; it is not opened here again while its execution hangs.
		_, declined := bundle.declined[queryGroup]
		if !open && !declined {
			missing = append(missing, queryGroup)
		}
	}
	bundle.mu.Unlock()

	// One line for the whole change, however it returns: the per-Query Group
	// successes below are counted and not written, and at a start they were
	// about 2,100 lines a Pod.
	var applied observability.AssignmentAppliedFacts
	defer bundle.observeAssignmentApplied(ctx, &applied)

	// Every error below is scoped to one Query Group. Store I/O failures mark
	// the Worker degraded and are retried on the next reconcile; siblings and
	// the Worker continue. Only invariant violations and cancellation return.
	for queryGroup, lifecycle := range removed {
		applied.Note(string(queryGroup), true, false)
		go bundle.handOverQueryGroup(queryGroup, lifecycle)
	}
	// Every missing Query Group is opened before any of their Runners starts,
	// and what the opened ones keep in the ownership store is read in
	// between, once for the round (LoadOpened): after their leases are held,
	// so it is what the previous owner last wrote, and before their first
	// Slot is classified. Read one at a time as each was opened, a slow store
	// cost every one of them a timeout at the takeover with the most to
	// catch up.
	type openedQueryGroup struct {
		queryGroup execution.QueryGroupIdentity
		runner     phaseTwoQueryGroupRuntime
	}
	opened := make([]openedQueryGroup, 0, len(missing))
	var stop error
	for _, queryGroup := range missing {
		bundle.observeOwnership(ctx, observability.StageTakeoverStarted, observability.ResultStarted, queryGroup, nil)
		runner, err := bundle.dependencies.Ownership.OpenQueryGroup(
			ctx, queryGroup, bundle.dependencies.Now(), bundle.dependencies.Config.PhaseTwo.Ownership.LeaseTTL.Duration(),
		)
		if err != nil {
			applied.Note(string(queryGroup), false, true)
			bundle.observeOwnership(ctx, observability.StageTakeoverCompleted, observability.ResultFailed, queryGroup, err)
			if errors.Is(err, ownership.ErrLeaseBusy) || errors.Is(err, ownership.ErrNotDesired) ||
				errors.Is(err, ownership.ErrStaleFence) {
				continue
			}
			if ctx.Err() != nil {
				stop = ctx.Err()
				break
			}
			if isPhaseTwoInvariantError(err) {
				stop = fmt.Errorf("phase-two open Query Group %s: %w", queryGroup, err)
				break
			}
			bundle.markControlDependencyDegraded()
			continue
		}
		if runner == nil {
			stop = newPhaseTwoInvariantError(fmt.Sprintf("phase-two open Query Group %s returned no runner", queryGroup))
			break
		}
		opened = append(opened, openedQueryGroup{queryGroup: queryGroup, runner: runner})
	}
	if stop != nil {
		// What the round opened and will not start is released, as a start
		// the bundle refuses is.
		for _, entry := range opened {
			_ = bundle.releaseUnstarted(ctx, entry.queryGroup, entry.runner)
		}
		return stop
	}
	queryGroups := make([]execution.QueryGroupIdentity, len(opened))
	for index, entry := range opened {
		queryGroups[index] = entry.queryGroup
	}
	bundle.dependencies.Ownership.LoadOpened(ctx, queryGroups)
	canceled := false
	for _, entry := range opened {
		if bundle.startQueryGroup(ctx, entry.queryGroup, entry.runner) {
			applied.Note(string(entry.queryGroup), false, false)
			continue
		}
		if err := bundle.releaseUnstarted(ctx, entry.queryGroup, entry.runner); err != nil && ctx.Err() != nil {
			canceled = true
		}
	}
	if canceled {
		return ctx.Err()
	}
	return nil
}

// releaseUnstarted releases a Query Group opened and never started. A
// release that fails leaves the lease to expire by its TTL.
func (bundle *phaseTwoWorkerBundle) releaseUnstarted(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	runner phaseTwoQueryGroupRuntime,
) error {
	err := runner.Release(ctx)
	if err != nil {
		bundle.observeOwnership(ctx, observability.StageAssignmentLost, observability.ResultFailed, queryGroup, err)
		if ctx.Err() == nil {
			bundle.markControlDependencyDegraded()
		}
	}
	return err
}

// observeAssignmentApplied writes the one line an assignment change has, when
// it moved anything: how many Query Groups were released, taken over and not
// taken over, and the first few of each.
func (bundle *phaseTwoWorkerBundle) observeAssignmentApplied(ctx context.Context, applied *observability.AssignmentAppliedFacts) {
	if applied == nil || !applied.Changed() {
		return
	}
	facts := *applied
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentApplied,
		Result: observability.ResultSuccess, Operation: observability.OperationTransition,
		Direction: observability.DirectionInternal, AssignmentApplied: &facts,
		Trace: observability.TraceFields{OwnerID: bundle.dependencies.Config.PhaseTwo.Worker.ID},
	})
}

// markControlDependencyDegraded records one transient control or Ownership
// Store failure. The Worker stays Ready-degraded (or NotReady when the
// Assignment is incomplete) with a fixed readiness reason until a full
// refresh or reconcile pass completes without a new failure.
func (bundle *phaseTwoWorkerBundle) markControlDependencyDegraded() {
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return
	}
	bundle.dependencyDegraded = true
	bundle.dependencyFailureSeq++
	bundle.mu.Unlock()
	bundle.updateReadiness()
}

func (bundle *phaseTwoWorkerBundle) controlDependencyFailureSeq() uint64 {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return bundle.dependencyFailureSeq
}

// clearControlDependencyDegraded ends the degraded episode after a reconcile
// pass that started at sinceSeq completed without another dependency failure.
func (bundle *phaseTwoWorkerBundle) clearControlDependencyDegraded(ctx context.Context, sinceSeq uint64) {
	bundle.mu.Lock()
	if !bundle.dependencyDegraded || bundle.dependencyFailureSeq != sinceSeq {
		bundle.mu.Unlock()
		return
	}
	bundle.dependencyDegraded = false
	bundle.lastControlRecoveryAt = bundle.dependencies.Now()
	bundle.mu.Unlock()
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
		Result: observability.Result(observability.ResultRecovered), Direction: observability.DirectionInternal,
		ReasonCode: phaseTwoControlDependencyReason,
	})
}

// scopeControlError classifies one refresh or reconcile error. Invariant
// violations and cancellation of the Run context propagate so Run stops.
// Everything else is a dependency failure: it is logged once with a fixed
// reason, marks the Worker degraded and is retried on the next tick.
func (bundle *phaseTwoWorkerBundle) scopeControlError(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	err error,
) error {
	if err == nil {
		return nil
	}
	if isPhaseTwoInvariantError(err) || errors.Is(err, errPhaseTwoWorkerDraining) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: component, Stage: stage, Result: observability.ResultFailed,
		Direction: observability.DirectionInternal, ReasonCode: phaseTwoControlDependencyReason, Err: err,
	})
	bundle.markControlDependencyDegraded()
	return nil
}

func (bundle *phaseTwoWorkerBundle) startQueryGroup(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	runner phaseTwoQueryGroupRuntime,
) bool {
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return false
	}
	if _, desired := bundle.assigned[queryGroup]; !desired {
		bundle.mu.Unlock()
		return false
	}
	if _, exists := bundle.runners[queryGroup]; exists {
		bundle.mu.Unlock()
		return false
	}
	leaseCtx, cancel := context.WithCancel(bundle.maintenanceCtx)
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: runner, cancel: cancel, done: make(chan struct{})}
	bundle.setRunnerLocked(queryGroup, lifecycle)
	bundle.maintenanceWG.Add(1)
	bundle.mu.Unlock()
	bundle.observeOwnership(ctx, observability.StageTakeoverCompleted, observability.ResultSuccess, queryGroup, nil)
	bundle.observeOwnership(ctx, observability.StageAssignmentAcquired, observability.ResultSuccess, queryGroup, nil)

	cfg := bundle.dependencies.Config.PhaseTwo.Ownership
	go func() {
		defer bundle.maintenanceWG.Done()
		defer close(lifecycle.done)
		err := runner.MaintainLease(leaseCtx, cfg.LeaseRenewInterval.Duration(), cfg.LeaseTTL.Duration())
		if err != nil && !errors.Is(err, context.Canceled) {
			bundle.detachLostQueryGroup(queryGroup, lifecycle, err)
		}
	}()
	return true
}

// handOverQueryGroup lets go of a Query Group this Worker is no longer
// desired for (design 02 §6.2 steps 2-3). Its lifecycle is already detached,
// so no Slot starts on it. A Slot already running is waited for while the
// lease keeps renewing: the store admits this holder until the grace the move
// gave it ends, and caps the renewal there. The lease is released after the
// Slot, so the commit boundary it is in lands under the lease it began under,
// and the next owner does not send its events again.
//
// The wait ends with the Slot, or with the lease. Once renewal stops -- the
// grace ran out, or the store refused the lease -- the Slot's later writes are
// refused whatever happens, and waiting longer only keeps the next owner out.
// A process that begins to stop meanwhile stops renewing every lease, and the
// Slot is then waited for within the shutdown timeout, which is what the stop
// gives its own Slots. It runs on its own goroutine under maintenanceWG, so
// the control loop never waits on a Slot.
func (bundle *phaseTwoWorkerBundle) handOverQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
) {
	defer bundle.maintenanceWG.Done()
	bound := bundle.dependencies.Config.ShutdownTimeout.Duration()
	began := time.Now()
	waited, idle := bundle.slotsInFlight(lifecycle)
	outcome := observability.SlotDrainIdle
	if idle != nil {
		select {
		case <-idle:
		case <-lifecycle.done:
		}
		switch {
		case channelClosed(idle):
			outcome = observability.SlotDrainFinished
		case bundle.maintenanceCtx != nil && bundle.maintenanceCtx.Err() != nil:
			outcome = observability.SlotDrainStopped
			select {
			case <-idle:
			case <-time.After(bound):
			}
		default:
			outcome = observability.SlotDrainLeaseEnded
		}
	}
	bundle.recordHandoverDrain(queryGroup, observability.SlotDrainFacts{
		Site: observability.SlotDrainSiteHandover, Outcome: outcome, Waited: waited,
		WaitMS: time.Since(began).Milliseconds(),
	})
	lifecycle.cancel()
	// Bounded as a lost Query Group's stop is: a lease goroutine that did not
	// end would otherwise hold this one for good. Past the bound the lease is
	// released anyway; a renewal still in flight then finds its fence stale.
	select {
	case <-lifecycle.done:
	case <-time.After(bound):
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	if err := lifecycle.runner.Release(releaseCtx); err != nil {
		bundle.observeOwnership(releaseCtx, observability.StageAssignmentLost, observability.ResultFailed, queryGroup,
			fmt.Errorf("phase-two release Query Group %s: %w", queryGroup, err))
		// The lease expires by its TTL.
		bundle.markControlDependencyDegraded()
		return
	}
	bundle.observeOwnership(releaseCtx, observability.StageAssignmentLost, observability.ResultSuccess, queryGroup, nil)
}

func channelClosed(signal <-chan struct{}) bool {
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

// recordHandoverDrain counts one handover's wait and writes its line.
func (bundle *phaseTwoWorkerBundle) recordHandoverDrain(queryGroup execution.QueryGroupIdentity, drain observability.SlotDrainFacts) {
	bundle.dependencies.Recorder.RecordHandoverDrain(drain.Outcome, time.Duration(drain.WaitMS)*time.Millisecond)
	observeRuntime(context.Background(), bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageHandoverDrained,
		Result: observability.ResultSuccess, Operation: observability.OperationTransition,
		Direction: observability.DirectionInternal, SlotDrain: &drain,
		Trace: observability.TraceFields{QueryGroupKey: string(queryGroup), OwnerID: bundle.dependencies.Config.PhaseTwo.Worker.ID},
	})
}

func (bundle *phaseTwoWorkerBundle) stopLostQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
	err error,
) {
	if !bundle.detachQueryGroup(queryGroup, lifecycle) {
		return
	}
	lifecycle.cancel()
	select {
	case <-lifecycle.done:
	case <-time.After(bundle.dependencies.Config.ShutdownTimeout.Duration()):
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	if releaseErr := lifecycle.runner.Release(releaseCtx); releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	bundle.updateReadiness()
	bundle.observeOwnership(context.Background(), observability.StageAssignmentLost, lostResult(err), queryGroup, err)
}

func (bundle *phaseTwoWorkerBundle) detachLostQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
	err error,
) {
	if !bundle.detachQueryGroup(queryGroup, lifecycle) {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	if releaseErr := lifecycle.runner.Release(releaseCtx); releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	bundle.updateReadiness()
	bundle.observeOwnership(context.Background(), observability.StageAssignmentLost, lostResult(err), queryGroup, err)
}

// lostResult is how a Query Group let go of outside a planned stop reads: a
// failure, unless its lease ran out at a content switch - the holder was
// still on the old content when the change fell due, and the next reconcile
// takes the Query Group again on the new content. Its reason says so
// either way (ownership.LeaseEndedAtContentSwitch).
func lostResult(err error) observability.Result {
	var switched *ownership.LeaseEndedAtContentSwitch
	if errors.As(err, &switched) {
		return observability.ResultSuccess
	}
	return observability.ResultFailed
}

func (bundle *phaseTwoWorkerBundle) detachQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
) bool {
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	if bundle.runners[queryGroup] != lifecycle {
		return false
	}
	bundle.removeRunnerLocked(queryGroup)
	return true
}

// ownedQueryGroups reports what this replica currently holds. The published
// snapshot needs it to make coverage arithmetic possible: an anomaly list alone
// cannot distinguish "nothing wrong here" from "nothing seen here".
func (bundle *phaseTwoWorkerBundle) ownedQueryGroups() []execution.QueryGroupIdentity {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	owned := make([]execution.QueryGroupIdentity, 0, len(bundle.runners))
	for queryGroup := range bundle.runners {
		owned = append(owned, queryGroup)
	}
	return owned
}

// ownedContentScope is the content the owned Query Group's Runner runs now,
// as its lease's last acquire or renewal left it; empty for a Query Group
// this process does not own or whose lease names no content.
func (bundle *phaseTwoWorkerBundle) ownedContentScope(queryGroup execution.QueryGroupIdentity) string {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	lifecycle, owned := bundle.runners[queryGroup]
	if !owned {
		return ""
	}
	runner, ok := lifecycle.runner.(maintenanceRunner)
	if !ok {
		return ""
	}
	contentScope, _, _ := runner.maintenanceLease()
	return contentScope
}

// ownedLeases is the owned Query Groups with the timeline revision each
// lease names, read from memory, for the cost roster. A Runner without a
// lease to read is owned and not accepting.
func (bundle *phaseTwoWorkerBundle) ownedLeases() []ownedLease {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	owned := make([]ownedLease, 0, len(bundle.runners))
	for queryGroup, lifecycle := range bundle.runners {
		lease := ownedLease{queryGroup: queryGroup}
		if runner, ok := lifecycle.runner.(maintenanceRunner); ok {
			_, lease.revision, lease.accepting = runner.maintenanceLease()
		}
		owned = append(owned, lease)
	}
	return owned
}

// setRunnerLocked and removeRunnerLocked are the only ways the owned Runner
// set changes. Both keep what is derived from that set - the owned count and
// the dispatcher's ordered view - in step with it, which is why no caller
// writes the map itself.
func (bundle *phaseTwoWorkerBundle) setRunnerLocked(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
) {
	bundle.runners[queryGroup] = lifecycle
	bundle.setOwnedQueryGroupsLocked()
}

func (bundle *phaseTwoWorkerBundle) removeRunnerLocked(queryGroup execution.QueryGroupIdentity) {
	delete(bundle.runners, queryGroup)
	// Not owned here any more: its timeline is its next owner's to report.
	delete(bundle.unreadableTimelines, queryGroup)
	bundle.setOwnedQueryGroupsLocked()
	// A Query Group no longer owned is no longer rechecked. The lookback
	// never takes the bundle's lock under its own, so this order is safe.
	bundle.dependencies.Lookback.Forget(queryGroup)
}

// setOwnedQueryGroupsLocked follows every change to the owned Runner set, so
// it is also where the dispatcher's ordered view of that set is dropped.
func (bundle *phaseTwoWorkerBundle) setOwnedQueryGroupsLocked() {
	bundle.scheduledRunners = nil
	bundle.runnersRevision++
	if bundle.dependencies.Recorder != nil {
		bundle.dependencies.Recorder.SetOwnedQueryGroups(len(bundle.runners))
	}
}

func (bundle *phaseTwoWorkerBundle) updateReadiness() {
	bundle.mu.RLock()
	// A replica that has not yet read its Assignment holds none, which is not
	// the same as having been assigned none: startup that could not read it
	// waits for the tick that does, not ready meanwhile.
	// A declined Query Group still assigned here counts as accounted for: it
	// is let go on purpose and waits for a leader to place it elsewhere, and
	// reporting the replica not ready for it would move all the others too.
	assignmentReady := (bundle.assignmentRead || !bundle.dependencies.Config.HasRole(roles.Worker)) && !bundle.draining && !bundle.closed
	if assignmentReady {
		open := 0
		for queryGroup := range bundle.assigned {
			if _, running := bundle.runners[queryGroup]; running {
				open++
				continue
			}
			if _, declined := bundle.declined[queryGroup]; !declined {
				assignmentReady = false
				break
			}
		}
		assignmentReady = assignmentReady && open == len(bundle.runners)
	}
	controlDegraded := bundle.controlDegraded
	controlReason := bundle.controlReason
	factsSeen := bundle.controlFactsSeen
	dependencyDegraded := bundle.dependencyDegraded
	lastRecoveryAt := bundle.lastControlRecoveryAt
	sinkReady := bundle.outputSinkReady
	draining := bundle.draining || bundle.closed
	bundle.mu.RUnlock()
	if bundle.dependencies.Config.HasRole(roles.Control) {
		controlState := observability.HealthReady
		switch {
		case draining:
			controlState = observability.HealthDraining
		case !factsSeen || !sinkReady:
			controlState = observability.HealthNotReady
		case controlDegraded || dependencyDegraded:
			controlState = observability.HealthDegraded
		}
		bundle.dependencies.Health.controlState.Store(&controlState)
	}
	state := observability.HealthReady
	var reasons []observability.ReasonCode
	if !sinkReady {
		// Not ready, under the dependency's name: the replica is up and its
		// diagnostics answer, and the rollout waits on it instead of
		// terminating the replicas that can publish for one that cannot.
		state = observability.HealthNotReady
		reasons = []observability.ReasonCode{phaseTwoOutputSinkReason}
	} else if !factsSeen {
		// Not ready rather than degraded: degraded still answers the
		// readiness probe as ready, and a rollout that took that answer would
		// terminate the replica that does know the facts for one that does
		// not. The reason names the fact this replica is waiting for.
		state = observability.HealthNotReady
		reasons = []observability.ReasonCode{observability.ReasonInternalUnknown}
		if controlReason != "" {
			reasons = []observability.ReasonCode{controlReason}
		}
	} else if !assignmentReady {
		state = observability.HealthNotReady
		reasons = []observability.ReasonCode{observability.ReasonInternalUnknown}
	} else if controlDegraded || dependencyDegraded {
		state = observability.HealthDegraded
		if controlDegraded {
			reasons = append(reasons, controlReason)
		}
	}
	if dependencyDegraded {
		reasons = append(reasons, phaseTwoControlDependencyReason)
	}
	bundle.dependencies.Health.Update(phaseTwoReadiness{
		State: state, Reasons: reasons, SnapshotReady: true, AssignmentReady: assignmentReady,
		RuntimeStateReady: true, OutputSinkReady: sinkReady, LastRecoveryAt: lastRecoveryAt,
	})
	bundle.publishControlCandidacy()
}

// phaseTwoOutputSinkReason is the readiness reason while the output sink is
// not open. Kafka's own code, because that is the dependency that did not
// answer; the endpoint list carries what it said.
var phaseTwoOutputSinkReason = observability.ReasonCode(contract.ReasonKafkaUnavailable)

// outputSinkChanged is told by the lazy output sink after every attempt. It
// keeps the readiness fact and the worker registration in step with the sink:
// not ready and starting while it is closed, ready on the first renewal after
// it opens.
func (bundle *phaseTwoWorkerBundle) outputSinkChanged(state outputSinkState) {
	bundle.mu.Lock()
	changed := bundle.outputSinkReady != state.Ready
	bundle.outputSinkReady = state.Ready
	bundle.mu.Unlock()
	if !changed {
		return
	}
	bundle.updateReadiness()
	if !state.Ready && state.LastFailure != "" {
		bundle.observe(context.Background(), observability.ComponentRuntime, observability.StageStartup,
			observability.ResultDegraded, errors.New("output sink is not open: "+state.LastFailure))
	}
}

// noteTimelineReading keeps the unreadable-timeline names in step with what
// this replica's rounds meet. A round that asked for a Slot and met a
// timeline that does not decode (SCHEDULE_UNREADABLE), or a timeline key
// that is not there at all (SCHEDULE_MISSING), names its Query Group;
// a later round of it that ran a Slot - the source read the timeline to
// freeze it - drops the name. A round that did not ask the source (not due,
// backing off) or that the source refused for another reason, before or
// beside the timeline, says nothing about the bytes and leaves the name as
// it is: dropping it there would have the name come and go between
// heartbeats while the timeline stayed unreadable. Only an owned Query
// Group is named.
func (bundle *phaseTwoWorkerBundle) noteTimelineReading(queryGroup execution.QueryGroupIdentity, attempted bool, outcome execution.SlotExecutionResult) {
	if !attempted {
		return
	}
	unreadable := outcome.ReasonCode == execution.ReasonCode(contract.ReasonScheduleUnreadable) ||
		outcome.ReasonCode == execution.ReasonCode(contract.ReasonScheduleMissing)
	if !unreadable && outcome.SourceRetry {
		return
	}
	bundle.mu.Lock()
	_, named := bundle.unreadableTimelines[queryGroup]
	_, owned := bundle.runners[queryGroup]
	switch {
	case unreadable && !named && owned:
		if bundle.unreadableTimelines == nil {
			bundle.unreadableTimelines = make(map[execution.QueryGroupIdentity]struct{})
		}
		bundle.unreadableTimelines[queryGroup] = struct{}{}
	case !unreadable && named:
		delete(bundle.unreadableTimelines, queryGroup)
	}
	bundle.mu.Unlock()
	if unreadable && !named && owned {
		bundle.dependencies.Recorder.RecordTimelineUnreadableReported()
	}
}

// unreadableTimelinesRegistration is the names the registration carries -
// the first ownership.MaxReportedUnreadableTimelines in Query Group order -
// and how many there are in all.
func (bundle *phaseTwoWorkerBundle) unreadableTimelinesRegistration() ([]string, int) {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	if len(bundle.unreadableTimelines) == 0 {
		return nil, 0
	}
	names := make([]string, 0, len(bundle.unreadableTimelines))
	for queryGroup := range bundle.unreadableTimelines {
		names = append(names, string(queryGroup))
	}
	sort.Strings(names)
	total := len(names)
	if total > ownership.MaxReportedUnreadableTimelines {
		names = names[:ownership.MaxReportedUnreadableTimelines]
	}
	return names, total
}

// refreshAndReconcile runs one control tick. Dependency failures never stop
// the Worker: they are scoped by scopeControlError, keep already-owned Query
// Groups running and are retried on the next tick. Only invariant violations
// and cancellation are returned to Run.
// declineHungExecutions finds the executions still running past their own
// deadline by executionPastDeadlineGrace without having begun to commit - an
// execution that ignored its cancellation, which only a code bug produces -
// and lets each one's Query Group go (design 02 section 6.5): the execution
// is claimed so it writes nothing more, the lifecycle is detached so nothing
// starts on it, its lease is released at once without waiting on it, and the
// Query Group is declined - named on this replica's registration so a leader
// places it elsewhere and never back here - until the hung execution
// returns.
//
// The deadline is the one the execution runs under (DeclineHung): a normal
// Slot's query deadline, a replay's, retry's or probe's derived from its own
// arrival - never the frozen Slot's first-attempt deadline, which a replay of
// a taken-over Slot is minutes past by design. Only the stages before the
// commit are judged, where past its deadline an execution has only its
// evaluation left, which the grace (the liveness probe's minute) covers. An
// execution that has begun to write output is never declined: its State and
// Progress writes run under their own bounded timeouts, and releasing the
// lease between them would leave acknowledged output unapplied.
func (bundle *phaseTwoWorkerBundle) declineHungExecutions(now time.Time) {
	type hung struct {
		queryGroup execution.QueryGroupIdentity
		entry      *declinedQueryGroup
	}
	var found []hung
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return
	}
	for queryGroup, lifecycle := range bundle.runners {
		if lifecycle.inflight == 0 {
			continue
		}
		// An execution with no deadline of its own yet - in its source, or
		// a recovery before access derived its deadline - is not judged: its
		// reads are bounded by their clients.
		stage, deadline, claimed := lifecycle.runner.DeclineHung(now, executionPastDeadlineGrace)
		if !claimed {
			continue
		}
		entry := &declinedQueryGroup{lifecycle: lifecycle, stage: stage, deadline: deadline, since: now}
		bundle.removeRunnerLocked(queryGroup)
		if bundle.declined == nil {
			bundle.declined = make(map[execution.QueryGroupIdentity]*declinedQueryGroup)
		}
		bundle.declined[queryGroup] = entry
		bundle.maintenanceWG.Add(1)
		found = append(found, hung{queryGroup, entry})
	}
	bundle.mu.Unlock()
	for _, hung := range found {
		go bundle.declineQueryGroup(hung.queryGroup, hung.entry)
	}
}

// declineQueryGroup lets go of a Query Group whose execution is hung here:
// renewal stops, the lease is released without waiting on the Slot, and the
// registration is written at once so the leader learns of the decline on its
// next round rather than at the next renewal.
func (bundle *phaseTwoWorkerBundle) declineQueryGroup(queryGroup execution.QueryGroupIdentity, entry *declinedQueryGroup) {
	defer bundle.maintenanceWG.Done()
	bound := bundle.dependencies.Config.ShutdownTimeout.Duration()
	lifecycle := entry.lifecycle
	lifecycle.cancel()
	select {
	case <-lifecycle.done:
	case <-time.After(bound):
	}
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	releaseErr := lifecycle.runner.Release(ctx)
	bundle.recordHungExecution(queryGroup, entry, observability.ExecutionHungDeclined, releaseErr)
	if err := bundle.register(ctx, ownership.WorkerReady); err != nil {
		bundle.markControlDependencyDegraded()
	}
	bundle.updateReadiness()
}

// recordHungExecution counts one hung execution's decline or return and
// writes its line: which Query Group, the stage it is stuck in, how far past
// its deadline, and on return how long it was hung.
func (bundle *phaseTwoWorkerBundle) recordHungExecution(
	queryGroup execution.QueryGroupIdentity,
	entry *declinedQueryGroup,
	outcome string,
	err error,
) {
	now := bundle.dependencies.Now()
	facts := observability.ExecutionHungFacts{Outcome: outcome, Stage: entry.stage,
		DeadlineUnixMilli: entry.deadline.UnixMilli(), PastDeadlineMS: now.Sub(entry.deadline).Milliseconds()}
	if outcome == observability.ExecutionHungReturned {
		facts.HungMS = now.Sub(entry.since).Milliseconds()
	}
	bundle.dependencies.Recorder.RecordExecutionHung(outcome)
	result := observability.Result(observability.ResultFailed)
	if outcome == observability.ExecutionHungReturned {
		result = observability.ResultSuccess
	}
	observeRuntime(context.Background(), bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageExecutionHung,
		Result: result, Direction: observability.DirectionInternal, ExecutionHung: &facts, Err: err,
		Trace: observability.TraceFields{QueryGroupKey: string(queryGroup), OwnerID: bundle.dependencies.Config.PhaseTwo.Worker.ID},
	})
}

// phaseTwoDispatcherFanout is how many execution slots the dispatcher runs:
// one goroutine each, taking Slots off the queue. A bundle that sizes its
// shape from its work runs the container's ceiling and offers work only
// while fewer than the derived bound execute; one that does not runs the
// configured limit. The limit is derived from the container and validated
// positive, so the floor of one is reached only by a Config assembled field
// by field in a test. There is no unlimited fanout to fall back to.
func phaseTwoDispatcherFanout(bundle *phaseTwoWorkerBundle) int {
	schedulerConfig := bundle.dependencies.Config.PhaseTwo.Scheduler
	fanout := max(schedulerConfig.ActiveExecutionLimit, 1)
	if bundle.shaper != nil {
		fanout = max(schedulerConfig.ActiveExecutionCeiling, fanout)
	}
	if schedulerConfig.ReadyQueueCapacity > 0 && schedulerConfig.ReadyQueueCapacity < fanout && bundle.shaper == nil {
		fanout = schedulerConfig.ReadyQueueCapacity
	}
	return fanout
}

// declineFleetFacts is what this replica publishes of its declines, from
// the same set its registration names (declinedRegistration), with the
// execution slots the dispatcher runs: each decline holds one until its
// hung execution returns. Nil while it declines none.
func (bundle *phaseTwoWorkerBundle) declineFleetFacts() *fleet.DeclineFacts {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	if len(bundle.declined) == 0 {
		return nil
	}
	declined := make([]fleet.DeclinedQueryGroup, 0, len(bundle.declined))
	for queryGroup, entry := range bundle.declined {
		declined = append(declined, fleet.DeclinedQueryGroup{QueryGroup: string(queryGroup), Stage: entry.stage, Since: entry.since.UTC()})
	}
	sort.Slice(declined, func(left, right int) bool { return declined[left].QueryGroup < declined[right].QueryGroup })
	return &fleet.DeclineFacts{Fanout: phaseTwoDispatcherFanout(bundle), Total: len(declined),
		QueryGroups: declined[:min(len(declined), fleet.MaxDeclineSamples)]}
}

// declinedRegistration is the declined set as the registration names it,
// in Query Group order.
func (bundle *phaseTwoWorkerBundle) declinedRegistration() []ownership.DeclinedQueryGroup {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	if len(bundle.declined) == 0 {
		return nil
	}
	declined := make([]ownership.DeclinedQueryGroup, 0, len(bundle.declined))
	for queryGroup, entry := range bundle.declined {
		declined = append(declined, ownership.DeclinedQueryGroup{QueryGroup: string(queryGroup), Stage: entry.stage})
	}
	sort.Slice(declined, func(left, right int) bool { return declined[left].QueryGroup < declined[right].QueryGroup })
	return declined
}

func (bundle *phaseTwoWorkerBundle) refreshAndReconcile(ctx context.Context, refresh bool) error {
	bundle.declineHungExecutions(bundle.dependencies.Now())
	if bundle.shaper.due() {
		bundle.mu.RLock()
		owned := len(bundle.runners)
		bundle.mu.RUnlock()
		bundle.shaper.derive(owned)
	}
	failureSeq := bundle.controlDependencyFailureSeq()
	var queryGroups []execution.QueryGroupIdentity
	var err error
	bundle.mu.RLock()
	factsSeenBefore := bundle.controlFactsSeen
	leadingBefore := bundle.controlLeader
	bundle.mu.RUnlock()
	// A candidate contends on every reconcile tick, not only on the refresh
	// tick, so a lease that ran out or was given back has a holder within one
	// reconcile interval. A tick that takes it runs as a refresh: a new term
	// starts by reading its source. Not knowing the lease is not a reason to
	// skip the rest of the tick, which is this replica's own Assignment.
	if !refresh && !leadingBefore && bundle.candidacy != "" {
		acquired, acquireErr := bundle.tryAcquireControlLeader(ctx)
		if acquireErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			bundle.dependencies.Recorder.RecordControlFactUnavailable("leader_lease", "read_failed")
		}
		refresh = acquired
	}
	if refresh {
		leader, acquireErr := bundle.tryAcquireControlLeader(ctx)
		if acquireErr != nil {
			return bundle.scopeControlError(ctx, observability.ComponentControlPlane, observability.StageSnapshotUnavailable, acquireErr)
		}
		if leader {
			var result phaseTwoControlRefreshResult
			result, err = bundle.dependencies.Control.Refresh(ctx)
			if err == nil {
				err = bundle.applyControlRefresh(ctx, result)
			}
		} else {
			var result phaseTwoControlRefreshResult
			result, err = bundle.dependencies.Control.LoadActive(ctx)
			if err == nil {
				err = bundle.applyFollowerControlLoad(ctx, result)
			}
		}
		// Read back what was applied rather than what the round returned. The
		// two differ on a round that could not read the active set: it keeps
		// the set the replica already had, and publishing the round's empty
		// one would take every Query Group off the fleet on a round that
		// learned nothing.
		if err == nil {
			bundle.mu.RLock()
			queryGroups = append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
			bundle.mu.RUnlock()
		}
		// Every replica reads the persisted success time on the refresh
		// tick, whatever its role and however the tick went: the age it
		// yields has to keep rising on a replica whose own rounds stopped.
		bundle.readPersistedSourceSuccess(ctx)
	} else {
		bundle.mu.RLock()
		queryGroups = append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
		bundle.mu.RUnlock()
	}
	if err != nil {
		if !errors.Is(err, controlplane.ErrSnapshotUnavailable) && !errors.Is(err, controlplane.ErrActivationUnavailable) {
			return bundle.scopeControlError(ctx, observability.ComponentControlPlane, observability.StageSnapshotUnavailable, err)
		}
		// The round could not read the active set. The set this replica
		// already runs is kept as it is (QueryGroupsRetained), under a reason
		// that names which fact was unreadable; a replica that never had one
		// keeps having none and stays registered as starting.
		if applyErr := bundle.applyControlRefresh(ctx, degradedControlFacts(err)); applyErr != nil {
			return applyErr
		}
		bundle.updateReadiness()
		return nil
	}
	bundle.mu.RLock()
	leader := bundle.controlLeader
	factsSeenNow := bundle.controlFactsSeen
	bundle.mu.RUnlock()
	if !factsSeenBefore && factsSeenNow {
		// The first round that named the Query Groups: the registration the
		// renewal loop keeps alive says starting, and the rendezvous only
		// places onto ready workers, so this replica joins the fleet on the
		// registration written here rather than on the next renewal. A failed
		// write is the renewal loop's to retry.
		if registerErr := bundle.register(ctx, ownership.WorkerReady); registerErr != nil {
			bundle.observeRegistrationRenewal(observability.ResultFailed, registerErr)
			bundle.markControlDependencyDegraded()
		}
	}
	if leader {
		if err := bundle.dependencies.Ownership.PublishAssignments(ctx, queryGroups, bundle.dependencies.Now()); err != nil {
			if !errors.Is(err, ownership.ErrStaleFence) {
				return bundle.scopeControlError(ctx, observability.ComponentOwnership, observability.StageAssignmentAcquired, err)
			}
			bundle.markControlFollower(err)
		} else if bundle.candidacy == ownership.ControlCandidacyFallback {
			// The round has published; a fallback Leader gives the lease back
			// here, between rounds, once a preferred process has been ready
			// for the whole window.
			bundle.considerHandback(ctx)
		}
	}
	if !bundle.dependencies.Config.HasRole(roles.Worker) {
		bundle.clearControlDependencyDegraded(ctx, failureSeq)
		bundle.updateReadiness()
		return nil
	}
	assigned, err := bundle.dependencies.Ownership.AssignedQueryGroups(ctx, queryGroups)
	if err != nil {
		return bundle.scopeControlError(ctx, observability.ComponentOwnership, observability.StageAssignmentAcquired, err)
	}
	if err := bundle.applyAssignment(ctx, assigned); err != nil {
		return err
	}
	bundle.clearControlDependencyDegraded(ctx, failureSeq)
	bundle.updateReadiness()
	return nil
}

// applyFollowerControlLoad takes a healthy activation read on a follower. A
// follower that was degraded because the activation was unreadable is
// recovered by the read succeeding again, through the same transition a
// leader reports; before this it stayed degraded until it became leader and
// refreshed, which on a deployment whose leader never changes is forever.
// A follower that was not degraded takes the Query Groups and records the
// round without reporting anything, as before.
func (bundle *phaseTwoWorkerBundle) applyFollowerControlLoad(ctx context.Context, result phaseTwoControlRefreshResult) error {
	bundle.mu.RLock()
	degraded := bundle.controlDegraded
	bundle.mu.RUnlock()
	if degraded || result.Status != phaseTwoControlHealthy || result.QueryGroupsRetained {
		return bundle.applyControlRefresh(ctx, result)
	}
	bundle.mu.Lock()
	bundle.queryGroups = append(bundle.queryGroups[:0], result.QueryGroups...)
	bundle.controlFactsSeen = true
	bundle.noteControlRoundLocked(result)
	bundle.mu.Unlock()
	return nil
}

// invalidControlHealthField names the field of a control refresh result this
// replica cannot act on, and reports whether there was one.
//
// The fields are checked in the order a reader would ask about them, and only
// the first wrong one is named: the fix starts there, and naming every wrong
// field would give the counter as many label values as there are ways for one
// round to be wrong.
func invalidControlHealthField(result phaseTwoControlRefreshResult) (string, bool) {
	if result.Status != phaseTwoControlHealthy && result.Status != phaseTwoControlDegradedLastGood {
		return "status", true
	}
	if result.Status == phaseTwoControlHealthy {
		if result.QueryGroupsRetained {
			// A round that could not read the active set has not established
			// that the deployment is healthy; it has established that it does
			// not know. Accepting the pair would let a replica report healthy
			// while serving a set nothing this round confirmed.
			return "query_groups", true
		}
		return "", false
	}
	switch {
	case result.SourceKind != observability.SourceKindLegacyStrategy &&
		result.SourceKind != observability.SourceKindCompiledSnapshot:
		return "source_kind", true
	case result.ReasonCode == "":
		return "reason_code", true
	case result.Cause == nil:
		return "cause", true
	}
	return "", false
}

// controlHealthFactLabel is the metric label of one applied health fact; the
// label set is closed in the recorder, so an unknown status counts as nothing
// rather than as a new series.
func controlHealthFactLabel(status phaseTwoControlRefreshStatus) string {
	switch status {
	case phaseTwoControlHealthy:
		return "healthy"
	case phaseTwoControlDegradedLastGood:
		return "degraded_last_good"
	default:
		return "invalid"
	}
}

func (bundle *phaseTwoWorkerBundle) applyControlRefresh(
	ctx context.Context,
	result phaseTwoControlRefreshResult,
) error {
	if field, invalid := invalidControlHealthField(result); invalid {
		// The refresh returned a health fact this replica cannot act on. That
		// is a defect in this program, and it used to end the process: the
		// round returned an invariant error, Run returned it, and the
		// container restarted -- on a Control Leader, in the one state where
		// only it could write the activation back.
		//
		// Keeping the previous fact is strictly better than exiting on every
		// count. The previous fact was true when it was applied, the Query
		// Groups this replica owns keep running under it, and the next round
		// is seconds away. What must not be lost is that it happened, so the
		// field that was wrong is named in the log and in a counter of its
		// own rather than inferred from a restart.
		bundle.dependencies.Recorder.RecordControlHealthFact("invalid")
		bundle.dependencies.Recorder.RecordControlHealthInvalid(field)
		observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultFailed, Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonInternalUnknown,
			Err:        fmt.Errorf("phase-two Control refresh returned an invalid health fact: %s", field),
		})
		return nil
	}
	bundle.dependencies.Recorder.RecordControlHealthFact(controlHealthFactLabel(result.Status))
	var transitionResult observability.Result
	var transitionSource observability.SourceKind
	var transitionReason observability.ReasonCode
	var transitionCause error
	bundle.mu.Lock()
	if !result.QueryGroupsRetained {
		bundle.queryGroups = append(bundle.queryGroups[:0], result.QueryGroups...)
		bundle.controlFactsSeen = true
	}
	bundle.noteControlRoundLocked(result)
	switch result.Status {
	case phaseTwoControlDegradedLastGood:
		if !bundle.controlDegraded {
			bundle.controlDegraded = true
			bundle.controlSourceKind = result.SourceKind
			bundle.controlReason = result.ReasonCode
			transitionResult = observability.ResultDegraded
			transitionSource = result.SourceKind
			transitionReason = result.ReasonCode
			transitionCause = result.Cause
		}
	case phaseTwoControlHealthy:
		if bundle.controlDegraded {
			transitionResult = observability.Result(observability.ResultRecovered)
			transitionSource = bundle.controlSourceKind
			transitionReason = bundle.controlReason
			bundle.controlDegraded = false
			bundle.lastControlRecoveryAt = bundle.dependencies.Now()
			bundle.controlSourceKind = ""
			bundle.controlReason = ""
		} else if !result.SourceRefreshObserved {
			transitionResult = observability.ResultSuccess
		}
	}
	bundle.mu.Unlock()

	if transitionResult == "" {
		return nil
	}
	stage := observability.Stage(observability.StageSnapshotRefreshed)
	if transitionResult == observability.ResultDegraded {
		stage = observability.Stage(observability.StageSnapshotUnavailable)
	}
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: stage, Result: transitionResult,
		Direction: observability.DirectionInternal, ReasonCode: transitionReason, Err: transitionCause,
		SourceKind: transitionSource,
	})
	return nil
}

func (bundle *phaseTwoWorkerBundle) markControlFollower(err error) {
	bundle.mu.Lock()
	bundle.controlLeader = false
	bundle.setControlRoleLocked(observability.ControlSourceRoleFollower)
	if bundle.cancelControl != nil {
		bundle.cancelControl()
	}
	bundle.mu.Unlock()
	bundle.observe(context.Background(), observability.ComponentControlPlane, observability.StageSnapshotUnavailable, observability.ResultFailed, err)
}

// markOwnershipUnsafe is reserved for a phase-two invariant violation in
// registration maintenance. Transient Ownership Store failures never reach
// it; they are retried by maintainRegistration.
func (bundle *phaseTwoWorkerBundle) markOwnershipUnsafe(err error) {
	bundle.mu.RLock()
	sinkReady := bundle.outputSinkReady
	bundle.mu.RUnlock()
	bundle.dependencies.Health.Update(phaseTwoReadiness{
		State: observability.HealthNotReady, Reasons: []observability.ReasonCode{observability.ReasonInternalUnknown},
		SnapshotReady: true, RuntimeStateReady: true, OutputSinkReady: sinkReady,
	})
	bundle.observe(context.Background(), observability.ComponentOwnership, observability.StageAssignmentLost, observability.ResultFailed, err)
}

func (bundle *phaseTwoWorkerBundle) observe(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	result observability.Result,
	err error,
) {
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: component, Stage: stage, Result: result,
		Direction: observability.DirectionInternal, Err: err,
	})
}

// observeRegistrationRenewal reports one failed READY registration renewal
// attempt, or the recovery after such failures. The registration is the
// Worker's membership lease in the Ownership Store, so it reuses the
// lease_renewed stage. Failures carry the shared retryable dependency reason
// so the bounded log policy folds repeats into one limited bucket. It is a
// Worker-level event and deliberately carries no Query Group or owner
// identity.
func (bundle *phaseTwoWorkerBundle) observeRegistrationRenewal(result observability.Result, err error) {
	reason := observability.ReasonNone
	if err != nil {
		reason = phaseTwoControlDependencyReason
	}
	observeRuntime(context.Background(), bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageLeaseRenewed, Result: result,
		Direction: observability.DirectionInternal, ReasonCode: reason, Err: err,
	})
}

func (bundle *phaseTwoWorkerBundle) observeOwnership(
	ctx context.Context,
	stage observability.Stage,
	result observability.Result,
	queryGroup execution.QueryGroupIdentity,
	err error,
) {
	reason := ownershipObservationReason(err)
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: stage, Result: result,
		Operation: observability.OperationTransition, Direction: observability.DirectionInternal,
		ReasonCode: reason, Trace: observability.TraceFields{
			QueryGroupKey: string(queryGroup), OwnerID: bundle.dependencies.Config.PhaseTwo.Worker.ID,
		}, Err: err,
	})
}

// ownershipObservationReason is the reason an ownership observation carries
// for err: none for no error, the store's own refusal word for one of its
// four refusals, internal_unknown for anything else. Every ownership site
// used to say internal_unknown for all four, and which refusal a deployment
// was seeing could only be read off the error text of a rate-limited log.
func ownershipObservationReason(err error) observability.ReasonCode {
	if err == nil {
		return observability.ReasonNone
	}
	if reason, ok := ownership.RefusalReason(err); ok {
		return observability.ReasonCode(reason)
	}
	return observability.ReasonInternalUnknown
}

func phaseTwoRuntimeObserver(observer observability.Observer) observability.Observer {
	if observer == nil {
		return observability.NopObserver{}
	}
	return observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
		observeRuntime(ctx, observer, observation)
		if observation.Component != observability.ComponentState || observation.Stage != observability.StageSideEffectAdmission {
			return
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageFenceChecked,
			Result: observation.Result, Operation: observation.Operation, Direction: observability.DirectionInternal,
			ReasonCode: observation.ReasonCode, Duration: observation.Duration, Trace: observation.Trace, Err: observation.Err,
		})
	})
}

func phaseTwoCapabilitiesDigest(cfg config.Config) (string, error) {
	return contract.DeriveCanonicalDigestV2("alarmd-phase-two-worker-capabilities-v1", struct {
		SchemaVersion     string                           `json:"schema_version"`
		AlgorithmRegistry string                           `json:"algorithm_registry"`
		Limits            config.LimitsConfig              `json:"limits"`
		Coordinator       config.PhaseTwoCoordinatorConfig `json:"coordinator"`
	}{
		SchemaVersion: schemaVersion, AlgorithmRegistry: strategy.NewDefaultAlgorithmCompilerRegistry().CapabilityDigest(),
		Limits: cfg.Limits, Coordinator: cfg.PhaseTwo.Coordinator,
	})
}

func waitPhaseTwoGroup(ctx context.Context, group *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type phaseTwoReadiness struct {
	State             observability.HealthState
	Reasons           []observability.ReasonCode
	SnapshotReady     bool
	AssignmentReady   bool
	RuntimeStateReady bool
	OutputSinkReady   bool
	LastRecoveryAt    time.Time
}

type phaseTwoApplicationHealth struct {
	tracker       *observability.HealthTracker
	selectedRoles roles.Set
	channelState  atomic.Pointer[observability.HealthState]
	controlState  atomic.Pointer[observability.HealthState]
	// candidacyReady is the bundle's word on whether this process may
	// contend for the Control Leader lease, for the instance record. False
	// until the bundle has started, and again once it drains.
	candidacyReady atomic.Bool
	// candidacyReadySince is the record writer's word back: when this
	// incarnation's record first said ready, in Unix milliseconds, or 0
	// while it does not. The bundle judges its own stability by it, so the
	// process and the fallbacks that read its record judge by one time.
	candidacyReadySince atomic.Int64
}

func newPhaseTwoApplicationHealth() *phaseTwoApplicationHealth {
	health := &phaseTwoApplicationHealth{tracker: observability.NewHealthTracker(observability.HealthSnapshot{})}
	health.Update(phaseTwoReadiness{State: observability.HealthStarting})
	return health
}

func (h *phaseTwoApplicationHealth) Update(readiness phaseTwoReadiness) {
	if h == nil || h.tracker == nil {
		return
	}
	h.tracker.Update(observability.HealthSnapshot{
		State: readiness.State, Reasons: append([]observability.ReasonCode(nil), readiness.Reasons...),
		ConfigLoaded: true, SchemaReady: true, PhaseTwo: true, SnapshotReady: readiness.SnapshotReady,
		AssignmentReady: readiness.AssignmentReady, RuntimeStateReady: readiness.RuntimeStateReady,
		OutputSinkReady: readiness.OutputSinkReady, LastRecoveryAt: readiness.LastRecoveryAt,
	})
}

func (h *phaseTwoApplicationHealth) HealthSnapshot() observability.HealthSnapshot {
	if h == nil || h.tracker == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{PhaseTwo: true})
	}
	snapshot := observability.NormalizeHealthSnapshot(h.tracker.HealthSnapshot())
	if len(h.selectedRoles) != 0 {
		snapshot.RoleReadiness = make(map[string]string, len(h.selectedRoles))
		for _, role := range h.selectedRoles {
			state := snapshot.State
			if role == roles.Control {
				state = h.controlReadiness(snapshot.State)
			}
			if role == roles.Channel && state != observability.HealthFatal && state != observability.HealthDraining {
				state = h.channelReadiness()
			}
			snapshot.RoleReadiness[string(role)] = string(state)
		}
	}
	return snapshot
}

func (h *phaseTwoApplicationHealth) channelReadiness() observability.HealthState {
	if state := h.channelState.Load(); state != nil {
		return *state
	}
	return observability.HealthStarting
}

func (h *phaseTwoApplicationHealth) controlReadiness(overall observability.HealthState) observability.HealthState {
	if overall == observability.HealthFatal || overall == observability.HealthDraining {
		return overall
	}
	if state := h.controlState.Load(); state != nil {
		return *state
	}
	return observability.HealthStarting
}

func diagnosticTimeMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// The dispatcher loop is the sole publisher, so a delayed observer can never
// roll the reported occupancy backward and no observer call is on a Runner's
// own return path. It publishes after handling the previous event and again
// before every select, so the snapshot is never older than one loop iteration.
func (dispatcher *phaseTwoRunnerDispatcher) observeOccupancy(ctx context.Context) {
	observeRuntime(ctx, dispatcher.bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageDispatcherSnapshot,
		Result: observability.ResultSuccess, Dispatcher: &observability.DispatcherFacts{
			Active: int(dispatcher.executing.Load()), Ready: len(dispatcher.normal),
			Delayed: len(dispatcher.delayed), QueuesKnown: true,
		},
	})
}

// changeExecuting records that one Runner invocation started or returned. It
// must stay free of the observer: a Runner that has to publish its own return
// before releasing its occupancy would report that publication wait as
// occupancy, and would serialise every Runner behind one observer call.
func (dispatcher *phaseTwoRunnerDispatcher) changeExecuting(delta int) {
	dispatcher.executing.Add(int64(delta))
}

// httpRuntime is the diagnostics and metrics listener. It is an interface so a
// test can run the application without binding a port.
type httpRuntime interface {
	Run(context.Context, string, time.Duration) error
	// SetAPI installs the observability API once the runtime that produces the
	// object facts is open. The listener starts before that runtime does.
	SetAPI(http.Handler)
	// SetGRPC installs the control stream the same way.
	SetGRPC(http.Handler)
	// SetLiveness installs what /healthz judges, once the loops it judges
	// exist.
	SetLiveness(httpservice.LivenessSource)
	// SetPublicSurfaceRestricted settles the public surface once the CLI is
	// built: restricted only when a session can be had.
	SetPublicSurfaceRestricted(bool)
}

// waitRuntimeComponent waits for one component's shutdown to report, up to the
// shared deadline. A component that has not reported by then is not waited for
// again: the deadline is the whole shutdown's, not each component's.
func waitRuntimeComponent(done <-chan error, deadline time.Time) error {
	wait := time.Until(deadline)
	if wait <= 0 {
		select {
		case err := <-done:
			return err
		default:
			return ErrApplicationShutdownTimeout
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return ErrApplicationShutdownTimeout
	}
}

var (
	ErrApplicationShutdownTimeout = errors.New("alarmd runtime: shutdown timeout")
	errHTTPServiceStopped         = errors.New("alarmd runtime: HTTP service stopped unexpectedly")
)

// normalizeRuntimeShutdownError drops the cancellation the shutdown itself
// caused and keeps every other stop as an error, so a component that stopped
// on its own before shutdown began is not reported as a clean exit.
func normalizeRuntimeShutdownError(err error, stoppedBeforeShutdown bool) error {
	if err == nil || (errors.Is(err, context.Canceled) && !stoppedBeforeShutdown) {
		return nil
	}
	return fmt.Errorf("runtime component: %w", err)
}

// schedulerShape is the scheduler shape in force: the one the shaper last
// derived, or the startup shape from the configuration.
func (bundle *phaseTwoWorkerBundle) schedulerShape() config.DerivedScheduler {
	if bundle.shaper != nil {
		return bundle.shaper.shape()
	}
	s := bundle.dependencies.Config.PhaseTwo.Scheduler
	return config.DerivedScheduler{ActiveExecutions: s.ActiveExecutionLimit, ProcessQueryPermits: s.ProcessQueryPermits,
		RecoveryQueryPermits: s.RecoveryQueryPermits, ReadyQueueCapacity: s.ReadyQueueCapacity,
		RecoveryQueueCapacity: s.RecoveryQueueCapacity, ActiveExecutionCeiling: max(s.ActiveExecutionCeiling, s.ActiveExecutionLimit)}
}

// slotsFull is whether every execution slot in force is in use: the
// dispatcher runs the ceiling's worth of goroutines and offers work only
// while fewer than the derived bound execute.
func (dispatcher *phaseTwoRunnerDispatcher) slotsFull() bool {
	return len(dispatcher.active) >= dispatcher.bundle.schedulerShape().ActiveExecutions
}
