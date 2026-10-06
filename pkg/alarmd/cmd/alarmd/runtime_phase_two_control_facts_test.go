// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// The rule these tests pin: once a replica is serving, no result of a
// control-plane read may end the process. The worst a read can do is leave
// the replica degraded under a named reason, keeping the last fact it had.
//
// The shape that broke it: a store reload that came back without the
// activation record. The Control Leader's refresh returned a zero result with
// a nil error for it, the bundle read that as a health fact it could not act
// on and returned an invariant error, and the process exited -- four times on
// the one replica able to write the record back. A follower starting in that
// window exited on its first read.

var activationMissingReason = observability.ReasonCode(contract.ReasonActivationMissing)

func newProductionControlWithRecorder(
	t *testing.T,
	reconciler *fakeSourceReconciler,
	repository *fakeProductionCatalogRepository,
	activator *fakeInitialScheduleActivator,
	observations *[]observability.Observation,
) (*productionPhaseTwoControl, *metric.Recorder) {
	t.Helper()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress: &fakeProductionProgressReader{}, RefreshInterval: time.Second, Recorder: recorder,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			*observations = append(*observations, observation)
		}),
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoControl() error = %v", err)
	}
	return control, recorder
}

// A pending round on a Control Leader whose store has no activation record,
// but does have a published Catalog, rebuilds the activation from the latest
// publication: the same idempotent first activation a fresh deployment makes.
// The round returns a healthy fact naming every Query Group as added, says so
// in its observation, and counts the record as missing once and rebuilt once.
func TestProductionPhaseTwoControlRebuildsAMissingActivationFromTheLatestPublication(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-1", Latest: publication},
	}}
	repository := &fakeProductionCatalogRepository{activationErr: controlplane.ErrActivationUnavailable,
		snapshot: controlplane.PublishedSnapshot{Publication: publication,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1"}}}}
	activator := &fakeInitialScheduleActivator{state: controlplane.ActivationState{RecordRevision: 1, Current: publication}}
	var observations []observability.Observation
	control, recorder := newProductionControlWithRecorder(t, reconciler, repository, activator, &observations)

	result, err := control.Refresh(context.Background())
	if err != nil || result.Status != phaseTwoControlHealthy || result.QueryGroupsRetained ||
		!reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("Refresh() = %#v, %v, want a healthy fact naming query-group-1", result, err)
	}
	if activator.calls != 1 || activator.publication != publication {
		t.Fatalf("activator calls/publication = %d/%+v, want one activation of the latest publication", activator.calls, activator.publication)
	}
	pending := sourceRefreshObservations(observations, observability.SourceRefreshPending)
	if len(pending) != 1 {
		t.Fatalf("pending observations = %d, want 1", len(pending))
	}
	facts := pending[0].SourceRefresh
	if !facts.ActivationRebuilt || !facts.ActivationCaughtUp || !facts.CountsKnown ||
		facts.AddedQueryGroups != 1 || facts.NewQueryGroups != 1 || facts.OldQueryGroups != 0 {
		t.Fatalf("source refresh facts = %+v, want rebuilt+caught up with every Query Group added", facts)
	}
	for name, labels := range map[string]map[string]string{
		"bkmonitor_alarmd_control_facts_unavailable_total": {"fact": "activation", "reason": "missing"},
		"bkmonitor_alarmd_control_facts_rebuilt_total":     {"fact": "activation"},
	} {
		if got := counterValue(t, recorder, name, labels); got != 1 {
			t.Fatalf("%s%v = %v, want 1", name, labels, got)
		}
	}
}

// The same round with nothing published yet has nothing to rebuild from. It
// used to return the zero result with a nil error; it now returns a degraded
// fact that names the missing activation and asks the receiver to keep the
// set it has, and counts the record as missing without counting a rebuild.
func TestProductionPhaseTwoControlNamesAMissingActivationItCannotRebuild(t *testing.T) {
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-1"},
	}}
	repository := &fakeProductionCatalogRepository{activationErr: controlplane.ErrActivationUnavailable}
	activator := &fakeInitialScheduleActivator{}
	var observations []observability.Observation
	control, recorder := newProductionControlWithRecorder(t, reconciler, repository, activator, &observations)

	result, err := control.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() error = %v, want a degraded fact rather than an error", err)
	}
	if result.Status != phaseTwoControlDegradedLastGood || !result.QueryGroupsRetained ||
		result.ReasonCode != activationMissingReason || !errors.Is(result.Cause, controlplane.ErrActivationUnavailable) ||
		result.SourceKind != observability.SourceKindCompiledSnapshot {
		t.Fatalf("Refresh() = %#v, want degraded_last_good/ACTIVATION_MISSING with the set retained", result)
	}
	if activator.calls != 0 {
		t.Fatalf("activator called %d times with nothing published", activator.calls)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_control_facts_unavailable_total", map[string]string{"fact": "activation", "reason": "missing"}); got != 1 {
		t.Fatalf("control_facts_unavailable_total{activation,missing} = %v, want 1", got)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_control_facts_rebuilt_total", map[string]string{"fact": "activation"}); got != 0 {
		t.Fatalf("control_facts_rebuilt_total{activation} = %v, want 0", got)
	}
}

