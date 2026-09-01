// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestNormalizeObservationBoundsCatalogAndCounts(t *testing.T) {
	t.Parallel()

	got := NormalizeObservation(Observation{
		Component: Component("strategy-123"),
		Stage:     Stage("dynamic-stage"),
		Result:    Result("dynamic-result"),
		Operation: Operation("dynamic-operation"),
		Direction: Direction("dynamic-direction"),
		ReasonCode: ReasonCode(
			"dynamic-reason",
		),
		Duration: -time.Second,
		Counts: Counts{
			Messages: -1,
			Records:  2,
		},
	})

	if got.Component != ComponentOther || got.Stage != StageOther || got.Result != ResultOther {
		t.Fatalf("normalized identity = %q/%q/%q", got.Component, got.Stage, got.Result)
	}
	if got.Operation != OperationOther || got.Direction != DirectionOther || got.ReasonCode != ReasonOther {
		t.Fatalf("normalized operation/direction/reason = %q/%q/%q", got.Operation, got.Direction, got.ReasonCode)
	}
	if got.Duration != -time.Second || got.Counts.Messages != 0 || got.Counts.Records != 2 {
		t.Fatalf("normalized duration/counts = %v/%#v", got.Duration, got.Counts)
	}
}

func TestPhaseTwoComponentValuesMatchFrozenObservabilityContract(t *testing.T) {
	if ComponentControlPlane != "source" || ComponentOwnership != "router" || ComponentScheduler != "scheduler" {
		t.Fatalf("phase-two components = %q/%q/%q", ComponentControlPlane, ComponentOwnership, ComponentScheduler)
	}
	for _, pair := range []ComponentStage{
		{ComponentControlPlane, StageSnapshotRefreshed},
		{ComponentOwnership, StageAssignmentAcquired},
		{ComponentOwnership, StageTakeoverStarted},
		{ComponentOwnership, StageTakeoverCompleted},
		{ComponentScheduler, StageSlotCompleted},
	} {
		component, stage := NormalizeComponentStage(pair.Component, pair.Stage)
		if component != pair.Component || stage != pair.Stage {
			t.Fatalf("component/stage normalized to %q/%q, want %q/%q", component, stage, pair.Component, pair.Stage)
		}
	}
}

func TestOwnershipLifecycleLogCarriesExactOperationalIdentity(t *testing.T) {
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentOwnership,
		Stage:     StageTakeoverCompleted,
		Result:    ResultSuccess,
		Trace: TraceFields{
			QueryGroupKey: "qg-exact-identity",
			OwnerID:       "worker-exact-identity",
			OwnerEpoch:    7,
		},
	})
	for _, field := range []string{
		`"stage":"takeover_completed"`,
		`"query_group_key":"qg-exact-identity"`,
		`"owner_id":"worker-exact-identity"`,
		`"owner_epoch":7`,
	} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("ownership lifecycle log missing %s: %s", field, output.String())
		}
	}
}

func TestObservationLoggerUsesBoundedEnvelope(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := New("", &output)
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 1})
	if err != nil {
		t.Fatalf("NewWindowLogLimiter() error = %v", err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatalf("NewBoundedLogPolicy() error = %v", err)
	}
	loggingObserver := NewLoggingObserver(logger, policy)
	loggingObserver.Observe(context.Background(), Observation{
		Component:  ComponentResource,
		Stage:      StageResourceSoft,
		Result:     ResultTerminal,
		Operation:  OperationTransition,
		Direction:  DirectionOutput,
		ReasonCode: ReasonRSS,
		Duration:   1500 * time.Millisecond,
		Counts: Counts{
			Records:    2,
			Keys:       3,
			StateBytes: 128,
		},
		Trace: TraceFields{
			MessageID: "message-1", StrategyID: "42", TerminalScope: "LEVEL", TerminalFieldPath: "level.trigger_plan",
		},
		Err: errors.New("do not log raw error content"),
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	for field, want := range map[string]any{
		"component":      string(ComponentResource),
		"stage":          string(StageResourceSoft),
		"result":         string(ResultTerminal),
		"operation":      string(OperationTransition),
		"direction":      string(DirectionOutput),
		"reason_code":    string(ReasonRSS),
		"duration_ms":    float64(1500),
		"records":        float64(2),
		"keys":           float64(3),
		"state_bytes":    float64(128),
		"message_id":     "message-1",
		"strategy_id":    "42",
		"terminal_scope": "LEVEL",
		"field_path":     "level.trigger_plan",
	} {
		if event[field] != want {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], want, event)
		}
	}
	if _, exists := event["error"]; exists {
		t.Fatalf("raw error was logged: %#v", event)
	}
	if event["error_type"] == nil {
		t.Fatalf("error type is missing: %#v", event)
	}
}

