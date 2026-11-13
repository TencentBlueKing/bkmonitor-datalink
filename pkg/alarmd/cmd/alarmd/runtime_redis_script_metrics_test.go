// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// The binary links every script, so this is where a name given twice to one
// text panics, and where the whole table can be read.
func TestEveryScriptOfTheBinaryHasItsName(t *testing.T) {
	want := []string{
		"cli_slot_list_read",
		"cliauth_active", "cliauth_exchange", "cliauth_forget", "cliauth_issue", "cliauth_refresh",
		"cliauth_revoke_all", "cliauth_roster", "cliauth_roster_read", "cliauth_session", "cliauth_upgrade",
		"controlplane_cas_cutover_schedules", "controlplane_cas_initial_schedules", "controlplane_publish_audit",
		"controlplane_publish_snapshot", "controlplane_rebuild_activation_body", "controlplane_rebuild_activation_header",
		"controlplane_refresh_snapshot_publication", "controlplane_renew_current_activation_objects",
		"controlplane_write_catalog_manifest",
		"fleet_publish_summarized",
		"list_push_trim_expire",
		"ownership_acquire", "ownership_check_fence", "ownership_fence_clock_probe", "ownership_fenced_cas",
		"ownership_fenced_hash", "ownership_publish_assignment", "ownership_publish_assignment_index",
		"ownership_read_query_group_owner", "ownership_reclaim_assignment", "ownership_register_preferred_instance",
		"ownership_register_worker", "ownership_release", "ownership_renew",
		"ownership_worker_session_acquire", "ownership_worker_session_read", "ownership_worker_session_release", "ownership_worker_session_renew",
		"query_cooldown_save",
		"rollout_create", "rollout_put", "rollout_read", "rollout_release", "rollout_take",
		"sre_node_cancel", "sre_node_create", "sre_node_dispatch", "sre_node_replace",
		"state_apply_hash_delta", "state_compare_and_set", "state_discard_staged_hash", "state_fenced_batch_write",
		"state_record_hash_field", "state_renew_if_below", "state_stage_hash_segment", "state_swap_staged_hash",
	}
	if got := redisscript.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the binary's scripts = %v\nwant %v", got, want)
	}
	if len(want) > redisscript.MaxScripts {
		t.Fatalf("%d scripts, the bound is %d", len(want), redisscript.MaxScripts)
	}
}

type scriptReading struct {
	t        *testing.T
	recorder *metric.Recorder
}

func (reading scriptReading) samples(family string, labels map[string]string) []*dto.Metric {
	reading.t.Helper()
	families, err := reading.recorder.Gatherer().Gather()
	if err != nil {
		reading.t.Fatal(err)
	}
	var samples []*dto.Metric
	for _, candidate := range families {
		if candidate.GetName() != family {
			continue
		}
	next:
		for _, sample := range candidate.GetMetric() {
			for name, value := range labels {
				matched := false
				for _, label := range sample.GetLabel() {
					matched = matched || (label.GetName() == name && label.GetValue() == value)
				}
				if !matched {
					continue next
				}
			}
			samples = append(samples, sample)
		}
	}
	return samples
}

func (reading scriptReading) counter(family string, labels map[string]string) float64 {
	reading.t.Helper()
	total := 0.0
	for _, sample := range reading.samples(family, labels) {
		total += sample.GetCounter().GetValue()
	}
	return total
}

func (reading scriptReading) observations(family string, labels map[string]string) float64 {
	reading.t.Helper()
	total := 0.0
	for _, sample := range reading.samples(family, labels) {
		total += float64(sample.GetHistogram().GetSampleCount())
	}
	return total
}

func (reading scriptReading) renewals(lease, result, reason string) float64 {
	reading.t.Helper()
	labels := map[string]string{"lease": lease, "result": result}
	if reason != "" {
		labels["reason"] = reason
	}
	return reading.counter("alarmd_lease_renewal_total", labels)
}

