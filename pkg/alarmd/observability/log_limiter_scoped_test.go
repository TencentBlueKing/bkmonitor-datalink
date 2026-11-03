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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func scopedFailure(queryGroup string) Observation {
	return Observation{
		Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed,
		ReasonCode: ReasonCode(contract.ReasonQueryUnavailable),
		Trace:      TraceFields{QueryGroupKey: queryGroup},
	}
}

func TestScopedLogLimiterRejectsMissingBounds(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]ScopedLogLimiterConfig{
		"window":   {MaxEvents: 1, MaxScopes: 1},
		"capacity": {Window: time.Second, MaxScopes: 1},
		"scopes":   {Window: time.Second, MaxEvents: 1},
	} {
		if _, err := NewScopedLogLimiter(config); err == nil {
			t.Fatalf("NewScopedLogLimiter(%s) returned nil error", name)
		}
	}
	if _, err := NewScopedBoundedLogPolicy(nil); err == nil {
		t.Fatal("NewScopedBoundedLogPolicy(nil) returned nil error")
	}
	// A logging observer without a policy writes nothing.
	var output bytes.Buffer
	NewLoggingObserver(New("runtime", &output), nil).Observe(context.Background(), Observation{
		Component: ComponentRuntime, Stage: StageStartup, Result: ResultStarted,
	})
	if output.Len() != 0 {
		t.Fatalf("nil policy emitted logs: %s", output.String())
	}
}

func TestScopedLogLimiterKeepsOneLinePerReasonAndQueryGroup(t *testing.T) {
	t.Parallel()

	limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16})
	if err != nil {
		t.Fatal(err)
	}
	if !limiter.Admit(scopedFailure("qg-a")).Allowed {
		t.Fatal("first line for qg-a was suppressed")
	}
	if !limiter.Admit(scopedFailure("qg-b")).Allowed {
		t.Fatal("qg-a traffic suppressed the independent qg-b bucket for the same reason")
	}
	if limiter.Admit(scopedFailure("qg-a")).Allowed || limiter.Admit(scopedFailure("qg-b")).Allowed {
		t.Fatal("second line within the window was admitted")
	}
	other := scopedFailure("qg-a")
	other.ReasonCode = ReasonCode(contract.ReasonQueryTimeout)
	if !limiter.Admit(other).Allowed {
		t.Fatal("a different reason for the same Query Group shares the bucket")
	}
	noScope := scopedFailure("")
	if !limiter.Admit(noScope).Allowed || limiter.Admit(noScope).Allowed {
		t.Fatal("observations without a Query Group did not keep their stage's own bucket")
	}
	if !limiter.Admit(scopedFailure("qg-c")).Allowed {
		t.Fatal("a line without a Query Group suppressed a Query Group's bucket")
	}
	if got := limiter.ScopeBuckets(); got != 5 {
		t.Fatalf("scope buckets=%d, want 5 (qg-a x2 reasons, qg-b, the stage without a Query Group, qg-c)", got)
	}
}

func TestScopedLogLimiterReportsSuppressedCountOnNextAdmittedLine(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if got := limiter.Admit(scopedFailure("qg-a")); !got.Allowed || got.Suppressed != 0 {
		t.Fatalf("first admission=%+v", got)
	}
	for i := 0; i < 3; i++ {
		if limiter.Admit(scopedFailure("qg-a")).Allowed {
			t.Fatal("line within the window was admitted")
		}
	}
	limiter.Admit(scopedFailure("qg-b"))
	limiter.Admit(scopedFailure("qg-b"))
	now = now.Add(time.Minute)
	if got := limiter.Admit(scopedFailure("qg-a")); !got.Allowed || got.Suppressed != 3 || got.SuppressedEvicted != 0 {
		t.Fatalf("rollover admission for qg-a=%+v, want 3 suppressed", got)
	}
	if got := limiter.Admit(scopedFailure("qg-b")); !got.Allowed || got.Suppressed != 1 {
		t.Fatalf("rollover admission for qg-b=%+v, want 1 suppressed", got)
	}
	now = now.Add(time.Minute)
	if got := limiter.Admit(scopedFailure("qg-a")); !got.Allowed || got.Suppressed != 0 {
		t.Fatalf("suppressed count was not reset after reporting: %+v", got)
	}
}

