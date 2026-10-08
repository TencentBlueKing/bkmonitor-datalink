// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
)

type releaseTestEngine string

func (e releaseTestEngine) Enrich(ctx context.Context, _ enrich.Input) (enrich.Result, error) {
	return enrich.Result{ConfigDigest: string(e)}, ctx.Err()
}

func TestReleaseEnricherUsesEventVersionAndClosesHistory(t *testing.T) {
	reads, opens, closes := 0, 0, 0
	r := &releaseEnricher{current: config.EventSource{EventSourceID: "host", Version: 2, RelatedTenantID: "t"}, engine: releaseTestEngine("current"), slots: make(chan struct{}, 1),
		read: func(_ context.Context, id string, version int64) (config.EventSource, error) {
			reads++
			return config.EventSource{EventSourceID: id, Version: version, RelatedTenantID: "t"}, nil
		},
		open: func(_ context.Context, source config.EventSource) (lifecycle.EventEnricher, func() error, error) {
			opens++
			if source.Version != 1 {
				t.Fatal("wrong historical version")
			}
			return releaseTestEngine("historical"), func() error { closes++; return nil }, nil
		},
	}
	for _, version := range []int64{2, 1} {
		result, err := r.Enrich(t.Context(), enrich.Input{Event: domain.Event{BKTenantID: "t", EventSourceID: "host", EventSourceVersion: version}})
		if err != nil {
			t.Fatal(err)
		}
		want := "current"
		if version == 1 {
			want = "historical"
		}
		if result.ConfigDigest != want {
			t.Fatal("silently used current config")
		}
	}
	if reads != 1 || opens != 1 || closes != 1 {
		t.Fatalf("reads=%d opens=%d closes=%d", reads, opens, closes)
	}
	for _, event := range []domain.Event{{BKTenantID: "other", EventSourceID: "host", EventSourceVersion: 1}, {BKTenantID: "t", EventSourceID: "other", EventSourceVersion: 1}, {BKTenantID: "t", EventSourceID: "host", EventSourceVersion: 0}} {
		if _, err := r.Enrich(t.Context(), enrich.Input{Event: event}); err == nil {
			t.Fatal("source/tenant/version mismatch accepted")
		}
	}
	if opens != 1 {
		t.Fatal("invalid scope opened dependency")
	}
	r.slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Enrich(ctx, enrich.Input{Event: domain.Event{BKTenantID: "t", EventSourceID: "host", EventSourceVersion: 1}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked history cancellation: %v", err)
	}
	<-r.slots
}

func TestReleaseEnricherPropagatesFailureWithoutFallback(t *testing.T) {
	r := &releaseEnricher{current: config.EventSource{EventSourceID: "host", Version: 2}, engine: releaseTestEngine("current"), slots: make(chan struct{}, 1)}
	event := enrich.Input{Event: domain.Event{BKTenantID: "t", EventSourceID: "host", EventSourceVersion: 1}}
	r.read = func(context.Context, string, int64) (config.EventSource, error) {
		return config.EventSource{}, errors.New("missing release")
	}
	if _, err := r.Enrich(t.Context(), event); err == nil {
		t.Fatal("historical read failed but current config used")
	}
	r.read = func(context.Context, string, int64) (config.EventSource, error) {
		return config.EventSource{EventSourceID: "host", Version: 1}, nil
	}
	r.open = func(context.Context, config.EventSource) (lifecycle.EventEnricher, func() error, error) {
		return nil, nil, errors.New("open failed")
	}
	if _, err := r.Enrich(t.Context(), event); err == nil {
		t.Fatal("open failure hidden")
	}
	closed := false
	r.open = func(context.Context, config.EventSource) (lifecycle.EventEnricher, func() error, error) {
		return releaseTestEngine("historical"), func() error { closed = true; return errors.New("close failed") }, nil
	}
	result, err := r.Enrich(t.Context(), event)
	if err == nil || !closed || result.ConfigDigest != "historical" {
		t.Fatal("cleanup failure or original config identity lost")
	}
}

func (e releaseTestEngine) BuildContent(ctx context.Context, _ domain.Event, _ domain.EventEvaluation, _ domain.Alert) (string, error) {
	return string(e), ctx.Err()
}

func TestReleaseContentUsesHistoricalVersionAndTenant(t *testing.T) {
	reads, opens, closes := 0, 0, 0
	r := &releaseEnricher{current: config.EventSource{EventSourceID: "host", Version: 2, RelatedTenantID: "new-tenant"}, engine: releaseTestEngine("current"), slots: make(chan struct{}, 1), read: func(_ context.Context, id string, version int64) (config.EventSource, error) {
		reads++
		return config.EventSource{EventSourceID: id, Version: version, RelatedTenantID: "old-tenant"}, nil
	}, open: func(_ context.Context, source config.EventSource) (lifecycle.EventEnricher, func() error, error) {
		opens++
		if source.Version != 1 {
			t.Fatal("historical content used latest release")
		}
		return releaseTestEngine("historical"), func() error { closes++; return nil }, nil
	}}
	for _, tc := range []struct {
		tenant  string
		version int64
		want    string
	}{{"new-tenant", 2, "current"}, {"old-tenant", 1, "historical"}} {
		content, err := r.BuildContent(t.Context(), domain.Event{BKTenantID: tc.tenant, EventSourceID: "host", EventSourceVersion: tc.version}, domain.EventEvaluation{}, domain.Alert{})
		if err != nil || content != tc.want {
			t.Fatalf("content=%q error=%v", content, err)
		}
	}
	if reads != 1 || opens != 1 || closes != 1 || len(r.slots) != 0 {
		t.Fatal("historical content resource leak")
	}
	r.slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.BuildContent(ctx, domain.Event{BKTenantID: "old-tenant", EventSourceID: "host", EventSourceVersion: 1}, domain.EventEvaluation{}, domain.Alert{}); !errors.Is(err, context.Canceled) {
		t.Fatal("content cancellation ignored", err)
	}
	<-r.slots
	r.current.Enrich.ContentMode = config.ContentModeBKMonitorDescription
	if _, err := r.BuildContent(t.Context(), domain.Event{BKTenantID: "foreign", EventSourceID: "host", EventSourceVersion: 2}, domain.EventEvaluation{}, domain.Alert{}); err == nil {
		t.Fatal("content tenant mismatch allowed")
	}
}
