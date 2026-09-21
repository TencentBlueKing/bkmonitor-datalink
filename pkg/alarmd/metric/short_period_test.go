// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestShortPeriodCompletionCountsCommitKindNotQueryOrUnfinishedAttempt(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	o := observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Operation: observability.OperationNormal, Result: observability.ResultSuccess, Duration: time.Second}
	r.Observe(context.Background(), o)
	o.ShortPeriodCompletion = &observability.ShortPeriodCompletionFacts{Cohort: "10s", CompletionKind: "FULL_COMPLETED", LagSeconds: 20}
	r.Observe(context.Background(), o)
	o.ShortPeriodCompletion = &observability.ShortPeriodCompletionFacts{Cohort: "10s", CompletionKind: "SNAPSHOT_UNAVAILABLE", LagSeconds: 21}
	o.Result = observability.ResultDegraded
	r.Observe(context.Background(), o)
	o.Component = observability.ComponentAccess
	o.Stage = observability.StageQueryCompleted
	r.Observe(context.Background(), o)
	for _, kind := range []string{"FULL_COMPLETED", "SNAPSHOT_UNAVAILABLE"} {
		if got := testutil.ToFloat64(r.phaseTwo.shortPeriod.completed.WithLabelValues("10s", "normal", kind)); got != 1 {
			t.Fatalf("%s count=%v", kind, got)
		}
	}
}

// The two histograms are told apart by completion kind, and every cohort and
// kind exists from construction. A query-free closure's lag is how late the
// skip was booked; mixed with the executed kinds it made the 15s and 30s
// cohorts' p99 a statistic of skips, and the 10s cohort's one-percent tail
// past fifteen seconds was interpolated to twenty-two between the 15 and 25
// edges. 12 and 20 are edges now, so the tail is read off a bucket.
func TestShortPeriodHistogramsAreToldApartByKindWithEdgesWhereTheTailIs(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	// Every cohort and kind exists before anything completes -- read through
	// the collector, not through WithLabelValues, which would create them.
	want := len(observability.ShortPeriodCohorts) * len(observability.ShortPeriodCompletionKinds)
	if got := testutil.CollectAndCount(r.phaseTwo.shortPeriod.lag); got != want {
		t.Fatalf("lag series at construction = %d, want %d: a kind that never completed must publish a zero", got, want)
	}
	if got := testutil.CollectAndCount(r.phaseTwo.shortPeriod.duration); got != want {
		t.Fatalf("duration series at construction = %d, want %d", got, want)
	}
	o := observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Operation: observability.OperationNormal, Result: observability.ResultSuccess, Duration: 2 * time.Second}
	o.ShortPeriodCompletion = &observability.ShortPeriodCompletionFacts{Cohort: "15s", CompletionKind: "FULL_COMPLETED", LagSeconds: 18, AttemptNo: 1}
	r.Observe(context.Background(), o)
	o.Duration = time.Millisecond
	o.ShortPeriodCompletion = &observability.ShortPeriodCompletionFacts{Cohort: "15s", CompletionKind: "GAP_SKIPPED", LagSeconds: 59, AttemptNo: 1}
	r.Observe(context.Background(), o)
	// Each kind counted on its own series; the other kinds exist at zero.
	for kind, want := range map[string]uint64{"FULL_COMPLETED": 1, "GAP_SKIPPED": 1, "SNAPSHOT_UNAVAILABLE": 0} {
		if got := histogramCount(t, r.phaseTwo.shortPeriod.lag, "15s", kind); got != want {
			t.Fatalf("lag count for %s = %d, want %d: the kinds share one histogram or the zero series is missing", kind, got, want)
		}
		if got := histogramCount(t, r.phaseTwo.shortPeriod.duration, "15s", kind); got != want {
			t.Fatalf("duration count for %s = %d, want %d", kind, got, want)
		}
	}
	// The executed lag of 18 s lands between the 15 and 20 edges, so the 20
	// bucket has it and the 15 bucket does not: a quantile in the tail is
	// read off an edge, not made up between 15 and 25.
	buckets := histogramBuckets(t, r.phaseTwo.shortPeriod.lag, "15s", "FULL_COMPLETED")
	if buckets[15] != 0 || buckets[20] != 1 || buckets[12] != 0 {
		t.Fatalf("lag buckets for an 18 s execution = %v, want cumulative 12:0 15:0 20:1", buckets)
	}
	for _, edge := range []float64{12, 20} {
		if _, present := buckets[edge]; !present {
			t.Fatalf("no bucket edge at %v: the tail past fifteen seconds has no edge to be read off", edge)
		}
	}
}

// histogramCount and histogramBuckets read one labelled histogram back
// through its wire form, which is the only public way at the sample count
// and the cumulative buckets.
func histogramCount(t *testing.T, vec *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	return histogramProto(t, vec, labels...).GetSampleCount()
}

func histogramBuckets(t *testing.T, vec *prometheus.HistogramVec, labels ...string) map[float64]uint64 {
	t.Helper()
	buckets := map[float64]uint64{}
	for _, bucket := range histogramProto(t, vec, labels...).GetBucket() {
		buckets[bucket.GetUpperBound()] = bucket.GetCumulativeCount()
	}
	return buckets
}

func histogramProto(t *testing.T, vec *prometheus.HistogramVec, labels ...string) *dto.Histogram {
	t.Helper()
	var metric dto.Metric
	if err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(&metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetHistogram()
}