// renewScriptAttempts is how many times the renewal script was tried: a cold
// cache's EVALSHA and the EVAL answering it are one attempt.
func (reading scriptReading) renewScriptAttempts() float64 {
	reading.t.Helper()
	counted := func(command, outcome string) float64 {
		return reading.counter("alarmd_redis_script_total", map[string]string{"script": "ownership_renew", "command": command, "outcome": outcome})
	}
	return counted("evalsha", "ok") + counted("eval", "ok") + counted("evalsha", "error") + counted("eval", "error")
}

func scriptMetricsClient(t *testing.T, server *redistest.Instance, recorder *metric.Recorder) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(recorder.RedisHook("runtime"))
	return client
}

// A batch of State writes is the script it carries, once, whatever is in front
// of it; the family that names commands reads it as its first member, evalsha.
func TestAStateBatchReadsUnderItsScriptAndItsCaller(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	client := scriptMetricsClient(t, server, recorder)
	backend, err := state.NewRedisBackendWithClient(server.Addr, redisForCaller(client, redisfailure.CallerRuntimeState))
	if err != nil {
		t.Fatal(err)
	}
	// A server holding the script, as it does but for the first batch after a
	// restart: the batch goes by digest alone, by a client the reading does not
	// count having left the script there.
	unread := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	t.Cleanup(func() { _ = unread.Close() })
	seeding, err := state.NewRedisBackendWithClient(server.Addr, unread)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seeding.CompareAndSetManyByDigest(context.Background(), nil, []state.FencedWrite{
		{Key: "seed", ExpectedMissing: true, Value: []byte("value"), TTL: time.Minute},
	}); err != nil {
		t.Fatal(err)
	}
	outcomes, err := backend.CompareAndSetManyByDigest(context.Background(), nil, []state.FencedWrite{
		{Key: "series-1", ExpectedMissing: true, Value: []byte("value-1"), TTL: time.Minute},
		{Key: "series-2", ExpectedMissing: true, Value: []byte("value-2"), TTL: time.Minute},
	})
	if err != nil || len(outcomes) != 2 || outcomes[0].Status != state.FencedWriteApplied {
		t.Fatalf("CompareAndSetManyByDigest() = (%+v, %v)", outcomes, err)
	}
	const script = "state_fenced_batch_write"
	for _, test := range []struct {
		command string
		want    float64
	}{{"load", 0}, {"evalsha", 1}, {"eval", 0}} {
		if got := reading.counter("alarmd_redis_script_total", map[string]string{"script": script, "command": test.command, "pipelined": "true", "outcome": "ok"}); got != test.want {
			t.Errorf("%s = %v, want %v", test.command, got, test.want)
		}
	}
	if got := reading.observations("alarmd_redis_script_duration_seconds", map[string]string{"script": script, "pipelined": "true"}); got != 1 {
		t.Errorf("the batch recorded %v times under its script", got)
	}
	if got := reading.observations("alarmd_redis_command_duration_seconds", map[string]string{"command": "evalsha", "pipelined": "true"}); got != 1 {
		t.Errorf("the command family reads the batch %v times under evalsha", got)
	}
	if got := reading.observations("alarmd_redis_caller_duration_seconds", map[string]string{"caller": redisfailure.CallerRuntimeState, "pipelined": "true"}); got != 1 {
		t.Errorf("the caller's batch recorded %v times", got)
	}
	if got := reading.counter("alarmd_redis_script_bytes_total", map[string]string{"script": script, "direction": "sent"}); got < float64(len("value-1")*2) {
		t.Errorf("the script sent %v bytes", got)
	}
}

