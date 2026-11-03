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
	"strings"
	"testing"
	"time"
)

// A successful slot_completed line of a round read at its read hold limit
// is written, carrying read_at_limit, under its Query Group's repeated-line
// limit; the same line of a round read whole is routine, counted and not
// written.
func TestARoundReadAtTheLimitWritesItsCompletion(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 2, MaxScopes: 1024})
	policy, _ := NewScopedBoundedLogPolicy(limiter)
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	ctx := context.Background()
	for range 3 {
		observer.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
			ReadAtLimit: true, Trace: TraceFields{QueryGroupKey: "qg-limit"}})
	}
	for range 4 {
		observer.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
			Trace: TraceFields{QueryGroupKey: "qg-whole"}})
	}
	counts := observer.LineCounts()
	written, limited := counts.Written[StageSlotCompleted], counts.Limited[StageSlotCompleted]
	if written == 0 || written+limited != 3 || counts.Unwritten[StageSlotCompleted] != 4 {
		t.Fatalf("slot_completed written %d limited %d unwritten %d, want the three rounds at the limit written or held back by the limiter, and the four whole rounds unwritten",
			written, limited, counts.Unwritten[StageSlotCompleted])
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if uint64(len(lines)) != written {
		t.Fatalf("wrote %d lines, counted %d: %s", len(lines), written, output.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, `"read_at_limit":true`) || !strings.Contains(line, "qg-limit") {
			t.Fatalf("line %s, want qg-limit's completion with read_at_limit", line)
		}
	}
}
