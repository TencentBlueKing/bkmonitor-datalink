// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Stored is superseded-by-a-later-Slot exactly when it is newer and its time
// is after the candidate's. Each row moves one thing from the Slot's own
// version (epoch 2, time 120, digest m); the expectation is read off the
// ruling, not off CompareApplyVersion.
func TestOnlyALaterSlotsVersionSupersedes(t *testing.T) {
	candidate := execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 120, SlotDigest: "m"}
	for _, test := range []struct {
		name   string
		stored execution.ApplyVersion
		want   bool
	}{
		{"later time, same epoch", execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 180, SlotDigest: "a"}, true},
		{"later time, newer epoch", execution.ApplyVersion{StateApplyEpoch: 3, EvaluationTime: 180, SlotDigest: "a"}, true},
		{"later time, older epoch", execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 180, SlotDigest: "z"}, false},
		{"same time, newer epoch", execution.ApplyVersion{StateApplyEpoch: 3, EvaluationTime: 120, SlotDigest: "m"}, false},
		{"earlier time, newer epoch", execution.ApplyVersion{StateApplyEpoch: 3, EvaluationTime: 60, SlotDigest: "z"}, false},
		{"same time and epoch, larger digest", execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 120, SlotDigest: "z"}, false},
		{"same time and epoch, smaller digest", execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 120, SlotDigest: "a"}, false},
		{"the Slot's own", candidate, false},
		{"nothing stored", execution.ApplyVersion{}, false},
	} {
		if got := execution.SupersededBy(test.stored, candidate); got != test.want {
			t.Errorf("%s: SupersededBy = %v, want %v", test.name, got, test.want)
		}
	}
}

// The sample a completion line carries is the first skipped series, with
// both versions, and the count every one; nothing skipped is no facts.
func TestSupersededSeriesKeepsTheFirstSampleAndCountsEvery(t *testing.T) {
	var series execution.SupersededSeries
	if series.Facts() != nil {
		t.Fatal("nothing skipped carries facts")
	}
	first := execution.SupersededSample{Series: "s1",
		Slot:   execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "k"},
		Stored: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "k1"}}
	series.Record(first)
	series.Record(execution.SupersededSample{Series: "s2"})
	facts := series.Facts()
	if facts == nil || facts.Series != 2 || facts.Sample == nil || facts.Sample.Series != "s1" ||
		facts.Sample.Slot.EvaluationTime != 60 || facts.Sample.Stored.EvaluationTime != 120 ||
		facts.Sample.Stored.SlotDigest != "k1" || facts.Sample.Slot.StateApplyEpoch != 1 {
		t.Fatalf("facts = %+v sample %+v", facts, facts.Sample)
	}
}