// A caller is named by a hook added after the metric's, on a clone: it has to
// reach the metric all the same, and the two sums have to hold on real traffic.
func TestTheCallerReachesTheMetricAndTheSumsHold(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	ctx := context.Background()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	client := scriptMetricsClient(t, server, recorder)
	named := redisForCaller(client, redisfailure.CallerFleet)
	if err := named.Set(ctx, "key", "value", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := named.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		pipeline.Get(ctx, "key")
		pipeline.Get(ctx, "absent")
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		t.Fatal(err)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if got := reading.observations("alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime", "caller": redisfailure.CallerFleet, "pipelined": "false"}); got != 1 {
		t.Errorf("the named call recorded %v times", got)
	}
	if got := reading.observations("alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime", "caller": redisfailure.CallerFleet, "pipelined": "true"}); got != 1 {
		t.Errorf("the named batch recorded %v times", got)
	}
	if got := reading.observations("alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime", "caller": "unnamed", "pipelined": "false"}); got != 1 {
		t.Errorf("the unnamed call recorded %v times", got)
	}
	total := reading.observations("alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime"})
	if operations := reading.counter("alarmd_redis_operation_total", map[string]string{"client": "runtime"}); total != operations {
		t.Errorf("the caller family counts %v operations, redis_operation_total %v", total, operations)
	}
	namedCount := reading.observations("alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime", "caller": redisfailure.CallerFleet})
	if operations := reading.counter("alarmd_redis_caller_operation_total", map[string]string{"client": "runtime", "caller": redisfailure.CallerFleet}); namedCount != operations {
		t.Errorf("the named caller counts %v operations, redis_caller_operation_total %v", namedCount, operations)
	}
}

// renewalOwnership is a production ownership on a real store, with its
// observations reaching the recorder as they do in production.
func renewalOwnership(t *testing.T, client redis.UniversalClient, recorder *metric.Recorder) (*productionPhaseTwoOwnership, *ownership.RedisStore) {
	t.Helper()
	store, err := ownership.NewRedisStoreWithClient(redisForCaller(client, redisfailure.CallerOwnership), "alarmd-renewal-test")
	if err != nil {
		t.Fatal(err)
	}
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{
		DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities",
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: time.Now,
		ControlLeaderTTL: time.Minute, Observer: recorder, Reconcile: reconciler, Flights: flights, RecoveryLimits: limits,
		PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour,
		PublicationDelayAllowance: time.Minute, SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return production, store
}

const renewalTestInterval = 20 * time.Millisecond

// openRenewingQueryGroup places a Query Group on the worker, takes it and keeps
// renewing it until a few renewals have gone through.
func openRenewingQueryGroup(t *testing.T, production *productionPhaseTwoOwnership, store *ownership.RedisStore, reading scriptReading, queryGroup execution.QueryGroupIdentity) (phaseTwoQueryGroupRuntime, <-chan error) {
	t.Helper()
	runner := openLeasedQueryGroup(t, production, store, queryGroup)
	done := maintainLease(t, runner)
	waitPhaseTwoCondition(t, "renewals to go through", func() bool {
		return reading.renewals("query_group", "renewed", "none") >= 3
	})
	return runner, done
}

// openLeasedQueryGroup takes the Query Group's lease and leaves it unrenewed.
func openLeasedQueryGroup(t *testing.T, production *productionPhaseTwoOwnership, store *ownership.RedisStore, queryGroup execution.QueryGroupIdentity) phaseTwoQueryGroupRuntime {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: queryGroup, DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	runner, err := production.OpenQueryGroup(ctx, queryGroup, now, time.Minute)
	if err != nil {
		t.Fatalf("OpenQueryGroup() error = %v", err)
	}
	return runner
}

// maintainLease runs the lease's renewal loop until the test ends, and says
// how it ended.
func maintainLease(t *testing.T, runner phaseTwoQueryGroupRuntime) <-chan error {
	t.Helper()
	leaseCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- runner.MaintainLease(leaseCtx, renewalTestInterval, time.Minute) }()
	return done
}

func waitRenewalEnd(t *testing.T, done <-chan error, want error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("the renewal loop ended with %v, want %v", err, want)
		}
	case <-testwait.Expired(t):
		t.Fatal("the renewal loop did not end")
	}
}