// The structural guard behind the two tests above: a round that says nothing
// and claims success is turned into a named error, never handed to the bundle
// as a fact. Reverting the pending branch to its early return lands here.
func TestCompleteControlResultRefusesAZeroResultWithNoError(t *testing.T) {
	if _, err := completeControlResult(phaseTwoControlRefreshResult{}, nil); !errors.Is(err, errIncompleteControlResult) {
		t.Fatalf("completeControlResult(zero, nil) error = %v, want %v", err, errIncompleteControlResult)
	}
	healthy := phaseTwoControlRefreshResult{Status: phaseTwoControlHealthy}
	if result, err := completeControlResult(healthy, nil); err != nil || result.Status != phaseTwoControlHealthy {
		t.Fatalf("completeControlResult(healthy, nil) = %#v, %v", result, err)
	}
	failed := errors.New("store unreachable")
	if _, err := completeControlResult(phaseTwoControlRefreshResult{}, failed); !errors.Is(err, failed) {
		t.Fatalf("completeControlResult(zero, err) error = %v, want the round's own error", err)
	}
}

func newBundleWithRecorder(
	t *testing.T,
	health *phaseTwoApplicationHealth,
	control phaseTwoControlRuntime,
	owner phaseTwoOwnershipRuntime,
) (*phaseTwoWorkerBundle, *metric.Recorder) {
	t.Helper()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: validGoAccessRuntimeConfig(), Health: health, Control: control, Ownership: owner,
		Observer: observability.NopObserver{}, Now: time.Now, Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	return bundle, recorder
}

func lastRegistration(owner *fakePhaseTwoOwnership) ownership.AssignmentReadiness {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.registrations) == 0 {
		return ""
	}
	return owner.registrations[len(owner.registrations)-1].AssignmentReadiness
}

// A follower that starts while the activation record is missing does not
// exit. It stays registered as starting -- a ready registration would let the
// rendezvous hand it Query Groups it cannot name -- reports not ready under
// the missing fact's name, and joins on the first tick that reads the facts,
// registering as ready on that tick rather than on the next renewal.
func TestPhaseTwoWorkerBundleFollowerWaitsForControlFactsInsteadOfExiting(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{
		queryGroups: []execution.QueryGroupIdentity{queryGroup}, loadActiveErr: controlplane.ErrActivationUnavailable,
	}
	owner := &fakePhaseTwoOwnership{follower: true, assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	bundle, recorder := newBundleWithRecorder(t, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start(activation missing) error = %v, want the replica to wait rather than exit", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	snapshot := health.HealthSnapshot()
	if snapshot.State != observability.HealthNotReady || snapshot.Ready ||
		!reflect.DeepEqual(snapshot.Reasons, []observability.ReasonCode{activationMissingReason}) {
		t.Fatalf("health after start = %+v, want not_ready under ACTIVATION_MISSING", snapshot)
	}
	if got := lastRegistration(owner); got != ownership.WorkerStarting {
		t.Fatalf("registration after start = %s, want STARTING: a replica that cannot name the Query Groups must not be placed onto", got)
	}
	if len(bundle.runners) != 0 {
		t.Fatalf("runners after start = %d, want none", len(bundle.runners))
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_control_health_facts_total", map[string]string{"status": "degraded_last_good"}); got != 1 {
		t.Fatalf("control_health_facts_total{degraded_last_good} = %v, want 1", got)
	}

	control.loadActiveErr = nil
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile(facts readable) error = %v", err)
	}
	waitSignal(t, runner.leaseStarted, "query-group lease after the facts became readable")
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("health after recovery = %+v, want ready", snapshot)
	}
	if got := lastRegistration(owner); got != ownership.WorkerReady {
		t.Fatalf("registration after recovery = %s, want READY written on the tick that read the facts", got)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_control_health_facts_total", map[string]string{"status": "healthy"}); got != 1 {
		t.Fatalf("control_health_facts_total{healthy} = %v, want 1", got)
	}
}

