// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func deferredSlot(stage Stage, queryGroup string) Observation {
	return Observation{
		Component: ComponentScheduler, Stage: stage, Result: ResultRetrying, Operation: OperationNormal,
		ReasonCode: ReasonCode(contract.ReasonQueryNotReady),
		Trace:      TraceFields{QueryGroupKey: queryGroup},
	}
}

// A Query Group deferred every round writes one line an hour per stage that
// says QUERY_NOT_READY, and each line carries how many lines of its own stage
// were merged into it since the previous one. Per stage, because the sample
// is the positive control for a reader who greps one stage: one bucket for
// both would hand that reader zero lines in the hours the other stage's line
// came first, which is what a dead emitter also gives. The policy's own
// window (a minute, one line) does not apply to the pacing word: under it the
// same Query Group would have written sixty lines an hour per stage. A
// bucket's hours are set at a point of its own (sampleWindowStart), so its
// first line comes at once and the next at the turn of its own hour.
func TestARoutinePacingReasonKeepsOneLineAnHourPerQueryGroupAndStage(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	first := limiter.Admit(deferredSlot(StageQueryCompleted, "qg-a"))
	if !first.Allowed || !first.Sampled || first.Suppressed != 0 || first.SampleWindowStart.After(now) || now.Sub(first.SampleWindowStart) >= time.Hour {
		t.Fatalf("first deferral of the hour=%+v, want admitted as the sample with nothing merged, in the hour its window holds", first)
	}
	// The other stage of the same round has its own hour, and its own sample.
	if got := limiter.Admit(deferredSlot(StageSlotCompleted, "qg-a")); !got.Allowed || !got.Sampled || got.Suppressed != 0 {
		t.Fatalf("first completion deferral of the hour=%+v, want its own sample", got)
	}
	// Two hours of rounds, a minute apart: each stage is admitted once a
	// window, at the first round of each of its own windows, carrying the
	// rounds of its own stage merged since its line before.
	last := map[Stage]time.Time{StageQueryCompleted: now, StageSlotCompleted: now}
	windows := map[Stage]time.Time{StageQueryCompleted: first.SampleWindowStart}
	admitted := map[Stage]int{}
	for round := 1; round <= 120; round++ {
		now = now.Add(time.Minute)
		for _, stage := range []Stage{StageQueryCompleted, StageSlotCompleted} {
			got := limiter.Admit(deferredSlot(stage, "qg-a"))
			if !got.Allowed {
				continue
			}
			admitted[stage]++
			if window, known := windows[stage]; known && got.SampleWindowStart.Sub(window) != time.Hour {
				t.Fatalf("round %d %s admitted in a window %v after the last, want the next hour of its own", round, stage, got.SampleWindowStart.Sub(window))
			}
			if now.Sub(got.SampleWindowStart) >= time.Minute {
				t.Fatalf("round %d %s admitted %v into its window, want its first round", round, stage, now.Sub(got.SampleWindowStart))
			}
			if want := uint64(now.Sub(last[stage])/time.Minute) - 1; !got.Sampled || got.Suppressed != want {
				t.Fatalf("round %d %s sample=%+v, want %d lines of its own stage merged", round, stage, got, want)
			}
			windows[stage], last[stage] = got.SampleWindowStart, now
		}
	}
	for _, stage := range []Stage{StageQueryCompleted, StageSlotCompleted} {
		if admitted[stage] != 2 {
			t.Fatalf("%s admitted %d times over two hours after its first line, want once an hour", stage, admitted[stage])
		}
	}
	// Another Query Group has its own hour, and another reason on the same
	// Query Group keeps the policy's window: a minute later it is admitted
	// again, unsampled.
	if got := limiter.Admit(deferredSlot(StageQueryCompleted, "qg-b")); !got.Allowed || !got.Sampled || got.Suppressed != 0 {
		t.Fatalf("another Query Group's first deferral=%+v", got)
	}
	failure := limiter.Admit(scopedFailure("qg-a"))
	if !failure.Allowed || failure.Sampled {
		t.Fatalf("a failure on the sampled Query Group=%+v, want admitted under the shared window, not sampled", failure)
	}
	now = now.Add(time.Minute)
	if got := limiter.Admit(scopedFailure("qg-a")); !got.Allowed || got.Sampled {
		t.Fatalf("a failure a minute later=%+v, want the shared window (a minute), not the sample's hour", got)
	}
}

