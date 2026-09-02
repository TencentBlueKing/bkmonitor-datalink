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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestWindowLogLimiterRejectsMissingBounds(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]WindowLogLimiterConfig{
		"window":   {MaxEvents: 1},
		"capacity": {Window: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewWindowLogLimiter(config); err == nil {
				t.Fatal("NewWindowLogLimiter() returned nil error")
			}
		})
	}
	if _, err := NewBoundedLogPolicy(nil); err == nil {
		t.Fatal("NewBoundedLogPolicy(nil) returned nil error")
	}
	var output bytes.Buffer
	NewLoggingObserver(New("runtime", &output), nil).Observe(context.Background(), Observation{
		Component: ComponentRuntime, Stage: StageStartup, Result: ResultStarted,
	})
	if output.Len() != 0 {
		t.Fatalf("nil policy emitted logs: %s", output.String())
	}
}

func TestWindowLogLimiterEnforcesCapacityConcurrently(t *testing.T) {
	t.Parallel()

	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 10})
	if err != nil {
		t.Fatalf("NewWindowLogLimiter() error = %v", err)
	}
	var allowed atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if limiter.Allow(Observation{ReasonCode: ReasonRSS, Result: ResultFailed}) {
				allowed.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed events = %d, want 10", got)
	}
}

func TestWindowLogLimiterIsolatesReasonAndNoneStageBuckets(t *testing.T) {
	t.Parallel()

	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 1})
	if err != nil {
		t.Fatalf("NewWindowLogLimiter() error = %v", err)
	}
	failed := func(reason ReasonCode) Observation {
		return Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultFailed, ReasonCode: reason}
	}
	if !limiter.Allow(failed(ReasonRSS)) || limiter.Allow(failed(ReasonRSS)) {
		t.Fatal("RSS bucket did not enforce its capacity")
	}
	if !limiter.Allow(failed(ReasonCPU)) {
		t.Fatal("RSS traffic suppressed the independent CPU bucket")
	}
	if !limiter.Allow(failed(ReasonCode(contract.ReasonRecordInvalid))) {
		t.Fatal("resource traffic suppressed the independent M0 reason bucket")
	}
	if !limiter.Allow(Observation{Component: ComponentDetect, Stage: StageDetectCompleted, Result: ResultSuccess}) {
		t.Fatal("detect reason-none stage bucket was not admitted")
	}
	if !limiter.Allow(Observation{Component: ComponentTrigger, Stage: StageTriggerCompleted, Result: ResultSuccess}) {
		t.Fatal("detect reason-none traffic suppressed trigger stage")
	}
	emptyReasonLimiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 1})
	if err != nil {
		t.Fatalf("NewWindowLogLimiter() for empty reasons error = %v", err)
	}
	if !emptyReasonLimiter.Allow(Observation{Component: ComponentDetect, Stage: StageDetectCompleted, Result: ResultFailed}) {
		t.Fatal("reason-empty failed observation did not use the detect stage bucket")
	}
	if !emptyReasonLimiter.Allow(Observation{Component: ComponentTrigger, Stage: StageTriggerCompleted, Result: ResultFailed}) {
		t.Fatal("reason-empty failed detect traffic suppressed trigger stage")
	}
	explicitNoneLimiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Minute, MaxEvents: 1})
	if err != nil {
		t.Fatalf("NewWindowLogLimiter() for explicit none error = %v", err)
	}
	failedNone := Observation{
		Component: ComponentDetect, Stage: StageDetectCompleted, Result: ResultFailed, ReasonCode: ReasonNone,
	}
	if !explicitNoneLimiter.Allow(failedNone) || explicitNoneLimiter.Allow(failedNone) {
		t.Fatal("failed explicit none did not use the bounded detect stage bucket")
	}
	if !explicitNoneLimiter.Allow(Observation{
		Component: ComponentTrigger, Stage: StageTriggerCompleted, Result: ResultFailed, ReasonCode: ReasonNone,
	}) {
		t.Fatal("failed explicit none in detect suppressed the independent trigger stage")
	}
	bucketCount := len(limiter.buckets)
	limiter.Allow(Observation{
		Component: ComponentResource, Stage: StageResourceHard, Result: ResultFailed, ReasonCode: "UNKNOWN_REASON",
	})
	if len(limiter.buckets) != bucketCount {
		t.Fatal("unknown reason created a dynamic limiter bucket")
	}
}
