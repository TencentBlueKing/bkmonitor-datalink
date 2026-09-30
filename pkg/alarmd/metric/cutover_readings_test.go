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
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The last successful cutover's duration is set with its payload and
// timelines read; the first successful cutover of the process is kept and
// never overwritten; every successful payload lands in the distribution. A
// failed cutover touches none of them.
func TestTheLastAndFirstCutoverAreReadExactly(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	cutover := func(result string, duration time.Duration, payload, timelines int) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageScheduleCutover,
			ScheduleCutover: &observability.ScheduleCutoverFacts{Result: result, Duration: duration, PayloadBytes: payload, TimelinesRead: timelines},
		})
	}
	m := recorder.phaseTwo
	if testutil.ToFloat64(m.scheduleCutoverFirstDuration) != 0 {
		t.Fatal("a first cutover before any ran")
	}
	cutover("success", 12*time.Second, 1_786_370, 2819)
	cutover("success", 300*time.Millisecond, 24_000, 3)
	cutover("failure", 40*time.Second, 9_000_000, 2819)
	if got := testutil.ToFloat64(m.scheduleCutoverLastDuration); got != 0.3 {
		t.Fatalf("last duration %v, want the second cutover's 0.3", got)
	}
	if got := testutil.ToFloat64(m.scheduleCutoverPayload); got != 24_000 {
		t.Fatalf("last payload %v, want the same cutover's", got)
	}
	if got, timelines := testutil.ToFloat64(m.scheduleCutoverFirstDuration), testutil.ToFloat64(m.scheduleCutoverFirstTimelines); got != 12 || timelines != 2819 {
		t.Fatalf("first cutover %v s over %v timelines, want the first one kept", got, timelines)
	}
	if got := testutil.CollectAndCount(m.scheduleCutoverPayloadSize); got != 1 {
		t.Fatalf("payload distribution series %d", got)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	// The 12 second cutover sits between the 10 and 20 second buckets.
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_schedule_cutover_duration_seconds" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metric.GetLabel()[0].GetValue() != "success" {
				continue
			}
			cumulative := map[float64]uint64{}
			for _, bucket := range metric.GetHistogram().GetBucket() {
				cumulative[bucket.GetUpperBound()] = bucket.GetCumulativeCount()
			}
			if cumulative[5] != 1 || cumulative[10] != 1 || cumulative[20] != 2 {
				t.Fatalf("cumulative success counts %v, want the 12 s cutover between 10 and 20", cumulative)
			}
		}
	}
	for _, family := range families {
		if family.GetName() == "bkmonitor_alarmd_schedule_cutover_payload_size_bytes" {
			if count := family.GetMetric()[0].GetHistogram().GetSampleCount(); count != 2 {
				t.Fatalf("payload distribution counted %d cutovers, want the two that succeeded", count)
			}
			return
		}
	}
	t.Fatal("no payload distribution")
}
