// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeObservationKeepsStateApplyChunkFactsOnlyForStateStages(t *testing.T) {
	t.Parallel()

	facts := &StateApplyChunkFacts{Index: 1, Count: 2, AppliedKeys: 16384, AppliedBytes: 4096, ElapsedMillis: 12}
	for _, stage := range []Stage{StageStateAdmission, StageStateApplied, StageGapGuardCommitted} {
		kept := NormalizeObservation(Observation{Component: ComponentState, Stage: stage, Result: ResultSuccess, StateApplyChunk: facts})
		if kept.StateApplyChunk == nil || *kept.StateApplyChunk != *facts || kept.StateApplyChunk == facts {
			t.Fatalf("%s facts = %+v, want a copy of %+v", stage, kept.StateApplyChunk, facts)
		}
	}
	dropped := []Observation{
		{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultSuccess, StateApplyChunk: facts},
		{Component: ComponentState, Stage: StageStatePreflight, Result: ResultSuccess, StateApplyChunk: facts},
		{Component: ComponentState, Stage: StageStateApplied, Result: ResultSuccess, StateApplyChunk: &StateApplyChunkFacts{Index: 2, Count: 2}},
		{Component: ComponentState, Stage: StageStateApplied, Result: ResultSuccess, StateApplyChunk: &StateApplyChunkFacts{Index: 0, Count: 1, AppliedKeys: -1}},
	}
	for _, observation := range dropped {
		if NormalizeObservation(observation).StateApplyChunk != nil {
			t.Fatalf("invalid chunk facts survived normalization: %+v", observation)
		}
	}
}

func TestLoggingObserverWritesStateApplyChunkAttributes(t *testing.T) {
	t.Parallel()

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
		Component: ComponentState, Stage: StageStateApplied, Result: ResultSuccess, Operation: OperationNormal,
		Direction: DirectionInternal, Counts: Counts{Keys: 8192, StateBytes: 1024},
		StateApplyChunk: &StateApplyChunkFacts{Index: 3, Count: 9, AppliedKeys: 32768, AppliedBytes: 4096, ElapsedMillis: 250},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode chunk observation log: %v; log=%s", err, output.String())
	}
	want := map[string]any{
		"chunk_index": float64(3), "chunk_count": float64(9), "applied_keys": float64(32768),
		"applied_bytes": float64(4096), "elapsed_ms": float64(250), "keys": float64(8192), "state_bytes": float64(1024),
	}
	for field, value := range want {
		if event[field] != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], value, event)
		}
	}
}
