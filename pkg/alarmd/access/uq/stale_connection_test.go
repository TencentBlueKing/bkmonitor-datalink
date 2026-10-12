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
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// staleConnectionServer answers the first request on each connection and
// closes the connection, unanswered, on the second: a pooled keep-alive
// connection the server dropped while it sat idle, met by the next request
// that picked it. That is the server unify-query is - no IdleTimeout, so Go
// closes idle connections at its 3s ReadTimeout - against a client that
// keeps them for 90s.
func staleConnectionServer(t *testing.T, body string) (*httptest.Server, func() (answered, dropped int)) {
	t.Helper()
	var mutex sync.Mutex
	perConnection := map[string]int{}
	answered, dropped := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		perConnection[request.RemoteAddr]++
		second := perConnection[request.RemoteAddr] == 2
		if second {
			dropped++
		} else {
			answered++
		}
		mutex.Unlock()
		if second {
			if hijacked, _, err := writer.(http.Hijacker).Hijack(); err == nil {
				_ = hijacked.Close()
			}
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, func() (int, int) {
		mutex.Lock()
		defer mutex.Unlock()
		return answered, dropped
	}
}

// An evaluation query sent on a pooled connection the server had already
// dropped is answered, not lost. The transport sends it again on a new
// connection, once, because the request is marked replayable; it does so only
// when the reused connection failed before any response began, which is the
// only case where nothing can have been read by anyone. Before, the query
// completed UNAVAILABLE, a gap in its Slot's evaluation that nothing fills
// afterwards: the failure was transport, which the degraded pool does not
// count, and the late-data lookback re-reads only complete first reads.
func TestAnEvaluationQueryOnAConnectionTheServerDroppedIsAnswered(t *testing.T) {
	server, counts := staleConnectionServer(t, `{"series":[],"is_partial":false}`)
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if completion.Completeness != execution.CompletenessFull {
			t.Fatalf("round %d completed %s (%+v), want FULL: a query on a connection the server dropped "+
				"while idle is a gap nothing fills", round, completion.Completeness, completion.RouteFacts)
		}
	}
	// The second round really did reuse the connection and meet the drop, and
	// was answered on a new one: two answers, one drop.
	if answered, dropped := counts(); answered != 2 || dropped != 1 {
		t.Fatalf("answered %d, dropped %d; want 2 and 1 - the fixture did not reproduce a reused stale connection", answered, dropped)
	}
}

// The page's range query is answered the same way, by the same mark, and
// needs no retry of its own.
func TestARangeQueryOnAConnectionTheServerDroppedIsAnswered(t *testing.T) {
	server, counts := staleConnectionServer(t, `{"series":[]}`)
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		if _, err := client.Range(context.Background(), retryTestRequest()); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	if answered, dropped := counts(); answered != 2 || dropped != 1 {
		t.Fatalf("answered %d, dropped %d; want 2 and 1", answered, dropped)
	}
}