// The sampled line says it is the sample and carries the merged count even
// when the count is zero; an ordinary admitted line with nothing merged
// carries neither.
func TestTheSampledLineSaysSoAndCarriesTheMergedCountEvenAtZero(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 600, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	observer.Observe(context.Background(), deferredSlot(StageSlotCompleted, "qg-a"))
	for i := 0; i < 7; i++ {
		observer.Observe(context.Background(), deferredSlot(StageSlotCompleted, "qg-a"))
	}
	observer.Observe(context.Background(), scopedFailure("qg-a"))
	now = now.Add(time.Hour)
	observer.Observe(context.Background(), deferredSlot(StageSlotCompleted, "qg-a"))

	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("lines=%d, want the first sample, the failure, the second sample:\n%s", len(lines), output.String())
	}
	var first, failure, second map[string]any
	for i, into := range []*map[string]any{&first, &failure, &second} {
		if err := json.Unmarshal(lines[i], into); err != nil {
			t.Fatal(err)
		}
	}
	if first["sampled"] != true || first["suppressed_logs"] != float64(0) || first["reason_code"] != contract.ReasonQueryNotReady {
		t.Fatalf("first sample=%v, want sampled with suppressed_logs 0 on the line", first)
	}
	if _, has := failure["sampled"]; has || failure["suppressed_logs"] != nil {
		t.Fatalf("an ordinary admitted line=%v, want neither sampled nor a zero merged count", failure)
	}
	if second["sampled"] != true || second["suppressed_logs"] != float64(7) {
		t.Fatalf("second sample=%v, want sampled with the 7 merged", second)
	}
}

// Every reason an observation can carry once normalized has a bucket in the
// limiter: a word it has none for is refused outright and its lines are never
// written. The completion attribution words were normalized and not listed,
// and so never reached the log.
func TestEveryNormalizedReasonHasALimiterBucket(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1 << 12}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	listed := map[ReasonCode]bool{}
	for _, reason := range AllLogReasons() {
		listed[reason] = true
	}
	for _, reason := range append(append([]ReasonCode(nil), CompletionAttributionReasons...), AdmissionFailureReasons...) {
		if !listed[reason] {
			t.Fatalf("%q is normalized but not a log reason", reason)
		}
	}
	for reason := range listed {
		if reason == ReasonNone {
			continue
		}
		if got := NormalizeReason(reason, ResultDegraded); got != reason {
			t.Fatalf("%q normalizes to %q", reason, got)
		}
		observation := Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultDegraded,
			ReasonCode: reason, Trace: TraceFields{QueryGroupKey: "qg-" + string(reason)}}
		if !limiter.Admit(observation).Allowed {
			t.Fatalf("%q has no limiter bucket: its first line was refused", reason)
		}
	}
	if got := NormalizeReason("NOT_A_REASON", ResultDegraded); got != ReasonOther {
		t.Fatalf("an unlisted word normalizes to %q, want %q", got, ReasonOther)
	}
}

// Query Groups whose first deferral comes in one minute - every group a
// process starts with or takes over - write their next sampled line spread
// over the hour, each at the turn of its own: not all of them together an
// hour on, which reads as a burst of what the lines stand for when there is
// none.
func TestSamplesBegunInOneMinuteComeSpreadOverTheHour(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1024}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	const groups = 240
	for index := 0; index < groups; index++ {
		if got := limiter.Admit(deferredSlot(StageQueryCompleted, fmt.Sprintf("qg-%d", index))); !got.Allowed {
			t.Fatalf("group %d's first deferral not admitted", index)
		}
	}
	perMinute := map[int]int{}
	for minute := 1; minute <= 60; minute++ {
		now = now.Add(time.Minute)
		for index := 0; index < groups; index++ {
			if got := limiter.Admit(deferredSlot(StageQueryCompleted, fmt.Sprintf("qg-%d", index))); got.Allowed {
				perMinute[minute]++
			}
		}
	}
	total, busiest := 0, 0
	for _, count := range perMinute {
		total, busiest = total+count, max(busiest, count)
	}
	if total != groups {
		t.Fatalf("%d second lines in the hour, want one per group", total)
	}
	// 240 lines over 60 minutes average 4 a minute; one minute taking a
	// sixth of them would be the burst again.
	if len(perMinute) < 40 || busiest > groups/6 {
		t.Fatalf("second lines in %d of 60 minutes, at most %d in one, want them spread over the hour: %v", len(perMinute), busiest, perMinute)
	}
}

// A sampled line names when the window it opened began, so a reader counts
// the lines merged into it from there.
func TestASampledLineNamesItsWindowStart(t *testing.T) {
	var out bytes.Buffer
	logger := New("alarmd", &out)
	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	observation := deferredSlot(StageQueryCompleted, "qg-a")
	admission := limiter.Admit(observation)
	logger.logObservation(context.Background(), observation, admission)
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("line %q: %v", out.String(), err)
	}
	if got, ok := line["sampling_window_start_ms"].(float64); !ok || int64(got) != admission.SampleWindowStart.UnixMilli() {
		t.Fatalf("line %q, want sampling_window_start_ms %d", out.String(), admission.SampleWindowStart.UnixMilli())
	}
}
