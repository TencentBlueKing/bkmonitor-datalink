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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The held-back Query Groups are read by reason, the set accountings and the
// reopened timelines as counters, every label present at zero.
func TestActivationBlockedReadsTheLeadersLastCutover(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.SetActivationBlockedSource(func() controlplane.ActivationBlockedReading {
		return controlplane.ActivationBlockedReading{
			ByReason:   map[string]int{controlplane.CutoverReasonOpenDigestMismatch: 2},
			Accounting: map[controlplane.BlockedSetAccounting]uint64{controlplane.BlockedSetLost: 1},
			Reopened:   3,
		}
	})
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	series := map[string]int{}
	for _, family := range families {
		for _, sample := range family.GetMetric() {
			label := ""
			for _, pair := range sample.GetLabel() {
				label = pair.GetValue()
			}
			switch family.GetName() {
			case "bkmonitor_alarmd_activation_blocked_query_groups":
				values["blocked:"+label] = sample.GetGauge().GetValue()
				series["blocked"]++
			case "bkmonitor_alarmd_activation_blocked_set_total":
				values["accounting:"+label] = sample.GetCounter().GetValue()
				series["accounting"]++
			case "bkmonitor_alarmd_activation_timeline_reopened_total":
				values["reopened"] = sample.GetCounter().GetValue()
			}
		}
	}
	if values["blocked:"+controlplane.CutoverReasonOpenDigestMismatch] != 2 || values["accounting:lost"] != 1 || values["reopened"] != 3 ||
		series["blocked"] != len(ActivationBlockedReasons) || series["accounting"] != len(controlplane.BlockedSetAccountings) {
		t.Fatalf("values = %v series = %v", values, series)
	}
}

// The activation body's length is one gauge: zero before a source is bound,
// the source's reading after.
func TestActivationBodyBytesIsTheLastReadLength(t *testing.T) {
	read := func(recorder *Recorder) (float64, int) {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == "bkmonitor_alarmd_activation_body_bytes" {
				return family.GetMetric()[0].GetGauge().GetValue(), len(family.GetMetric())
			}
		}
		return -1, 0
	}
	recorder := NewRecorder(BuildInfo{})
	if value, series := read(recorder); value != 0 || series != 1 {
		t.Fatalf("unbound gauge = %v over %d series, want 0 over 1", value, series)
	}
	recorder.SetActivationBodyBytesSource(func() int64 { return 1_541_020 })
	if value, _ := read(recorder); value != 1_541_020 {
		t.Fatalf("gauge = %v, want the source's 1541020", value)
	}
}