// Every failed read of the starting control facts is treated the same way,
// not only the one shape an earlier version named: the replica waits under a
// retryable reason. This replaces the test that pinned the exit.
func TestPhaseTwoWorkerBundleDoesNotExitOnAnyInitialControlReadFailure(t *testing.T) {
	health := newPhaseTwoApplicationHealth()
	cause := errors.New("store unreachable")
	control := &fakePhaseTwoControl{beforeInitialRefresh: func() error { return cause }}
	owner := &fakePhaseTwoOwnership{}
	bundle, _ := newBundleWithRecorder(t, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start(initial control read failed) error = %v, want nil", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	snapshot := health.HealthSnapshot()
	if snapshot.State != observability.HealthNotReady || snapshot.Ready ||
		!reflect.DeepEqual(snapshot.Reasons, []observability.ReasonCode{phaseTwoControlDependencyReason}) {
		t.Fatalf("health after start = %+v, want not_ready under the dependency reason", snapshot)
	}
	if got := lastRegistration(owner); got != ownership.WorkerStarting {
		t.Fatalf("registration after start = %s, want STARTING", got)
	}
}

// A Control Leader that loses the activation record mid-run keeps the Query
// Groups it owns running under a named degraded fact; the next healthy round
// recovers it. An empty set is never applied on the round that learned
// nothing.
func TestPhaseTwoWorkerBundleLeaderKeepsItsQueryGroupsWhenTheActivationGoesMissing(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{
		queryGroups:   []execution.QueryGroupIdentity{queryGroup},
		refreshErrors: []error{controlplane.ErrActivationUnavailable},
		refreshResults: []phaseTwoControlRefreshResult{
			// The shape the production round returns when it cannot rebuild:
			// degraded, the set retained.
			{Status: phaseTwoControlDegradedLastGood, QueryGroupsRetained: true, SourceKind: observability.SourceKindCompiledSnapshot,
				ReasonCode: activationMissingReason, Cause: controlplane.ErrActivationUnavailable},
			{QueryGroups: []execution.QueryGroupIdentity{queryGroup}, Status: phaseTwoControlHealthy},
		},
	}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	bundle, recorder := newBundleWithRecorder(t, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	waitSignal(t, runner.leaseStarted, "query-group lease")

	for round, want := range []string{"the read that failed", "the degraded fact with the set retained"} {
		if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
			t.Fatalf("refreshAndReconcile(%s) error = %v, want the replica to keep running", want, err)
		}
		snapshot := health.HealthSnapshot()
		if snapshot.State != observability.HealthDegraded || !snapshot.Ready ||
			!reflect.DeepEqual(snapshot.Reasons, []observability.ReasonCode{activationMissingReason}) {
			t.Fatalf("round %d health = %+v, want degraded under ACTIVATION_MISSING", round, snapshot)
		}
		bundle.mu.RLock()
		groups, runners := append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...), len(bundle.runners)
		bundle.mu.RUnlock()
		if !reflect.DeepEqual(groups, []execution.QueryGroupIdentity{queryGroup}) || runners != 1 {
			t.Fatalf("round %d query groups/runners = %v/%d, want the owned set kept", round, groups, runners)
		}
	}
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile(healthy) error = %v", err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady {
		t.Fatalf("health after recovery = %+v, want ready", snapshot)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_control_health_facts_total", map[string]string{"status": "degraded_last_good"}); got != 2 {
		t.Fatalf("control_health_facts_total{degraded_last_good} = %v, want 2", got)
	}
}

// A refresh that returns a health fact the bundle cannot act on -- the exact
// shape that ended the process -- is counted by the field that was wrong and
// the previous fact is kept. A healthy fact that also asks to retain the set is
// the same defect under another field.
func TestPhaseTwoWorkerBundleKeepsTheLastHealthFactWhenARefreshReturnsAnInvalidOne(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{
		queryGroups: []execution.QueryGroupIdentity{queryGroup},
		refreshResults: []phaseTwoControlRefreshResult{
			{},
			{Status: phaseTwoControlHealthy, QueryGroupsRetained: true},
			{Status: phaseTwoControlDegradedLastGood, SourceKind: observability.SourceKindCompiledSnapshot, Cause: errors.New("x")},
		},
	}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	bundle, recorder := newBundleWithRecorder(t, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	waitSignal(t, runner.leaseStarted, "query-group lease")

	for _, field := range []string{"status", "query_groups", "reason_code"} {
		if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
			t.Fatalf("refreshAndReconcile(invalid %s) error = %v, want the previous fact kept", field, err)
		}
		if got := counterValue(t, recorder, "bkmonitor_alarmd_control_health_invalid_total", map[string]string{"field": field}); got != 1 {
			t.Fatalf("control_health_invalid_total{field=%s} = %v, want 1", field, got)
		}
		if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
			t.Fatalf("health after invalid %s = %+v, want the previous ready fact kept", field, snapshot)
		}
		bundle.mu.RLock()
		groups, runners := append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...), len(bundle.runners)
		bundle.mu.RUnlock()
		if !reflect.DeepEqual(groups, []execution.QueryGroupIdentity{queryGroup}) || runners != 1 {
			t.Fatalf("after invalid %s query groups/runners = %v/%d, want the owned set kept", field, groups, runners)
		}
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_control_health_facts_total", map[string]string{"status": "invalid"}); got != 3 {
		t.Fatalf("control_health_facts_total{invalid} = %v, want 3", got)
	}
}
