// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// An unfinished Slot a later Segment moves the cursor past is skipped by the
// store itself: one slot_skipped line once the write applies, with what held
// the round before it and the lookback as read then, and the recent gap it
// leaves. A Slot the new grid still reaches, and a write that does not
// apply, write none.
func TestAnAbandonedUnfinishedSlotWritesOneSlotSkippedLine(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 4, LeaseToken: "lease"}
	for _, test := range []struct {
		name      string
		requested execution.EvaluationTime
		status    ownership.FencedCASStatus
		heldBy    bool
		want      int
	}{
		{name: "skipped", requested: 180, heldBy: true, want: 1},
		{name: "skipped with nothing on the round", requested: 180, want: 1},
		{name: "the write does not apply", requested: 180, status: ownership.FencedCASConflict, want: 0},
		{name: "the new grid reaches it", requested: 90, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			persisted := progressProjectionAt(120)
			current := execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull}
			current.UnfinishedSlot = &persisted
			fake := &controlFake{value: mustEncode(t, current), status: test.status}
			store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 120, 120: 180})
			var output bytes.Buffer
			limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 1, MaxScopes: 16})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := observability.NewScopedBoundedLogPolicy(limiter)
			if err != nil {
				t.Fatal(err)
			}
			store.options.Observer = observability.NewLoggingObserver(observability.New("alarmd", &output), policy)
			ctx := context.Background()
			if test.heldBy {
				ctx = observability.ContextWithHeldBy(ctx, observability.HeldByFacts{Decision: "query_cooldown", AtUnixMilli: 1, QueryCooldownFailures: 2})
				ctx = observability.ContextWithLookbackBusy(ctx, func() (observability.LookbackBusyFacts, bool) {
					return observability.LookbackBusyFacts{ProbationRereads: 1}, true
				})
			}
			_, _ = store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: newerSegmentProjectionAt(test.requested)})
			var lines []map[string]any
			for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
				var line map[string]any
				if raw == "" || json.Unmarshal([]byte(raw), &line) != nil || line["stage"] != observability.StageSlotSkipped {
					continue
				}
				lines = append(lines, line)
			}
			if len(lines) != test.want {
				t.Fatalf("slot_skipped lines = %d, want %d: %s", len(lines), test.want, output.String())
			}
			if test.want == 0 {
				return
			}
			line := lines[0]
			if line["finalizing_path"] != observability.SlotSkippedProgressStore || line["skip_gap_guard"] != observability.SkipGapProgressOnly ||
				line["skipped_first_slot"] != float64(120) || line["owner_epoch"] != float64(4) || line["query_group_key"] != "q" {
				t.Fatalf("abandon line = %v", line)
			}
			if test.heldBy {
				if line["held_by"] != "query_cooldown" || line["held_by_lookback"] != true || line["lookback_probation_rereads"] != float64(1) {
					t.Fatalf("abandon line held_by %v lookback %v: %v", line["held_by"], line["held_by_lookback"], line)
				}
			} else if line["held_by"] != observability.HeldByNothing || line["held_by_lookback_known"] != false {
				t.Fatalf("abandon line without a round: held_by %v lookback known %v", line["held_by"], line["held_by_lookback_known"])
			}
		})
	}
}
