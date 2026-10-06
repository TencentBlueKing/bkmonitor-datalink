// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestGapConflictLogsBothSidesBeyondErrorTextLimit(t *testing.T) {
	var output bytes.Buffer
	limiter, err := observability.NewWindowLogLimiter(observability.WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	logger := observability.NewLoggingObserver(observability.New("alarmd", &output), policy)
	conflict := &GapGuardConflictError{Persisted: GapGuardProtection{MarkerRevision: 6}}
	for i := uint32(1); i <= 8; i++ {
		conflict.Persisted.ScopeDetails = append(conflict.Persisted.ScopeDetails, execution.GapScopeState{
			Scope: execution.GapScope{HasLevel: true, LevelID: i}, Status: execution.GapStatusGapped,
			ReasonCode: "HISTORY_GAPPED", RequiredFullSlots: 9, ObservedFullSlots: 1})
	}
	conflict.Proposed.MutationScopes = []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: "SNAPSHOT_UNAVAILABLE", RequiredFullSlots: 9}}
	// The scheduler logs Execute's returned error directly; it does not pass
	// through the coordinator's internal stage observation helper.
	logger.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler,
		Stage: observability.StageSlotCompleted, Result: observability.ResultFailed, ReasonCode: "GAP_GUARD_CONFLICT", Err: fmt.Errorf("finalization: %w", conflict)})
	var event struct {
		GapConflict observability.GapExtensionFacts `json:"gap_conflict"`
	}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	facts := event.GapConflict
	if facts.MarkerRevision != 6 || len(facts.Persisted) != 8 || len(facts.Proposed) != 1 || facts.Persisted[7].Observed != 1 || facts.Proposed[0].Reason != "SNAPSHOT_UNAVAILABLE" || facts.Proposed[0].Kind != string(execution.GapOpen) {
		t.Fatalf("comparison evidence truncated: %s", output.Bytes())
	}
}
