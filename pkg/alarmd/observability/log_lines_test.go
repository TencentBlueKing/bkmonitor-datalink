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
	"errors"
	"strings"
	"testing"
	"time"
)

// The observer counts, by stage, the lines it wrote and the ones its limiter
// held back, every stage present from the start: which stage fills the log
// is one reading. A routine success the policy never writes, and the stages
// the observer never logs by design, are not lines and are not counted; a
// stage outside the closed list counts as _other.
func TestTheLogObserverCountsItsLinesByStage(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 2})
	policy, _ := NewBoundedLogPolicy(limiter)
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	ctx := context.Background()
	if counts := observer.LineCounts(); len(counts.Written) != len(logStages) || len(counts.Limited) != len(logStages) ||
		counts.Written[StageOther] != 0 {
		t.Fatalf("stages counted %d / %d, want every stage and _other present from the start", len(counts.Written), len(counts.Limited))
	}
	for range 5 {
		observer.Observe(ctx, Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused, Err: errors.New("pool full")})
	}
	for range 3 {
		observer.Observe(ctx, Observation{Component: ComponentRuntime, Stage: StageStartup, Result: ResultSuccess})
	}
	// Not lines: a routine success, a scheduler wait, a catalog object read.
	observer.Observe(ctx, Observation{Component: ComponentRuntime, Stage: StageFleetSnapshotPublish, Result: ResultSuccess})
	observer.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotWait, Result: ResultSuccess, Duration: time.Millisecond})
	observer.Observe(ctx, Observation{Component: ComponentControlPlane, Stage: StageObjectRead, Result: ResultSuccess})
	// A stage nobody listed, failed: written, under _other.
	observer.Observe(ctx, Observation{Component: ComponentRuntime, Stage: Stage("not_a_stage"), Result: ResultFailed, Err: errors.New("x")})

	counts := observer.LineCounts()
	if counts.Written[StageResourceHard] != 2 || counts.Limited[StageResourceHard] != 3 {
		t.Errorf("resource_hard written %d limited %d, want 2 and 3", counts.Written[StageResourceHard], counts.Limited[StageResourceHard])
	}
	if counts.Written[StageStartup] != 3 || counts.Limited[StageStartup] != 0 {
		t.Errorf("startup written %d limited %d, want 3 and 0", counts.Written[StageStartup], counts.Limited[StageStartup])
	}
	for _, stage := range []Stage{StageFleetSnapshotPublish, StageSlotWait, StageObjectRead} {
		if counts.Written[stage] != 0 || counts.Limited[stage] != 0 {
			t.Errorf("%s counted %d / %d, want nothing: not a line", stage, counts.Written[stage], counts.Limited[stage])
		}
	}
	if counts.Written[StageOther] != 1 {
		t.Errorf("_other written %d, want the unlisted stage's line", counts.Written[StageOther])
	}
	written := 0
	for _, n := range counts.Written {
		written += int(n)
	}
	if lines := strings.Count(strings.TrimSpace(output.String()), "\n") + 1; lines != written {
		t.Errorf("%d lines in the log, %d counted written", lines, written)
	}
}