func TestReasonNormalizationConsumesM0ObservationCatalog(t *testing.T) {
	t.Parallel()

	for _, definition := range contract.ReasonCatalogV2() {
		if !definition.Domains.Has(contract.ReasonDomainObservation) {
			continue
		}
		reason := ReasonCode(definition.Code)
		if got := NormalizeReason(reason, ResultTerminal); got != reason {
			t.Fatalf("log reason %q normalized to %q", reason, got)
		}
		var want ReasonCode
		switch definition.Class {
		case contract.ReasonClassDeterministic:
			want = ReasonContractDeterministic
		case contract.ReasonClassRetryable:
			want = ReasonContractRetryable
		case contract.ReasonClassCoverage:
			want = ReasonContractCoverage
		default:
			t.Fatalf("M0 reason %q has unknown class %q", definition.Code, definition.Class)
		}
		if got := NormalizeMetricReason(ComponentAdapter, reason, ResultTerminal); got != want {
			t.Fatalf("metric reason %q = %q, want %q", reason, got, want)
		}
	}
	if got := NormalizeReason("unsafe-reason", ResultTerminal); got != ReasonOther {
		t.Fatalf("unsafe log reason = %q, want %q", got, ReasonOther)
	}
	if got := NormalizeReason("UNKNOWN_REASON", ResultTerminal); got != ReasonOther {
		t.Fatalf("unknown catalog reason = %q, want %q", got, ReasonOther)
	}
	if got := NormalizeReason(ReasonContractDeterministic, ResultTerminal); got != ReasonOther {
		t.Fatalf("metric-only contract class leaked into logs: got %q, want %q", got, ReasonOther)
	}
	if got := NormalizeReason(ReasonNone, ResultFailed); got != ReasonInternalUnknown {
		t.Fatalf("failed reason none = %q, want %q", got, ReasonInternalUnknown)
	}
	if got := NormalizeReason(ReasonNone, ResultSuccess); got != ReasonNone {
		t.Fatalf("successful reason none = %q, want %q", got, ReasonNone)
	}
}

func TestBoundedLogPolicyRequiresExplicitExceptionalLimiter(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	limiter, err := newWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 2}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newWindowLogLimiter() error = %v", err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatalf("NewBoundedLogPolicy() error = %v", err)
	}
	if policy.ShouldLog(Observation{Component: ComponentDetect, Stage: StageDetectCompleted, Result: ResultSuccess}) {
		t.Fatal("routine success was logged")
	}
	if !policy.ShouldLog(Observation{Component: ComponentRuntime, Stage: StageStartup, Result: ResultStarted}) {
		t.Fatal("one-time startup was not logged")
	}
	for index := 0; index < 2; index++ {
		if !policy.ShouldLog(Observation{Component: ComponentConsumer, Stage: StageOffsetGap, Result: ResultFailed}) {
			t.Fatalf("bounded offset gap %d was not logged", index)
		}
	}
	if policy.ShouldLog(Observation{Component: ComponentConsumer, Stage: StageOffsetGap, Result: ResultFailed}) {
		t.Fatal("repeated offset gap exceeded its stage bound")
	}
	if !policy.ShouldLog(Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused}) {
		t.Fatal("reason-empty offset traffic suppressed the independent resource stage")
	}
	now = now.Add(time.Minute)
	if !policy.ShouldLog(Observation{Component: ComponentRuntime, Stage: StageRestartRecovered, Result: ResultResumed}) {
		t.Fatal("recovery was not logged after the window reset")
	}
}

