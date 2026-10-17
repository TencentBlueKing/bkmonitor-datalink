// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package elasticsearchstore

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestRefreshActiveAlertsUsesOnlyActiveTargetsAndChecksShards(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      map[string]any
		wantError bool
	}{
		{"success", map[string]any{"_shards": map[string]any{"total": 2, "successful": 2, "failed": 0}}, false},
		{"unassigned replica", map[string]any{"_shards": map[string]any{"total": 2, "successful": 1, "failed": 0}}, false},
		{"failed shard", map[string]any{"_shards": map[string]any{"total": 2, "successful": 1, "failed": 1}}, true},
		{"missing result", map[string]any{}, true},
		{"empty result", map[string]any{"_shards": map[string]any{"total": 0, "successful": 0, "failed": 0}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			static, err := NewStaticRouter("refresh-test")
			if err != nil {
				t.Fatal(err)
			}
			router := &alertLookupRouter{StaticRouter: static, activeTargets: []string{"active-only"}, terminalTargets: []string{"history"}}
			calls := 0
			repo, err := New(transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/active-only/_refresh" {
					t.Errorf("unexpected refresh %s %s", r.Method, r.URL.Path)
				}
				return jsonResponse(t, test.body), nil
			}), router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.RefreshActiveAlerts(t.Context()); (err != nil) != test.wantError {
				t.Fatal("wrong refresh result", err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if err := repo.RefreshActiveAlerts(canceled); !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatal("canceled refresh reached transport", calls, err)
			}
		})
	}
}