// The store's refusal is the script answering normally: redis_script_total
// reads ok, and only this count says the renewal was refused and why.
func TestAQueryGroupRenewalRefusedByTheStoreReadsAsRefused(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(ctx context.Context, admin *redis.Client, keys ownership.FenceKeys) error
		want   error
		word   string
	}{
		{"no longer desired", func(ctx context.Context, admin *redis.Client, keys ownership.FenceKeys) error {
			return admin.HDel(ctx, keys.AssignmentKey, "desired_worker_id").Err()
		}, ownership.ErrNotDesired, "OWNERSHIP_NOT_DESIRED"},
		{"a stale fence", func(ctx context.Context, admin *redis.Client, keys ownership.FenceKeys) error {
			return admin.HSet(ctx, keys.OwnershipKey, "lease_token", "not-the-token").Err()
		}, ownership.ErrStaleFence, "OWNERSHIP_STALE_FENCE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := redistest.Start(t)
			if server == nil {
				return
			}
			recorder := metric.NewRecorder(metric.BuildInfo{})
			reading := scriptReading{t, recorder}
			production, store := renewalOwnership(t, scriptMetricsClient(t, server, recorder), recorder)
			_, done := openRenewingQueryGroup(t, production, store, reading, "query-group-1")

			if err := test.tamper(context.Background(), server.Client(), store.FenceKeys("query-group-1")); err != nil {
				t.Fatal(err)
			}
			waitRenewalEnd(t, done, test.want)

			if got := reading.renewals("query_group", "refused", test.word); got != 1 {
				t.Errorf("refused %s = %v, want the one", test.word, got)
			}
			if got := reading.renewals("query_group", "failed", ""); got != 0 {
				t.Errorf("failed = %v, a refusal is not a failure", got)
			}
			if got := reading.counter("alarmd_ownership_refusals_total", map[string]string{"site": "renewal", "refusal": test.word}); got != 1 {
				t.Errorf("ownership_refusals_total = %v", got)
			}
			// The reason a script's count cannot be this count.
			if got := reading.counter("alarmd_redis_script_total", map[string]string{"script": "ownership_renew", "outcome": "error"}); got != 0 {
				t.Errorf("the script reads %v errors: the refusal is a normal reply", got)
			}
			attempts := reading.renewals("query_group", "renewed", "none") + reading.renewals("query_group", "refused", "")
			if scripted := reading.renewScriptAttempts(); scripted != attempts {
				t.Errorf("the script ran %v times, the attempts count %v", scripted, attempts)
			}
		})
	}
}

// A session that has stopped refuses locally, with the same word, and sends
// nothing: this count has it and the script's does not. Released before its
// loop renews once, so no renewal can be in flight when it stops.
func TestARenewalRefusedLocallySendsNoScriptAndIsCounted(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	production, store := renewalOwnership(t, scriptMetricsClient(t, server, recorder), recorder)
	runner := openLeasedQueryGroup(t, production, store, "query-group-1")
	if err := runner.Release(context.Background()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	waitRenewalEnd(t, maintainLease(t, runner), ownership.ErrStaleFence)
	if got := reading.renewals("query_group", "refused", "OWNERSHIP_STALE_FENCE"); got != 1 {
		t.Errorf("refused = %v", got)
	}
	if scripted := reading.renewScriptAttempts(); scripted != 0 {
		t.Errorf("the script ran %v times, want none: a stopped session sends nothing", scripted)
	}
}

// releaseInFlight holds the first script it sees once armed until release
// has returned: a renewal that read its session as accepting is sent after
// the session stopped.
type releaseInFlight struct {
	armed   atomic.Bool
	release func()
}

func (hook *releaseInFlight) hold(cmds ...redis.Cmder) {
	for _, cmd := range cmds {
		if name := cmd.Name(); (name == "evalsha" || name == "eval") && hook.armed.CompareAndSwap(true, false) {
			hook.release()
			return
		}
	}
}

func (hook *releaseInFlight) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	hook.hold(cmd)
	return ctx, nil
}

func (*releaseInFlight) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (hook *releaseInFlight) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	hook.hold(cmds...)
	return ctx, nil
}

