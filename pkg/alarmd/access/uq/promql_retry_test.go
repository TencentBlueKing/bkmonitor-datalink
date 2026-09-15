// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func retryTestRequest() RangeRequest {
	return RangeRequest{
		PromQL: `max(bkmonitor_alarmd_fleet_objects{state="expected"})`,
		Start:  time.Unix(1789000000, 0), End: time.Unix(1789003600, 0),
		Step: time.Minute, SpaceUID: "bkcc__2",
	}
}

// A pooled keep-alive connection can be closed by the peer between being picked
// and being written to. Go retries that by itself only for requests it deems
// replayable, and a POST is not one, so without this the caller gets a bare EOF
// -- which is exactly what the page showed for one curve while the other three,
// reusing the connection this one had just established, were fine.
func TestRangeRetriesOnceWhenTheConnectionFailedBeforeAnyResponse(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			// Close without answering, which is what a connection the peer had
			// already decided to drop looks like from here.
			if hijacked, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = hijacked.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Range(context.Background(), retryTestRequest()); err != nil {
		t.Fatalf("a connection that failed before any response was not retried: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want the original and exactly one retry", got)
	}
	// One retry, not a loop: a peer that refuses every connection must surface
	// as a failure rather than as a client that keeps trying.
	if got := client.RangeRetries(); got != 1 {
		t.Fatalf("RangeRetries() = %d, want 1", got)
	}
}

// Retrying forever would turn an unreachable dependency into a hang. The second
// failure ends it, and the first error is the one reported because it describes
// the condition that started this.
func TestRangeGivesUpAfterOneRetry(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if hijacked, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = hijacked.Close()
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Range(context.Background(), retryTestRequest()); err == nil {
		t.Fatal("a peer that refused both attempts was reported as success")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want it to stop after one retry", got)
	}
}

// A caller that has given up is not waiting for a second try. Retrying a
// cancelled request spends the dependency's capacity on an answer nobody will
// read, and on a shutdown path it delays the shutdown.
func TestRangeDoesNotRetryWhenTheCallerHasGoneAway(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if hijacked, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = hijacked.Close()
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Range(ctx, retryTestRequest()); err == nil {
		t.Fatal("a cancelled range query reported success")
	}
	if got := client.RangeRetries(); got != 0 {
		t.Fatalf("RangeRetries() = %d, want no retry for a caller that had gone away", got)
	}
}

// A response that arrived is an answer, including an error status. Repeating
// the request would ask the dependency to do the same work twice to receive the
// same refusal.
func TestRangeDoesNotRetryOnceAResponseArrived(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Range(context.Background(), retryTestRequest()); err == nil {
		t.Fatal("a 500 was reported as success")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want the answered request not to be repeated", got)
	}
	if got := client.RangeRetries(); got != 0 {
		t.Fatalf("RangeRetries() = %d, want none once a response arrived", got)
	}
}
