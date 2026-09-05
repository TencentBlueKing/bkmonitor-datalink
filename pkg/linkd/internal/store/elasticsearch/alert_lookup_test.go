// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available under the MIT License.

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
	"errors"
	"net/http"
	"testing"

	"linkd/internal/store"
)

type alertLookupRouter struct {
	*StaticRouter
	activeTargets   []string
	terminalTargets []string
}

func (r *alertLookupRouter) ActiveAlertTargets(context.Context) ([]string, error) {
	return append([]string(nil), r.activeTargets...), nil
}

func (r *alertLookupRouter) TerminalAlertTargets(context.Context) ([]string, error) {
	return append([]string(nil), r.terminalTargets...), nil
}

func TestAlertLookupsUseMatchingPhysicalTargets(t *testing.T) {
	t.Parallel()
	staticRouter, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	router := &alertLookupRouter{
		StaticRouter: staticRouter, activeTargets: []string{"active-only"}, terminalTargets: []string{"terminal-history"},
	}
	wantPaths := []string{"/active-only/_search", "/terminal-history/_search"}
	requests := 0
	repository, err := New(transportFunc(func(request *http.Request) (*http.Response, error) {
		if requests >= len(wantPaths) {
			t.Fatalf("unexpected lookup request %s", request.URL.Path)
		}
		if request.URL.Path != wantPaths[requests] {
			t.Fatalf("lookup request path=%q, want %q", request.URL.Path, wantPaths[requests])
		}
		requests++
		return jsonResponse(t, map[string]any{"hits": map[string]any{"hits": []any{}}}), nil
	}), router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActiveAlert(t.Context(), store.ActiveAlertKey{
		BKTenantID: "tenant-1", EventSourceID: "source-1", Fingerprint: "fingerprint-1",
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("FindActiveAlert() error=%v", err)
	}
	if _, err := repository.FindAlertEndedByEvent(t.Context(), "tenant-1", "event-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("FindAlertEndedByEvent() error=%v", err)
	}
	if requests != len(wantPaths) {
		t.Fatalf("lookup requests=%d, want %d", requests, len(wantPaths))
	}
}
