// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The cursor advance records why it moved, and the two reasons it can move for
// are not the same fact.
//
// Both causes reach this function and both move the cursor the same way, so
// what the Progress carries afterwards is the only thing that tells them
// apart. Pruned means the timeline dropped those times and no read will ever
// find them; plan-not-active means the times are there and the Plan was not,
// because it left the activation and came back. A reader asking where the
// rounds went is sent to retention by one and to the active set by the other,
// and until this split they were all sent to retention.
func TestTheCursorAdvanceNamesWhyItMoved(t *testing.T) {
	at := time.Unix(661, 0)
	for name, test := range map[string]struct {
		cause error
		want  execution.ReasonCode
	}{
		"the timeline no longer holds the cursor": {
			cause: &SourceBlockedError{Err: ErrProgressOffSchedule},
			want:  execution.ReasonCode(contract.ReasonSchedulePruned),
		},
		"the segment holds it but no Plan is due": {
			cause: &SourceBlockedError{Err: ErrNoPlanDueInSegment},
			want:  execution.ReasonCode(contract.ReasonPlanNotActive),
		},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t,
				schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
			progress := execution.ScheduleProgress{
				Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120,
				LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
			}
			reader := &advancingProgressReader{
				fakeProgressReader: &fakeProgressReader{
					result:  execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &progress},
					catalog: catalog,
				},
				result: execution.ProgressSkipResult{Status: execution.ProgressCommitted},
			}
			source := prunedCursorSource(t, catalog, reader, at, observability.ObserverFunc(
				func(context.Context, observability.Observation) {}))

			resumed, advanced, err := source.advancePrunedCursor(context.Background(), progress, testFence(7), test.cause)
			if err != nil || !advanced {
				t.Fatalf("advancePrunedCursor() = (advanced %v, error %v), want the cursor moved", advanced, err)
			}
			if resumed.CurrentOrRecentGap == nil {
				t.Fatal("the advance recorded no gap, so nothing says the Slots were passed over")
			}
			if got := resumed.CurrentOrRecentGap.ReasonCode; got != test.want {
				t.Fatalf("the advance recorded %q, want %q", got, test.want)
			}
			// Whatever the reason, the Progress must still read as a forward
			// skip: it carries no completion to navigate from, and anchoring
			// on one would resume inside the stretch just passed over.
			if !resumed.SkippedPrunedRange() {
				t.Fatal("the advance does not read as a forward skip, so navigation would anchor on a Slot nobody ran")
			}
		})
	}
}
