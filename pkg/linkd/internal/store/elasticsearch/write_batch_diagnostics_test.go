// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"
)

type diagnosticObserver struct {
	mu        sync.Mutex
	phases    map[string]int
	triggers  map[string]int
	durations map[string]time.Duration
}

func (o *diagnosticObserver) BatchFinished(context.Context, string, int, int, time.Duration, time.Duration, int) {
}

func (o *diagnosticObserver) BatchPhase(_ context.Context, kind, phase string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.phases[kind+"/"+phase]++
	if o.durations == nil {
		o.durations = map[string]time.Duration{}
	}
	o.durations[kind+"/"+phase] += d
}

func TestBatchServerTiming(t *testing.T) {
	for _, tc := range []struct {
		name, took    string
		status        int
		read, present bool
		want          time.Duration
	}{
		{name: "success", took: `7`, status: 201, present: true, want: 7 * time.Millisecond},
		{name: "zero", took: `0`, status: 201, present: true},
		{name: "partial failure still timed", took: `9`, status: 429, present: true, want: 9 * time.Millisecond},
		{name: "missing", status: 201},
		{name: "null", took: `null`, status: 201},
		{name: "negative", took: `-1`, status: 201},
		{name: "invalid type", took: `"bad"`, status: 201},
		{name: "overflow", took: `9223372036854775807`, status: 201},
		{name: "read does not provide bulk timing", took: `7`, status: 200, read: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &diagnosticObserver{phases: map[string]int{}, triggers: map[string]int{}}
			r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{"items":[{"create":{"status":%d}}]`, tc.status)
				if tc.read {
					body = `{"docs":[{"found":true}]`
				}
				if tc.took != "" {
					body += `,"took":` + tc.took
				}
				return batchHTTPResponse(200, []byte(body+`}`)), nil
			}), mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			_, b, err := r.EnableWriteBatch(WriteBatchConfig{MaxOperations: 1, MaxBytes: 1 << 20, MaxConcurrentBatches: 1, MaxCalls: 1, Timeout: time.Second}, o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(b.Close)
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
			kind := "write/"
			if tc.read {
				kind = "read/"
				req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, "/events/_doc/a?realtime=true", nil)
			}
			resp, err := b.Perform(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			b.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			want := 0
			if tc.present {
				want = 1
			}
			for _, phase := range []string{"server_took", "paired_execution"} {
				if o.phases[kind+phase] != want {
					t.Fatalf("phase %s: %v", phase, o.phases)
				}
			}
			if o.durations[kind+"server_took"] != tc.want {
				t.Fatalf("took %v, want %v", o.durations, tc.want)
			}
			if o.phases[kind+"response_items"] != 1 {
				t.Fatal(o.phases)
			}
		})
	}
}

func (o *diagnosticObserver) BatchTriggered(_ context.Context, kind, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.triggers[kind+"/"+reason]++
}

func TestBatchDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		reason string
		limit  int
		fail   bool
	}{
		{"operations", 1, false}, {"deadline", 32, false}, {"operations", 1, true},
		{"bytes", 32, false}, {"ready", 32, false},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			o := &diagnosticObserver{phases: map[string]int{}, triggers: map[string]int{}}
			r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				tr := httptrace.ContextClientTrace(req.Context())
				tr.GetConn("test")
				if tc.fail {
					return nil, context.DeadlineExceeded
				}
				tr.GotConn(httptrace.GotConnInfo{})
				tr.WroteRequest(httptrace.WroteRequestInfo{})
				tr.GotFirstResponseByte()
				if req.URL.Path == "/_mget" {
					return batchHTTPResponse(200, []byte(`{"docs":[{"found":true}]}`)), nil
				}
				return batchHTTPResponse(200, []byte(`{"items":[{"create":{"status":201}}]}`)), nil
			}), mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			_, b, err := r.EnableWriteBatch(WriteBatchConfig{MaxOperations: tc.limit, MaxBytes: 1 << 20, Wait: time.Millisecond, MaxConcurrentBatches: 1, MaxCalls: 1, Timeout: time.Second}, o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(b.Close)
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
			kind := "write/"
			switch tc.reason {
			case "ready":
				kind = "read/"
				req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, "/events/_doc/a?realtime=true", nil)
			case "bytes":
				req, _ = http.NewRequestWithContext(t.Context(), http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`"`+strings.Repeat("x", 1<<20)+`"`))
			}
			response, err := b.Perform(req)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v", err)
			}
			if response != nil {
				_ = response.Body.Close()
			}
			b.Close()
			if o.triggers[kind+tc.reason] != 1 {
				t.Fatal(o.triggers)
			}
			for _, phase := range []string{"admission", "collect", "worker_slot", "operation_queue", "encode"} {
				if o.phases[kind+phase] != 1 {
					t.Fatalf("missing %s: %v", phase, o.phases)
				}
			}
			for _, phase := range []string{"connection", "request_write", "first_byte", "response_body", "response_decode"} {
				want := 1
				if tc.fail {
					want = 0
				}
				if o.phases[kind+phase] != want {
					t.Fatalf("phase %s: %v", phase, o.phases)
				}
			}
		})
	}
}
