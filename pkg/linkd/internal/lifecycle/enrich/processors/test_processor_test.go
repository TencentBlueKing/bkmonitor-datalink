// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/datasources"
)

func TestTestProcessorConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  map[string]any
		invalid bool
	}{
		{name: "defaults"},
		{name: "fixed sleep", config: map[string]any{"datasource": map[string]any{"sleep_mean_milliseconds": 10}}},
		{name: "random sleep", config: map[string]any{"datasource": map[string]any{"sleep_mean_milliseconds": 10.5, "sleep_stddev_milliseconds": 2.5, "error_rate": 0.2}}},
		{name: "no legacy setting", config: map[string]any{"sleep_milliseconds": 10}, invalid: true},
		{name: "unknown datasource setting", config: map[string]any{"datasource": map[string]any{"other": 1}}, invalid: true},
		{name: "negative mean", config: map[string]any{"datasource": map[string]any{"sleep_mean_milliseconds": -1}}, invalid: true},
		{name: "negative stddev", config: map[string]any{"datasource": map[string]any{"sleep_stddev_milliseconds": -1}}, invalid: true},
		{name: "mean above cap", config: map[string]any{"datasource": map[string]any{"sleep_mean_milliseconds": 20, "sleep_max_milliseconds": 10}}, invalid: true},
		{name: "excessive calls", config: map[string]any{"datasource": map[string]any{"calls": 17}}, invalid: true},
		{name: "fractional calls", config: map[string]any{"datasource": map[string]any{"calls": 1.5}}, invalid: true},
		{name: "zero calls", config: map[string]any{"datasource": map[string]any{"calls": 0}}, invalid: true},
		{name: "error rate overflow", config: map[string]any{"datasource": map[string]any{"error_rate": 1.1}}, invalid: true},
		{name: "zero timeout", config: map[string]any{"datasource": map[string]any{"timeout_milliseconds": 0}}, invalid: true},
		{name: "invalid JSON", config: map[string]any{"fields": map[string]any{"value": math.NaN()}}, invalid: true},
		{name: "non-object fields", config: map[string]any{"fields": []any{1}}, invalid: true},
		{name: "large payload", config: map[string]any{"fields": map[string]any{"value": strings.Repeat("a", 64<<10)}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTest(tc.config)
			if (err != nil) != tc.invalid {
				t.Fatalf("invalid=%v error=%v", tc.invalid, err)
			}
		})
	}
}

func TestTestProcessorClonesConfiguredFieldsConcurrently(t *testing.T) {
	fields := map[string]any{"nested": map[string]any{"enabled": true}, "count": 3, "tags": []any{"one", 2}}
	settings := map[string]any{"calls": 2}
	p, err := NewTest(map[string]any{"fields": fields, "datasource": settings})
	if err != nil {
		t.Fatal(err)
	}
	fields["nested"].(map[string]any)["enabled"] = false
	settings["error_rate"] = 1
	scope := testProcessorScope(t, datasources.TestClient{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			result, processErr := p.Process(t.Context(), scope)
			if processErr != nil || result.Status != domain.EnrichStatusSucceeded || string(result.Value["nested"]) != `{"enabled":true}` {
				t.Errorf("result=%#v error=%v", result, processErr)
				return
			}
			result.Value["nested"][0] = 'x'
			result.Value["count"] = json.RawMessage(`999`)
		})
	}
	wg.Wait()
	result, err := p.Process(t.Context(), scope)
	if err != nil || string(result.Value["count"]) != "3" {
		t.Fatalf("shared result: %#v %v", result, err)
	}
}

func TestTestProcessorCallsAreScopedAndFailFast(t *testing.T) {
	p, err := NewTest(map[string]any{"datasource": map[string]any{"calls": 3}, "fields": map[string]any{"ok": true}})
	if err != nil {
		t.Fatal(err)
	}
	requests := []enrich.TestRequest{}
	scope := testProcessorScope(t, testSourceFunc(func(_ context.Context, r enrich.TestRequest) error {
		requests = append(requests, r)
		if r.CallIndex == 1 {
			return enrich.ErrInjectedTestFailure
		}
		return nil
	}))
	result, err := p.Process(t.Context(), scope)
	if !errors.Is(err, enrich.ErrInjectedTestFailure) || len(requests) != 2 || result.Value != nil {
		t.Fatalf("result=%#v error=%v calls=%d", result, err, len(requests))
	}
	alert := scope.Alert()
	for i, r := range requests {
		if r.TenantID != alert.BKTenantID || r.EventSourceID != alert.EventSourceID || r.AlertID != alert.AlertID || r.CallIndex != i {
			t.Fatalf("request scope=%#v", r)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Process(ctx, scope); !errors.Is(err, context.Canceled) || len(requests) != 2 {
		t.Fatalf("canceled call=%v", err)
	}
	if matched, err := p.Match(ctx, scope); matched || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled match=%v %v", matched, err)
	}
	if _, err := p.Process(t.Context(), nil); err == nil {
		t.Fatal("nil scope accepted")
	}
}

type testSourceFunc func(context.Context, enrich.TestRequest) error

func (f testSourceFunc) Call(ctx context.Context, r enrich.TestRequest) error { return f(ctx, r) }

func testProcessorScope(t *testing.T, source enrich.TestSource) *enrich.Scope {
	t.Helper()
	scope, err := enrich.NewScope(strategyTestAlert(t, 2), enrich.Sources{Test: source})
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
