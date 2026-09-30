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
// same Query Group would have written sixty lines an hour per stage.
func TestARoutinePacingReasonKeepsOneLineAnHourPerQueryGroupAndStage(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	first := limiter.Admit(deferredSlot(StageQueryCompleted, "qg-a"))
	if !first.Allowed || !first.Sampled || first.Suppressed != 0 {
		t.Fatalf("first deferral of the hour=%+v, want admitted as the sample with nothing merged", first)
	}
	// The other stage of the same round has its own hour, and its own sample.
	if got := limiter.Admit(deferredSlot(StageSlotCompleted, "qg-a")); !got.Allowed || !got.Sampled || got.Suppressed != 0 {
		t.Fatalf("first completion deferral of the hour=%+v, want its own sample", got)
	}
	// Fifty-nine more rounds inside the hour, two stages each: none admitted.
	for round := 1; round < 60; round++ {
		now = now.Add(time.Minute)
		for _, stage := range []Stage{StageQueryCompleted, StageSlotCompleted} {
			if got := limiter.Admit(deferredSlot(stage, "qg-a")); got.Allowed {
				t.Fatalf("round %d %s admitted under the pacing sample: %+v", round, stage, got)
			}
		}
	}
	// The hour turns on the sixtieth round; each stage's first line is its
	// sample and carries the 59 rounds of its own stage merged since.
	now = now.Add(time.Minute)
	second := limiter.Admit(deferredSlot(StageSlotCompleted, "qg-a"))
	if !second.Allowed || !second.Sampled || second.Suppressed != 59 {
		t.Fatalf("completion sample after an hour=%+v, want admitted with the 59 completion lines merged", second)
	}
	if got := limiter.Admit(deferredSlot(StageQueryCompleted, "qg-a")); !got.Allowed || !got.Sampled || got.Suppressed != 59 {
		t.Fatalf("query sample after an hour=%+v, want admitted with the 59 query lines merged, not the completion's", got)
	}
	if got := limiter.Admit(deferredSlot(StageQueryCompleted, "qg-a")); got.Allowed {
		t.Fatalf("second query line of the new hour admitted: %+v", got)
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

// Every sampled reason is a word the policy's fixed buckets know; a sample on
// a word outside the vocabulary would never be admitted at all, and the
// positive control would be silence.
func TestEverySampledReasonIsInTheLogVocabulary(t *testing.T) {
	t.Parallel()

	known := map[ReasonCode]bool{}
	for _, reason := range AllLogReasons() {
		known[reason] = true
	}
	sampled := PacingLogSampledReasons()
	if len(sampled) == 0 {
		t.Fatal("no reason is sampled: the pacing word writes every line again")
	}
	for _, reason := range sampled {
		if !known[reason] {
			t.Fatalf("sampled reason %q is not a log reason the limiter buckets", reason)
		}
		sample, ok := PacingLogSample(reason)
		if !ok || sample.Window < time.Hour || sample.MaxEvents != 1 {
			t.Fatalf("sample for %q=%+v, want one line per hour or slower", reason, sample)
		}
	}
	if _, ok := PacingLogSample(ReasonCode(contract.ReasonQueryUnavailable)); ok {
		t.Fatal("a failure word is under a pacing sample")
	}
}
