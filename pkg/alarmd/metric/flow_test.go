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
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The bounds the summary's block is read by are the metrics' own.
func TestTheFlowBoundsAreTheMetricsBounds(t *testing.T) {
	if !slices.Equal(fleet.FlowQueryBounds, QueryDurationBuckets) || !slices.Equal(fleet.FlowDecisionBounds, DecisionLagBuckets) ||
		!slices.Equal(fleet.FlowRedisBounds, redisDurationBuckets) {
		t.Fatal("the flow block's bounds are not the metrics' bounds")
	}
}

// A physical query is one count on the work series that always read zero,
// one duration, and, when it failed, one count under its cause; a cause the
// closed list does not know is other.
func TestAPhysicalQueryIsCountedTimedAndClassifiedOnce(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.RecordPhysicalQuery(1500*time.Millisecond, "")
	r.RecordPhysicalQuery(20*time.Millisecond, "refused")
	r.RecordPhysicalQuery(time.Second, "a cause from elsewhere")
	if got := testutil.ToFloat64(r.phaseTwo.work.WithLabelValues("physical_query")); got != 3 {
		t.Fatalf("physical queries = %v, want 3", got)
	}
	if got := testutil.CollectAndCount(r.phaseTwo.flow.queryDuration); got != 1 {
		t.Fatalf("duration series = %d", got)
	}
	if refused, other := testutil.ToFloat64(r.phaseTwo.flow.queryFailures.WithLabelValues("refused")),
		testutil.ToFloat64(r.phaseTwo.flow.queryFailures.WithLabelValues("other")); refused != 1 || other != 1 {
		t.Fatalf("refused %v other %v, want 1 and 1", refused, other)
	}
	flow := r.FlowCounters()
	if flow.Queries != 3 || flow.QueryFailures["refused"] != 1 || flow.QueryFailures["other"] != 1 || flow.QueryDuration.Count != 3 ||
		len(flow.QueryDuration.Buckets) != len(QueryDurationBuckets) || flow.QueryDuration.Buckets[0] != 1 {
		t.Fatalf("flow = %+v", flow)
	}
}

// The decision lag is one histogram with its own buckets, reaching
// minutes; rejected events are also counted without labels, at the same
// place as the labelled count.
func TestTheDecisionLagAndTheRejectedEventsAreCopiedIntoTheFlow(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.RecordDecisionLag(45 * time.Second)
	r.RecordDecisionLag(400 * time.Millisecond)
	r.Observe(context.Background(), observabilityRejectedWrite(2))
	flow := r.FlowCounters()
	if flow.DecisionLag.Count != 2 || len(flow.DecisionLag.Buckets) != len(DecisionLagBuckets) || flow.DecisionLag.Buckets[1] != 1 {
		t.Fatalf("decision lag = %+v", flow.DecisionLag)
	}
	if flow.EventsRejected != 2 {
		t.Fatalf("rejected = %d, want 2", flow.EventsRejected)
	}
}

func observabilityRejectedWrite(n int) observability.Observation {
	write := &observability.OutputWriteFacts{}
	for i := 0; i < n; i++ {
		write.Rejected = append(write.Rejected, observability.OutputRejectedEvent{StrategyID: "1", Rule: "any"})
	}
	return observability.Observation{Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
		Result: observability.ResultSuccess, OutputWrite: write}
}

// A Slot booked without a decision -- skipped, or without its snapshot --
// is not a detection.
func TestASlotBookedWithoutADecisionIsNotADetection(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for _, kind := range []string{"FULL_COMPLETED", "GAP_SKIPPED", "SNAPSHOT_UNAVAILABLE", "COMPLETED_WITH_PARTIAL_GAP"} {
		r.phaseTwo.workflow.progress.WithLabelValues(kind).Inc()
	}
	if got := r.FlowCounters().Detections; got != 2 {
		t.Fatalf("detections = %d, want the full and the partial-gap completions", got)
	}
}

// The runtime round trips are read under the label of the client that
// serves runtime state: its own client when the process opened one, the
// source's when the two share a connection and so a client.
func TestTheRuntimeRoundTripsFollowTheClientThatServesRuntimeState(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.phaseTwo.redisCalls.duration.WithLabelValues("runtime", "evalsha", "false").Observe(0.001)
	r.phaseTwo.redisCalls.duration.WithLabelValues("source", "evalsha", "false").Observe(0.002)
	r.phaseTwo.redisCalls.duration.WithLabelValues("source", "get", "false").Observe(0.003)
	if got := r.FlowCounters().RuntimeRedis.Count; got != 1 {
		t.Fatalf("separate client: %d round trips, want the runtime client's one", got)
	}
	r.SetRuntimeRedisClient("source")
	if got := r.FlowCounters().RuntimeRedis.Count; got != 2 {
		t.Fatalf("shared client: %d round trips, want the source client's two", got)
	}
}
