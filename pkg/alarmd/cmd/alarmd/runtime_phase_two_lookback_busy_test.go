// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var _ lookbackBusyReading = (*lookback.Engine)(nil)

type busyGroup struct{ asked execution.QueryGroupIdentity }

func (group *busyGroup) Busy(queryGroup execution.QueryGroupIdentity) (lookback.Busy, bool) {
	group.asked = queryGroup
	return lookback.Busy{Capturing: true, SamplePending: true, ProbePending: true, ProbationRereads: 2,
		DirectedPending: 3, ReadsInFlight: 4, TrialPrepared: true}, true
}

// The slot_skipped line reads every count the lookback holds of the group,
// for the group skipped; no lookback, no reading.
func TestTheSkipLineReadsWhatTheLookbackHoldsOfTheGroup(t *testing.T) {
	group := &busyGroup{}
	facts, known := lookbackBusyOf(group)("qg-a")
	want := observability.LookbackBusyFacts{Capturing: true, SamplePending: true, ProbePending: true, ProbationRereads: 2,
		DirectedPending: 3, ReadsInFlight: 4, TrialPrepared: true}
	if !known || facts != want || group.asked != "qg-a" {
		t.Fatalf("reading %+v (%v) for %q, want %+v for qg-a", facts, known, group.asked, want)
	}
	if lookbackBusy(nil) != nil {
		t.Fatal("no lookback has a reading")
	}
}