func (*releaseInFlight) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// A renewal in flight when its session stops is refused by the store: it
// sent its script, so the script's count has it, and it is counted refused
// once, with the word a local refusal has.
func TestARenewalInFlightAtReleaseIsRefusedByTheStoreAndItsScriptCounted(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	client := scriptMetricsClient(t, server, recorder)
	inFlight := &releaseInFlight{}
	client.AddHook(inFlight)
	production, store := renewalOwnership(t, client, recorder)
	runner, done := openRenewingQueryGroup(t, production, store, reading, "query-group-1")
	inFlight.release = func() {
		if err := runner.Release(context.Background()); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}
	inFlight.armed.Store(true)
	waitRenewalEnd(t, done, ownership.ErrStaleFence)
	if got := reading.renewals("query_group", "refused", "OWNERSHIP_STALE_FENCE"); got != 1 {
		t.Errorf("refused = %v", got)
	}
	remote := reading.renewals("query_group", "renewed", "none")
	if scripted := reading.renewScriptAttempts(); scripted != remote+1 {
		t.Errorf("the script ran %v times, want the %v renewals and the one refused in flight", scripted, remote)
	}
}

// A store that cannot be asked is a failure with its reason, one per attempt.
func TestARenewalThatCannotReachTheStoreReadsAsFailedWithItsReason(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	production, store := renewalOwnership(t, scriptMetricsClient(t, server, recorder), recorder)
	_, _ = openRenewingQueryGroup(t, production, store, reading, "query-group-1")

	server.Stop()
	waitPhaseTwoCondition(t, "failed attempts", func() bool {
		return reading.renewals("query_group", "failed", "") >= 3
	})
	if got := reading.renewals("query_group", "refused", ""); got != 0 {
		t.Errorf("refused = %v, nothing refused it", got)
	}
	reasons := map[string]float64{}
	for _, reason := range redisfailure.Reasons {
		if count := reading.renewals("query_group", "failed", reason); count > 0 {
			reasons[reason] = count
		}
	}
	if len(reasons) == 0 || reasons[redisfailure.Other] > 0 {
		t.Errorf("failed by reason = %v, want the words of a stopped server", reasons)
	}
	if got := reading.counter("alarmd_redis_script_total", map[string]string{"script": "ownership_renew", "outcome": "error"}); got == 0 {
		t.Error("the script reads no errors")
	}
	// Nothing was sent, so nothing is counted as traffic in the window.
	sent := reading.counter("alarmd_redis_script_bytes_total", map[string]string{"script": "ownership_renew", "direction": "sent"})
	failed := reading.renewals("query_group", "failed", "")
	waitPhaseTwoCondition(t, "more failed attempts", func() bool {
		return reading.renewals("query_group", "failed", "") >= failed+3
	})
	if after := reading.counter("alarmd_redis_script_bytes_total", map[string]string{"script": "ownership_renew", "direction": "sent"}); after != sent {
		t.Errorf("the script's sent bytes moved from %v to %v with the server stopped", sent, after)
	}
}

// The leader's renewal is the same script, told apart here by its lease.
func TestTheControlLeadersRenewalReadsUnderItsOwnLease(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	production, store := renewalOwnership(t, scriptMetricsClient(t, server, recorder), recorder)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leader, err := production.TryAcquireControlLeader(ctx, time.Now(), time.Minute)
	if err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() = (%v, %v)", leader, err)
	}
	done := make(chan error, 1)
	go func() { done <- production.MaintainControlLeader(ctx, renewalTestInterval, time.Minute) }()
	waitPhaseTwoCondition(t, "leader renewals", func() bool {
		return reading.renewals("control_leader", "renewed", "none") >= 3
	})
	if got := reading.renewals("query_group", "", ""); got != 0 {
		t.Errorf("a query group's renewals = %v", got)
	}

	if err := server.Client().HSet(ctx, store.FenceKeys(ownership.ControlLeaderIdentity).OwnershipKey, "lease_token", "not-the-token").Err(); err != nil {
		t.Fatal(err)
	}
	waitRenewalEnd(t, done, ownership.ErrStaleFence)

	if got := reading.renewals("control_leader", "refused", "OWNERSHIP_STALE_FENCE"); got != 1 {
		t.Errorf("refused = %v", got)
	}
	// Reported beside the query groups' now.
	if got := reading.counter("alarmd_ownership_refusals_total", map[string]string{"site": "renewal", "refusal": "OWNERSHIP_STALE_FENCE"}); got != 1 {
		t.Errorf("ownership_refusals_total = %v", got)
	}
	attempts := reading.renewals("control_leader", "renewed", "none") + reading.renewals("control_leader", "refused", "")
	if scripted := reading.renewScriptAttempts(); scripted != attempts {
		t.Errorf("the script ran %v times, the attempts count %v", scripted, attempts)
	}
}