func TestMultiNormalizesOnceBeforeFanout(t *testing.T) {
	t.Parallel()

	called := 0
	sink := ObserverFunc(func(_ context.Context, observation Observation) {
		called++
		if !observation.normalized || observation.Component != ComponentOther || observation.Counts.Messages != 0 {
			t.Fatalf("sink received unnormalized observation: %#v", observation)
		}
	})
	Multi(sink, sink).Observe(context.Background(), Observation{
		Component: Component("dynamic"), Stage: Stage("dynamic"), Counts: Counts{Messages: -1},
	})
	if called != 2 {
		t.Fatalf("sink calls = %d, want 2", called)
	}
}

func TestStageCatalogMapsEachStageToOneComponent(t *testing.T) {
	t.Parallel()

	components := make(map[Stage]Component)
	for _, pair := range AllComponentStages() {
		if existing, ok := components[pair.Stage]; ok {
			t.Fatalf("stage %q maps to both %q and %q", pair.Stage, existing, pair.Component)
		}
		components[pair.Stage] = pair.Component
	}
}

func TestPhaseTwoWorkflowFactsDoNotExpandGenericMetricCatalog(t *testing.T) {
	t.Parallel()

	component, stage := NormalizeComponentStage(ComponentState, StageMutationCompared)
	if component != ComponentState || stage != StageMutationCompared {
		t.Fatalf("phase-two log fact normalized to (%q,%q)", component, stage)
	}
	if IsGenericMetricComponentStage(component, stage) {
		t.Fatal("phase-two detailed stage entered the generic metric cross product")
	}
	if got := NormalizeOperation(OperationReplay); got != OperationReplay {
		t.Fatalf("phase-two log operation=%q", got)
	}
	if got := NormalizeMetricOperation(OperationReplay); got != OperationOther {
		t.Fatalf("phase-two operation entered generic metric labels as %q", got)
	}
}

func TestMultiObserverSkipsNilObservers(t *testing.T) {
	t.Parallel()

	called := 0
	observer := Multi(nil, ObserverFunc(func(context.Context, Observation) { called++ }))
	observer.Observe(context.Background(), Observation{})
	if called != 1 {
		t.Fatalf("observer calls = %d, want 1", called)
	}
	NopObserver{}.Observe(context.Background(), Observation{})
}

func TestPhaseTwoSuccessLogsAreBoundedAndCarryTraceID(t *testing.T) {
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	observation := Observation{
		Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultSuccess,
	}
	ctx := ContextWithTraceFields(context.Background(), TraceFields{
		TraceID: "uq-trace-1", QueryGroupKey: "qg-sensitive",
		ScheduleSegmentStart: 1_700_000_000, DuePlanSetDigest: "due-plan-set-sensitive",
	})
	observer.Observe(ctx, observation)
	observer.Observe(ctx, observation)
	if got := strings.Count(output.String(), "\"stage\":\"query_completed\""); got != 1 {
		t.Fatalf("bounded phase-two success logs = %d, want 1: %s", got, output.String())
	}
	if !strings.Contains(output.String(), "\"trace_id\":\"uq-trace-1\"") {
		t.Fatalf("trace id missing from structured log: %s", output.String())
	}
	if !strings.Contains(output.String(), "\"schedule_segment_start\":1700000000") ||
		!strings.Contains(output.String(), "\"due_plan_set_digest\":\"due-plan-set-sensitive\"") {
		t.Fatalf("frozen Slot provenance missing from structured log: %s", output.String())
	}
}

func TestNestedTraceContextKeepsInheritedFieldsAndExplicitValuesWin(t *testing.T) {
	parent := ContextWithTraceFields(context.Background(), TraceFields{
		TraceID: "trace-parent", QueryGroupKey: "qg-parent", StrategyID: "strategy-parent",
		Partition: 3, PartitionKnown: true,
	})
	child := ContextWithTraceFields(parent, TraceFields{TraceID: "trace-child", LevelID: "level-child"})
	got := TraceFieldsFromContext(child)
	if got.TraceID != "trace-child" || got.QueryGroupKey != "qg-parent" || got.StrategyID != "strategy-parent" ||
		got.LevelID != "level-child" || !got.PartitionKnown || got.Partition != 3 {
		t.Fatalf("merged trace fields = %#v", got)
	}
}