func TestScopedLogLimiterBoundsScopeBucketsAndFoldsEvictedCounts(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 2}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	limiter.Admit(scopedFailure("qg-a"))
	limiter.Admit(scopedFailure("qg-a"))
	limiter.Admit(scopedFailure("qg-a"))
	limiter.Admit(scopedFailure("qg-b"))
	if got := limiter.ScopeBuckets(); got != 2 {
		t.Fatalf("scope buckets=%d, want 2", got)
	}
	// qg-b was touched after qg-a, so qg-a is the least recently used bucket
	// and is evicted with two suppressed lines pending. The very next admitted
	// line of the same reason (here qg-c itself) reports them as evicted.
	got := limiter.Admit(scopedFailure("qg-c"))
	if !got.Allowed || got.SuppressedEvicted != 2 || limiter.ScopeBuckets() != 2 {
		t.Fatalf("third scope admission=%+v buckets=%d, want 2 evicted suppressed lines within the bound", got, limiter.ScopeBuckets())
	}
	if got := limiter.Admit(scopedFailure("qg-d")); !got.Allowed || got.SuppressedEvicted != 0 {
		t.Fatalf("evicted count was reported twice: %+v", got)
	}
	for i := 0; i < 1000; i++ {
		limiter.Admit(scopedFailure(fmt.Sprintf("qg-%d", i)))
	}
	if got := limiter.ScopeBuckets(); got != 2 {
		t.Fatalf("scope buckets grew past the bound: %d", got)
	}
	// Buckets evicted without suppressed lines contribute nothing.
	now = now.Add(time.Minute)
	if got := limiter.Admit(scopedFailure("qg-z")); !got.Allowed || got.Suppressed != 0 || got.SuppressedEvicted != 0 {
		t.Fatalf("clean evictions leaked counts: %+v", got)
	}
}

func TestScopedLogLimiterEnforcesCapacityConcurrently(t *testing.T) {
	t.Parallel()

	limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 3, MaxScopes: 8})
	if err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 200; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if limiter.Admit(scopedFailure(fmt.Sprintf("qg-%d", index%4))).Allowed {
				allowed.Add(1)
			}
		}(index)
	}
	wait.Wait()
	if got := allowed.Load(); got != 12 {
		t.Fatalf("allowed events = %d, want 3 per Query Group across 4 groups", got)
	}
	if got := limiter.ScopeBuckets(); got != 4 {
		t.Fatalf("scope buckets=%d, want 4", got)
	}
}

func TestLoggingObserverEmitsSuppressedLogsPerQueryGroupFromContext(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	ctxA := ContextWithTraceFields(context.Background(), TraceFields{QueryGroupKey: "qg-a"})
	ctxB := ContextWithTraceFields(context.Background(), TraceFields{QueryGroupKey: "qg-b"})
	failed := Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed, ReasonCode: ReasonCode(contract.ReasonQueryUnavailable)}
	observer.Observe(ctxA, failed)
	observer.Observe(ctxA, failed)
	observer.Observe(ctxA, failed)
	observer.Observe(ctxB, failed)
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d, want one per Query Group: %s", len(lines), output.String())
	}
	for index, want := range []string{"qg-a", "qg-b"} {
		var event map[string]any
		if err := json.Unmarshal([]byte(lines[index]), &event); err != nil {
			t.Fatal(err)
		}
		if event["query_group_key"] != want || event["suppressed_logs"] != nil {
			t.Fatalf("line %d=%v, want query_group_key=%s without suppressed_logs", index, event, want)
		}
	}
	output.Reset()
	now = now.Add(time.Minute)
	observer.Observe(ctxA, failed)
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode: %v; log=%s", err, output.String())
	}
	if event["query_group_key"] != "qg-a" || event["suppressed_logs"] != float64(2) {
		t.Fatalf("rollover line=%v, want suppressed_logs=2 for qg-a", event)
	}
}