// Each attempt of a round counts once, a decision once, and giving up on a
// passed deadline adds nothing to the attempts already counted.
func TestEachLeaderRenewalAttemptCountsOnce(t *testing.T) {
	replay := func(fixture renewalTestFixture) scriptReading {
		recorder := metric.NewRecorder(metric.BuildInfo{})
		for _, observation := range fixture.observations() {
			recorder.Observe(context.Background(), observation)
		}
		return scriptReading{t, recorder}
	}
	acquire := func(fixture renewalTestFixture) {
		t.Helper()
		leader, err := fixture.production.TryAcquireControlLeader(context.Background(), fixture.clock.Now(), time.Minute)
		if err != nil || !leader {
			t.Fatalf("TryAcquireControlLeader() = (%v, %v)", leader, err)
		}
	}
	t.Run("an invariant error is observed as a decision is", func(t *testing.T) {
		fixture := newRenewalTestFixture(t)
		invariant := error(&phaseTwoInvariantError{err: errors.New("an invariant of the phase two runtime")})
		fixture.store.renewLeaderErr = func() error { return invariant }
		acquire(fixture)
		err := fixture.production.MaintainControlLeader(context.Background(), time.Millisecond, time.Minute)
		if !errors.Is(err, invariant) {
			t.Fatalf("MaintainControlLeader() = %v", err)
		}
		reading := replay(fixture)
		if got := reading.renewals("control_leader", "failed", ""); got != 1 {
			t.Errorf("failed = %v, want the one attempt", got)
		}
		if calls := float64(fixture.store.renewLeaderCount()); calls != 1 {
			t.Errorf("the store was asked %v times", calls)
		}
	})
	t.Run("a passed deadline adds nothing to its attempts", func(t *testing.T) {
		fixture := newRenewalTestFixture(t)
		transient := errors.New("store unreachable")
		fixture.store.renewLeaderErr = func() error {
			fixture.clock.Advance(2 * time.Minute)
			return transient
		}
		acquire(fixture)
		err := fixture.production.MaintainControlLeader(context.Background(), time.Millisecond, time.Minute)
		if !errors.Is(err, transient) {
			t.Fatalf("MaintainControlLeader() = %v", err)
		}
		reading := replay(fixture)
		if got, calls := reading.renewals("control_leader", "failed", ""), float64(fixture.store.renewLeaderCount()); got != calls {
			t.Errorf("failed = %v, the store was asked %v times", got, calls)
		}
		if got := reading.renewals("control_leader", "refused", ""); got != 0 {
			t.Errorf("refused = %v", got)
		}
	})
	t.Run("a cancelled round counts nothing", func(t *testing.T) {
		fixture := newRenewalTestFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		fixture.store.renewLeaderErr = func() error {
			cancel()
			return context.Canceled
		}
		acquire(fixture)
		_ = fixture.production.MaintainControlLeader(ctx, time.Millisecond, time.Minute)
		reading := replay(fixture)
		if got := reading.renewals("control_leader", "", ""); got != 0 {
			t.Errorf("counted %v", got)
		}
	})
}

// A registration failing is counted, and its recovery is not an attempt.
func TestARegistrationRenewalCountsItsFailuresOnly(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	reading := scriptReading{t, recorder}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Observer: recorder}}
	bundle.observeRegistrationRenewal(observability.ResultFailed, context.DeadlineExceeded)
	bundle.observeRegistrationRenewal(observability.ResultResumed, nil)
	if got := reading.renewals("worker_registration", "failed", redisfailure.Timeout); got != 1 {
		t.Errorf("failed = %v", got)
	}
	if got := reading.counter("alarmd_lease_renewal_total", nil); got != 1 {
		t.Errorf("counted %v in all", got)
	}
}
