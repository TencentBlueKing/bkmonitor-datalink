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
		t.Fatal("observations without a Query Group did not fall back to the fixed reason bucket")
	}
	if !limiter.Admit(scopedFailure("qg-c")).Allowed {
		t.Fatal("fixed reason bucket traffic suppressed a scoped bucket")
	}
	if got := limiter.ScopeBuckets(); got != 4 {
		t.Fatalf("scope buckets=%d, want 4 (qg-a x2 reasons, qg-b, qg-c)", got)
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

func TestPhaseOneWindowLimiterAdmitDoesNotReportSuppressedCounts(t *testing.T) {
	t.Parallel()

	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	first := policy.Admit(scopedFailure("qg-a"))
	second := policy.Admit(scopedFailure("qg-b"))
	if !first.Allowed || second.Allowed || first.Suppressed != 0 || second.Suppressed != 0 {
		t.Fatalf("phase-one behaviour changed: first=%+v second=%+v", first, second)
	}
}
