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
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func envelopePassSeries(t *testing.T, recorder *Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	found := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_state_envelope_pass_series_total" {
			continue
		}
		for _, sample := range family.Metric {
			for _, label := range sample.Label {
				if label.GetName() == "outcome" {
					found[label.GetValue()] = sample.GetCounter().GetValue()
				}
			}
		}
	}
	return found
}

// Every outcome of the second pass is a series on /metrics from the start,
// before anything has happened.
//
// This is the whole reason the split is a counter and not only a log line. The
// preflight line is rate-limited like every workflow stage, so a busy
// deployment merges most of them away: a sampled line can carry a non-zero --
// wait long enough and one appears -- but "zero everywhere, always" cannot be
// established from a sample, and that is exactly the reading the compatibility
// read's deletion waits on. A label nobody pre-created is absent, and absent
// reads the same as zero while meaning the opposite.
func TestEveryEnvelopePassOutcomeIsReadableBeforeItHappens(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	found := envelopePassSeries(t, recorder)
	for _, outcome := range observability.EnvelopePassOutcomes {
		value, present := found[outcome]
		if !present {
			t.Errorf("outcome %q has no series before anything happened: absent and zero are the two readings this "+
				"family exists to keep apart", outcome)
			continue
		}
		if value != 0 {
			t.Errorf("outcome %q starts at %v, want 0", outcome, value)
		}
	}
	if len(found) != len(observability.EnvelopePassOutcomes) {
		t.Fatalf("the family carries %d outcomes, want the %d in the closed set: a value outside it would be a label "+
			"nobody pre-created", len(found), len(observability.EnvelopePassOutcomes))
	}
}

// A preflight's counts reach the counter under the outcome they belong to.
//
// Asserted per outcome rather than on a total, because the failure this
// catches is a count landing under the wrong name -- which a total cannot see,
// and which would put a corrupt record's count into the one outcome that is
// allowed to be non-zero for ever.
func TestAPreflightsCountsReachTheirOwnOutcome(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageStatePreflight,
		Result: observability.ResultSuccess,
		Counts: observability.Counts{
			EnvelopeAnswered: 3, NoRecordYet: 5, EnvelopeCorrupt: 7,
			FrameCorruptRescued: 11, FrameCorruptLost: 13,
		},
	})
	for outcome, want := range map[string]float64{
		observability.EnvelopePassOldRepresentation: 3,
		observability.EnvelopePassNoRecordYet:       5,
		observability.EnvelopePassEnvelopeCorrupt:   7,
		observability.EnvelopePassFrameCorruptSaved: 11,
		observability.EnvelopePassFrameCorruptLost:  13,
	} {
		if got := envelopePassSeries(t, recorder)[outcome]; got != want {
			t.Errorf("outcome %q = %v, want %v", outcome, got, want)
		}
	}
}

// The write path's envelope count is a series at zero from the first scrape,
// and it is moved by the state_applied rows and by nothing else.
//
// The row it is read from is sampled and omits a zero, so the deletion's
// "zero for a whole window" can only be read here. The preflight row is
// observed too, carrying the same field, so a collector that read the count
// off every state row - or off the wrong one - fails.
func TestTheWritePathEnvelopeCountIsACounterReadFromTheAppliedRowOnly(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if got := testutil.ToFloat64(recorder.phaseTwo.envelopeApply); got != 0 {
		t.Fatalf("before anything happened = %v, want a series at zero", got)
	}
	observe := func(stage observability.Stage, count int64) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentState, Stage: stage, Result: observability.ResultSuccess,
			Counts: observability.Counts{Keys: 4, EnvelopeReadsApply: count},
		})
	}
	observe(observability.StageStateApplied, 2)
	observe(observability.StageStateApplied, 0)
	observe(observability.StageStateApplied, 3)
	observe(observability.StageStatePreflight, 7)
	if got := testutil.ToFloat64(recorder.phaseTwo.envelopeApply); got != 5 {
		t.Fatalf("state_envelope_apply_items_total = %v, want 5 from the applied rows alone", got)
	}
}
