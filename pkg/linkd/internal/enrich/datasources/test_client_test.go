// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"linkd/internal/enrich"
	"testing/synctest"
)

func testRequest() enrich.TestRequest {
	return enrich.TestRequest{TenantID: "tenant", EventSourceID: "source", AlertID: "alert", Config: enrich.DefaultTestSourceConfig()}
}

func TestTestClientDelayFailureTimeoutAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := TestClient{}
		req := testRequest()
		req.Config.SleepMeanMilliseconds = 100
		start := time.Now()
		if err := client.Call(t.Context(), req); err != nil || time.Since(start) != 100*time.Millisecond {
			t.Fatalf("fixed delay=%v error=%v", time.Since(start), err)
		}
		req.Config.ErrorRate = 1
		start = time.Now()
		if err := client.Call(t.Context(), req); !errors.Is(err, enrich.ErrInjectedTestFailure) || time.Since(start) != 100*time.Millisecond {
			t.Fatalf("injected failure=%v", err)
		}
		req.Config.TimeoutMilliseconds = 10
		start = time.Now()
		if err := client.Call(t.Context(), req); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 10*time.Millisecond {
			t.Fatalf("call timeout=%v", err)
		}
		req.Config.TimeoutMilliseconds = 1000
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- client.Call(ctx, req) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("parent cancellation=%v", err)
		}
		if err := client.Call(ctx, req); !errors.Is(err, context.Canceled) {
			t.Fatalf("already canceled=%v", err)
		}
	})
}

func TestTestClientDistributionAndConcurrentSampling(t *testing.T) {
	req := testRequest()
	req.Config.SleepMeanMilliseconds = 100
	req.Config.SleepStddevMilliseconds = 10
	req.Config.SleepMaxMilliseconds = 200
	req.Config.ErrorRate = 0.2
	var sum, squares float64
	failures := 0
	const count = 20000
	for range count {
		delay, fail := testSample(req.Config)
		value := float64(delay) / float64(time.Millisecond)
		sum += value
		squares += value * value
		if fail {
			failures++
		}
	}
	mean := sum / count
	stddev := math.Sqrt(squares/count - mean*mean)
	if math.Abs(mean-100) > 0.5 || math.Abs(stddev-10) > 0.5 || math.Abs(float64(failures)/count-0.2) > 0.025 {
		t.Fatalf("mean=%v stddev=%v error rate=%v", mean, stddev, float64(failures)/count)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				d, _ := testSample(req.Config)
				if d < 0 || d > 200*time.Millisecond {
					t.Error("concurrent sampling exceeded configured bound")
				}
			}
		})
	}
	wg.Wait()
}

func TestTestClientClampsExtremeSamples(t *testing.T) {
	req := testRequest()
	req.Config.SleepMeanMilliseconds = 20
	req.Config.SleepStddevMilliseconds = 1000
	req.Config.SleepMaxMilliseconds = 40
	low, high := false, false
	for i := range 100 {
		req.AlertID = fmt.Sprint(i)
		d, _ := testSample(req.Config)
		if d < 0 || d > 40*time.Millisecond {
			t.Fatalf("unbounded delay=%v", d)
		}
		low = low || d == 0
		high = high || d == 40*time.Millisecond
	}
	if !low || !high {
		t.Fatal("extreme normal samples were not clipped")
	}
}

func TestTestClientRejectsInvalidRequests(t *testing.T) {
	for _, change := range []func(*enrich.TestRequest){func(r *enrich.TestRequest) { r.TenantID = "" }, func(r *enrich.TestRequest) { r.CallIndex = -1 }, func(r *enrich.TestRequest) { r.CallIndex = 1 }, func(r *enrich.TestRequest) { r.Config.Calls = 17 }, func(r *enrich.TestRequest) { r.Config.SleepStddevMilliseconds = math.Inf(1) }, func(r *enrich.TestRequest) { r.Config.ErrorRate = math.NaN() }} {
		req := testRequest()
		change(&req)
		if err := (TestClient{}).Call(t.Context(), req); err == nil {
			t.Fatal("invalid test request accepted")
		}
	}
}
