// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"
	"time"
)

// The overshoot lands in the bucket its magnitude belongs to, and the two
// magnitudes that need opposite responses land in different ones.
//
// That separation is the whole reason this measurement exists. A bound reaching
// a minute past an object that is already due is the correctness defect the
// index's own file names; a bound crossed while the round was in flight is the
// clock doing what clocks do. The count of violations reports them identically,
// which is why reading it against zero condemns every one of the second kind.
func TestTheOvershootSeparatesALateBoundFromACrossedBoundary(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	// Inside one walk over the owned set, and far outside it.
	recorder.RecordDueIndexAuditOvershoot(200 * time.Millisecond)
	recorder.RecordDueIndexAuditOvershoot(45 * time.Second)

	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var cumulative map[float64]uint64
	var count uint64
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_due_index_audit_overshoot_seconds" {
			continue
		}
		cumulative = map[float64]uint64{}
		for _, series := range family.Metric {
			count = series.GetHistogram().GetSampleCount()
			for _, bucket := range series.GetHistogram().GetBucket() {
				cumulative[bucket.GetUpperBound()] = bucket.GetCumulativeCount()
			}
		}
	}
	if cumulative == nil {
		t.Fatal("the overshoot was not published at all, so nothing can say why the index held back " +
			"an object that was due")
	}
	if count != 2 {
		t.Fatalf("observed %d samples, want 2", count)
	}
	// One below a quarter second, and the other not below a full minute's
	// predecessor -- so the two are on opposite sides of the walk's own duration
	// and a reader can tell them apart.
	if got := cumulative[0.25]; got != 1 {
		t.Errorf("cumulative count at 0.25s = %d, want only the in-flight crossing below it", got)
	}
	if got := cumulative[30]; got != 1 {
		t.Errorf("cumulative count at 30s = %d, want the late bound above it", got)
	}
	if got := cumulative[60]; got != 2 {
		t.Errorf("cumulative count at 60s = %d, want both samples inside the last bucket", got)
	}
}

// A negative duration is not a measurement of anything.
//
// It can only come from the bound already having passed between the prediction
// and this call, and recording it as a large negative would sit outside every
// bucket and silently change the count the buckets are read against.
func TestAnOvershootThatAlreadyPassedIsRecordedAsNone(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.RecordDueIndexAuditOvershoot(-5 * time.Second)

	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_due_index_audit_overshoot_seconds" {
			continue
		}
		for _, series := range family.Metric {
			if sum := series.GetHistogram().GetSampleSum(); sum != 0 {
				t.Fatalf("sample sum = %v, want 0: a negative overshoot is the bound having passed, "+
					"not a measurement, and it would drag the sum below every bucket", sum)
			}
		}
	}
}
