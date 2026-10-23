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

// A connection that fails on its first request is not sent again: the
// transport re-sends only on a reused connection, the stale one the server
// dropped while idle (stale_connection_test.go), and a fresh connection
// closed or refused is the dependency answering no. The page's own retry,
// which re-dialed this too, is gone.
func TestARangeQueryOnAFreshConnectionThatFailsIsNotSentAgain(t *testing.T) {
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
		t.Fatal("a connection closed on its first request was reported as success")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want one: a fresh connection that fails is not re-sent", got)
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
}
