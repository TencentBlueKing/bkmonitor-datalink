// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group whose timeline key is not there at all blocks its source by
// a word of its own, SCHEDULE_MISSING, whichever read of the timeline meets
// the absence first. It used to share BLOCKED_EXACT_SET_UNAVAILABLE with
// every other reason the control plane had nothing to hand over, so nothing
// told the missing key apart, and the Worker had nothing to name it by on
// its registration.
func TestAMissingTimelineKeyBlocksTheSourceByName(t *testing.T) {
	for name, load := range map[string]execution.ProgressLoadResult{
		"with Progress": {Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120, LastFullSlot: 60,
			LastCompletionKind: execution.CompletionFull,
		}},
		"without Progress": {Status: execution.ProgressMissing},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)},
				timelineErr: controlplane.ErrScheduleTimelineAbsent}
			source := newProductionSlotSourceForTest(t, catalog, load, time.Unix(661, 0))
			_, due, _, err := source.Next(context.Background(), "query-group-1")
			var blocked *SourceBlockedError
			if due || !errors.As(err, &blocked) || blocked.ReasonCode() != "SCHEDULE_MISSING" {
				t.Fatalf("Next() = (due %v, error %T %v), want the source blocked under SCHEDULE_MISSING", due, err, err)
			}
		})
	}
}

// The other causes behind the old word keep it: a timeline that is there
// but holds no Segment for the Slot, and an exact set the control plane did
// not hand over, are not a missing key.
func TestOnlyAMissingKeyIsNamedMissing(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"missing key", failClosedScheduleNavigation(controlplane.ErrScheduleTimelineAbsent), "SCHEDULE_MISSING"},
		{"no Segment for the Slot", failClosedScheduleNavigation(controlplane.ErrScheduleUnavailable), "BLOCKED_EXACT_SET_UNAVAILABLE"},
		{"exact set unavailable", &SourceBlockedError{Err: errors.New("exact set unavailable")}, "BLOCKED_EXACT_SET_UNAVAILABLE"},
		{"undecodable", failClosedScheduleNavigation(&controlplane.DeterministicScheduleError{Err: errors.New("unknown field")}), "SCHEDULE_UNREADABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var blocked *SourceBlockedError
			if !errors.As(test.err, &blocked) || string(blocked.ReasonCode()) != test.want {
				t.Fatalf("%v is named %v, want %s", test.err, blocked, test.want)
			}
		})
	}
}
