// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Slot given up for its share is finalized on the query-free path: no query
// is sent, Progress completes GAP_SKIPPED, and the gap marker it opens on each
// due Plan is exactly the one an expired replay opens. The evaluator reads
// markers, not causes, so the Slot after it is held by the marker as after
// any query-free gap - no RECOVERY across it and no no-data inferred from it -
// and only the completion's cause names the share.
func TestAShareExceededSlotOpensTheSameGapMarkerAsAnyQueryFreeGap(t *testing.T) {
	run := func(cause execution.CompletionCause, shareExceeded bool) queryFreeFixture {
		t.Helper()
		fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
		fixture.ports.finalization.Mode = execution.FinalizationGapSkipped
		fixture.ports.finalization.ReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
		fixture.ports.finalization.Cause = cause
		request := slotRequest(execution.OperationRetry)
		request.ShareExceeded = shareExceeded
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed || result.CompletionKind != execution.CompletionGapSkipped || result.QueryFreeCause != cause {
			t.Fatalf("%s: Execute() result=%+v error=%v", cause, result, err)
		}
		if slices.Contains(*fixture.trace, "query") {
			t.Fatalf("%s: a query-free finalization sent a query: %v", cause, *fixture.trace)
		}
		return fixture
	}
	share := run(execution.CauseResourceShareExceeded, true)
	expired := run(execution.CauseExpiredReplay, false)
	if len(share.ports.mutations) != 1 || share.ports.mutations[0].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		share.ports.mutations[0].Scopes[0].Scope != (execution.GapScope{}) {
		t.Fatalf("share-exceeded Slot did not open a whole-Plan GAP_SKIPPED marker: %+v", share.ports.mutations)
	}
	if !reflect.DeepEqual(share.ports.mutations, expired.ports.mutations) {
		t.Fatalf("share-exceeded marker differs from an expired replay's:\nshare   %+v\nexpired %+v", share.ports.mutations, expired.ports.mutations)
	}
	if share.ports.lastProgress.Completion.Kind != execution.CompletionGapSkipped ||
		!reflect.DeepEqual(share.ports.lastProgress.Completion, expired.ports.lastProgress.Completion) {
		t.Fatalf("share-exceeded completion differs from an expired replay's: %+v vs %+v",
			share.ports.lastProgress.Completion, expired.ports.lastProgress.Completion)
	}
}