// The clock is read under the limiter's lock: a caller that read the time
// before another finished admission could otherwise look like a clock that
// ran backwards and reset the window.
func TestScopedLogLimiterSerializesClockWithAdmission(t *testing.T) {
	t.Parallel()

	start := time.Unix(1_000, 0)
	observation := Observation{ReasonCode: ReasonRSS, Result: ResultFailed}
	var limiter *ScopedLogLimiter
	var calls atomic.Int64
	var allowed atomic.Int64
	clock := func() time.Time {
		call := calls.Add(1)
		captured := start.Add(time.Duration(call) * time.Nanosecond)
		// If the clock runs outside the critical section, deterministically
		// let a later caller finish admission before returning this timestamp.
		// TryLock avoids timing assumptions and cannot unlock another caller.
		if call == 1 && limiter.mu.TryLock() {
			limiter.mu.Unlock()
			done := make(chan struct{})
			go func() {
				defer close(done)
				if limiter.Admit(observation).Allowed {
					allowed.Add(1)
				}
			}()
			<-done
		}
		return captured
	}
	var err error
	limiter, err = newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16}, clock)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if limiter.Admit(observation).Allowed {
			allowed.Add(1)
		}
	}
	if got := allowed.Load(); got != 1 {
		t.Fatalf("allowed events = %d, want 1; all timestamps are within one minute", got)
	}
}

func TestScopedLogLimiterResetsOnItsWindowAndOnAClockRollback(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16},
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range []Observation{
		{ReasonCode: ReasonRSS, Result: ResultFailed},
		scopedFailure("qg-a"),
	} {
		admit := func() bool { return limiter.Admit(observation).Allowed }
		if !admit() || admit() {
			t.Fatalf("%+v: initial window capacity changed", observation)
		}
		now = now.Add(time.Minute)
		if !admit() || admit() {
			t.Fatalf("%+v: window expiry did not reset capacity", observation)
		}
		now = now.Add(-time.Second)
		if !admit() || admit() {
			t.Fatalf("%+v: actual clock rollback did not reset capacity", observation)
		}
	}
}

// An observation without a Query Group is bounded by its reason, or by its
// stage when it has none; each bucket is its own, and an unlisted reason
// folds into _other's rather than opening one of its own.
func TestScopedLogLimiterKeepsOneBucketPerReasonAndStageWithoutAQueryGroup(t *testing.T) {
	t.Parallel()

	limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 16})
	if err != nil {
		t.Fatal(err)
	}
	admit := func(observation Observation) bool { return limiter.Admit(observation).Allowed }
	failed := func(reason ReasonCode) Observation {
		return Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultFailed, ReasonCode: reason}
	}
	if !admit(failed(ReasonRSS)) || admit(failed(ReasonRSS)) {
		t.Fatal("RSS bucket did not enforce its capacity")
	}
	if !admit(failed(ReasonCPU)) {
		t.Fatal("RSS traffic suppressed the independent CPU bucket")
	}
	if !admit(failed(ReasonCode(contract.ReasonRecordInvalid))) {
		t.Fatal("resource traffic suppressed the independent contract reason bucket")
	}
	startup := Observation{Component: ComponentRuntime, Stage: StageStartup, Result: ResultFailed, ReasonCode: ReasonNone}
	if !admit(startup) || admit(startup) {
		t.Fatal("a failed observation without a reason did not use its bounded stage bucket")
	}
	if !admit(Observation{Component: ComponentRuntime, Stage: StageShutdown, Result: ResultFailed}) {
		t.Fatal("one stage's traffic suppressed another stage's bucket")
	}
	if !admit(failed("UNKNOWN_REASON")) || admit(failed("ANOTHER_UNKNOWN_REASON")) {
		t.Fatal("two unlisted reasons did not share _other's bucket")
	}
}
