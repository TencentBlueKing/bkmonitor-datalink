// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Slot resumed from an unfinished projection is still in its cohort.
//
// It is the same Slot on the same schedule; the only thing that changed is
// that this process is picking it up rather than building it. It used to carry
// no cohort, so its completion never reached the short-period counter -- and
// what that counter is read for is whether the short-period cohorts are
// completing, which makes the resumed rounds the ones most worth counting. A
// process restart or an owner change made them vanish from the population
// instead.
func TestASlotResumedFromAProjectionKeepsItsCohort(t *testing.T) {
	for _, test := range []struct {
		interval int64
		want     string
	}{
		{interval: 10, want: "10s"},
		{interval: 15, want: "15s"},
		{interval: 30, want: "30s"},
		{interval: 60, want: ""},
	} {
		t.Run((time.Duration(test.interval) * time.Second).String(), func(t *testing.T) {
			schedule := schedulerSchedule(t, test.interval, execution.EvaluationTime(test.interval), nil, "snapshot-1", 1)
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
			first := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(test.interval*4, 0))
			slot, due, _, err := first.Next(context.Background(), "query-group-1")
			if err != nil || !due {
				t.Fatalf("Next(first) = (%+v, %t, %v)", slot, due, err)
			}
			if slot.ShortPeriodCohort != test.want {
				t.Fatalf("the Slot built from the schedule carries cohort %q, want %q",
					slot.ShortPeriodCohort, test.want)
			}

			projection := execution.UnfinishedSlotProjection{
				Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
				EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
				KeepUntilUnixMilli:             slot.KeepUntilUnixMilli,
			}
			load := foundProgress(slot.ExpectedNextSlot, 0)
			load.Progress.UnfinishedSlot = &projection
			// The projection is taken because the Slot can no longer be frozen
			// from the Snapshot, which is the state a resumed Slot is in.
			catalog.freezeErr = controlplane.ErrSnapshotUnavailable
			resumed := newProductionSlotSourceForTest(t, catalog, load, time.Unix(test.interval*4, 0))
			restored, due, _, err := resumed.Next(context.Background(), "query-group-1")
			if err != nil || !due {
				t.Fatalf("Next(resumed) = (%+v, %t, %v)", restored, due, err)
			}
			if restored.Contract != slot.Contract {
				t.Fatalf("the resumed Slot is not the same Slot: %+v", restored.Contract)
			}
			if restored.ShortPeriodCohort != test.want {
				t.Fatalf("the resumed Slot carries cohort %q and the same Slot built from the schedule "+
					"carries %q. A Slot that loses its cohort on resume drops out of the counter read to "+
					"decide whether that cohort completes at all", restored.ShortPeriodCohort, test.want)
			}
		})
	}
}

// A Slot whose schedule cannot be read gets no cohort rather than no Slot.
//
// The cohort is a label on an observation and decides nothing. A Slot resumed
// from a projection is more likely than any other to be unable to read the
// Segment under it -- a Segment pruned while the Slot was in flight is one of
// the reasons it is being resumed at all -- and failing the resume to put a
// label on it would trade the Slot for the label.
//
// Asserted on the lookup rather than through Next, because the paths that
// reach Next have already read a schedule by the time the projection is taken.
// This pins the branch; it does not claim to have reached it from outside.
func TestALookupWithNoReadableScheduleYieldsNoCohort(t *testing.T) {
	empty := &fakeSlotCatalog{t: t}
	source := newProductionSlotSourceForTest(t, empty, missingProgress(), time.Unix(40, 0))
	if cohort := source.cohortForSlot(context.Background(), 10); cohort != "" {
		t.Fatalf("cohort = %q, want none: nothing could say which it is", cohort)
	}
}

// The cohort is the Slot's own, not the Segment's.
//
// A Segment can open at an instant no Plan under it is aligned at -- a cutover
// lands where the publication lands, not on anyone's grid -- so the Segment
// start and the Slot are two different questions with two different answers.
// The Slot is the one being run, and it is the one the completion is counted
// under.
func TestAResumedSlotTakesItsCohortFromItsOwnSlot(t *testing.T) {
	// A ten-second grid whose Segment opens at 15, which is not one of its
	// points; the first Slot is 20.
	schedule := schedulerSchedule(t, 10, 15, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(60, 0))
	if cohort := source.cohortForSlot(context.Background(), 15); cohort != "" {
		t.Fatalf("the Segment start is aligned for no Plan and reports cohort %q", cohort)
	}

	first := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(60, 0))
	slot, due, _, err := first.Next(context.Background(), "query-group-1")
	if err != nil || !due || slot.Contract.Slot.EvaluationTime != 20 {
		t.Fatalf("Next(first) = (%+v, %t, %v), want the Slot at 20", slot.Contract.Slot, due, err)
	}
	projection := execution.UnfinishedSlotProjection{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
		KeepUntilUnixMilli:             slot.KeepUntilUnixMilli,
	}
	load := foundProgress(slot.ExpectedNextSlot, 0)
	load.Progress.UnfinishedSlot = &projection
	catalog.freezeErr = controlplane.ErrSnapshotUnavailable
	resumed := newProductionSlotSourceForTest(t, catalog, load, time.Unix(60, 0))

	restored, due, _, err := resumed.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next(resumed) = (%+v, %t, %v)", restored, due, err)
	}
	if restored.ShortPeriodCohort != "10s" {
		t.Fatalf("cohort = %q, want 10s from the Slot itself rather than from the Segment it opened in",
			restored.ShortPeriodCohort)
	}
}
