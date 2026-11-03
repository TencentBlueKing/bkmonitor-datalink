// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package blackbox

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The query service is a local wire fixture. Credentials are configured only
// on its server-side diagnostic client, never on the CLI or in a next_call.
type slotUQFixture struct {
	server   *httptest.Server
	client   *http.Client
	secret   string
	body     atomic.Value
	mu       sync.Mutex
	requests []slotUQReceipt
}

type slotUQReceipt struct {
	Path                     string         `json:"path"`
	Tenant                   string         `json:"tenant"`
	Space                    string         `json:"space"`
	QuerySource              string         `json:"query_source"`
	Body                     map[string]any `json:"body"`
	ReceivedAt               time.Time      `json:"received_at"`
	ConfiguredCredentialUsed bool           `json:"configured_credential_used"`
}

type slotUQTransport struct {
	base   http.RoundTripper
	secret string
}

func (transport slotUQTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Set("Authorization", "Bearer "+transport.secret)
	return transport.base.RoundTrip(request)
}

func newSlotUQFixture(t *testing.T, response string) *slotUQFixture {
	t.Helper()
	uq := &slotUQFixture{secret: "constructed-slot-uq-server-only-credential"}
	uq.body.Store(response)
	uq.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (r.URL.Path != "/query/ts" && r.URL.Path != "/query/ts/promql") {
			t.Error("unexpected UQ request method or path")
		}
		credentialUsed := r.Header.Get("Authorization") == "Bearer "+uq.secret
		if !credentialUsed {
			t.Error("UQ query did not use its server-side configured credential")
		}
		var body map[string]any
		decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Error("UQ request did not contain a bounded JSON object")
		}
		uq.mu.Lock()
		uq.requests = append(uq.requests, slotUQReceipt{Path: r.URL.Path, Tenant: r.Header.Get("X-Bk-Tenant-Id"), Space: r.Header.Get("X-Bk-Scope-Space-Uid"), QuerySource: r.Header.Get("Bk-Query-Source"), Body: body, ReceivedAt: time.Now().UTC(), ConfiguredCredentialUsed: credentialUsed})
		uq.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, uq.body.Load().(string))
	}))
	t.Cleanup(uq.server.Close)
	client := *uq.server.Client()
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = slotUQTransport{base: transport, secret: uq.secret}
	uq.client = &client
	return uq
}

func (uq *slotUQFixture) receipts() []slotUQReceipt {
	uq.mu.Lock()
	defer uq.mu.Unlock()
	return append([]slotUQReceipt(nil), uq.requests...)
}

func slotNextCall(t *testing.T, response map[string]any, operation string) map[string]any {
	t.Helper()
	calls, ok := response["next_call"].([]any)
	if !ok {
		t.Fatal("response did not supply structured next_call suggestions")
	}
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok || call["operation"] != operation {
			continue
		}
		if mode, _ := call["mode"].(string); mode != "" && mode != "invoke" {
			t.Fatal("expected an executable invoke suggestion")
		}
		params, ok := call["params"].(map[string]any)
		if !ok {
			t.Fatal("next_call omitted its structured input")
		}
		copy := make(map[string]any, len(params))
		for key, value := range params {
			copy[key] = value
		}
		return copy
	}
	t.Fatalf("response did not provide a %s next_call", operation)
	return nil
}

func invokeSlotNext(t *testing.T, h *harness, name string, want int, response map[string]any, operation string) map[string]any {
	t.Helper()
	params := slotNextCall(t, response, operation)
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal("cannot encode server-provided next_call")
	}
	for _, secret := range h.secrets {
		if secret != "" && strings.Contains(string(raw), secret) {
			t.Fatal("server next_call exposed a credential")
		}
	}
	return h.run(name, want, "", "invoke", operation, "--env", environment, "--input", string(raw))
}
