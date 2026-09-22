// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package preview

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/assembly"
	"linkd/internal/eventsource"
)

type sourceStub struct{ source config.EventSource }

func (s sourceStub) Get(context.Context, string) (eventsource.Record, error) {
	return eventsource.Record{ID: s.source.EventSourceID, Published: 7}, nil
}

func (s sourceStub) GetRelease(context.Context, string, int64) (eventsource.Release, error) {
	return eventsource.Release{ID: s.source.EventSourceID, Version: 7, Spec: s.source}, nil
}

func previewConfig() config.EnrichConfig {
	var c config.EnrichConfig
	_ = json.Unmarshal([]byte(`{"processors":[{"type":"fields","config":{"rules":[{"id":"x","operations":[{"id":"set","type":"assign","assignments":[{"target":"$.labels.strategy_id","value":{"literal":9001}},{"target":"$.title","value":{"template":"${title} enriched","variables":{"title":{"jsonpath":"$.alert.title"}}}}]}]}]}}]}`), &c)
	return c
}

func TestPreviewJSONAndIDDoNotPersistOrReplayOldPatches(t *testing.T) {
	source := sourceStub{config.EventSource{EventSourceID: "host", RelatedTenantID: "tenant-a", Enrich: previewConfig()}}
	reads, opens, closes := 0, 0, 0
	stored := domain.Alert{AlertID: "a", BKTenantID: "tenant-a", EventSourceID: "host", Title: "raw", Labels: domain.DimensionMap{}}
	stored.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.title","value":"old"}]}}]`)}
	service := New(source, func(_ context.Context, tenant, id string) (domain.Alert, error) {
		reads++
		if tenant != "tenant-a" || id != "a" {
			t.Fatal("reader scope")
		}
		return stored, nil
	}, func(_ context.Context, s config.EventSource) (Enricher, func() error, error) {
		opens++
		r, err := assembly.NewRouter([]config.EventSource{s}, enrich.Sources{})
		return r, func() error { closes++; return nil }, err
	})
	for _, input := range []Input{{Alert: json.RawMessage(`{"title":"raw"}`)}, {AlertID: "a"}} {
		r, err := service.Preview(t.Context(), Request{BKTenantID: "tenant-a", EventSourceID: "host", Input: input})
		if err != nil {
			t.Fatal(err)
		}
		if r.EffectiveAlert["title"] != "raw enriched" || r.Original["title"] != "raw" || r.Version != 7 || len(r.Trace) != 1 || len(r.Changes) == 0 {
			t.Fatalf("response=%+v", r)
		}
		encoded, _ := json.Marshal(r.Enrich)
		var object map[string]any
		if err := json.Unmarshal(encoded, &object); err != nil {
			t.Fatal(err)
		}
		if stored.Title != "raw" {
			t.Fatal("stored alert mutated")
		}
	}
	if reads != 1 || opens != 2 || closes != 2 {
		t.Fatalf("reads=%d opens=%d closes=%d", reads, opens, closes)
	}
	for _, req := range []Request{{BKTenantID: "tenant-b", EventSourceID: "host", Input: Input{AlertID: "a"}}, {BKTenantID: "tenant-a", EventSourceID: "host", Input: Input{AlertID: "a", Alert: json.RawMessage(`{}`)}}, {BKTenantID: "tenant-a", EventSourceID: "host", Input: Input{Alert: json.RawMessage(`{"bk_tenant_id":"tenant-b"}`)}}} {
		if _, err := service.Preview(t.Context(), req); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if opens != 2 {
		t.Fatal("invalid request reached runtime")
	}
}

func TestPreviewBudgetAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	service := New(sourceStub{config.EventSource{EventSourceID: "host"}}, nil, func(ctx context.Context, _ config.EventSource) (Enricher, func() error, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil, ctx.Err()
	})
	req := Request{BKTenantID: "tenant-a", EventSourceID: "host", Input: Input{Alert: json.RawMessage(`{}`)}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _, _ = service.Preview(ctx, req) })
	}
	for range 4 {
		<-entered
	}
	_, err := service.Preview(t.Context(), req)
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != 429 {
		t.Fatalf("budget=%v", err)
	}
	cancel()
	wg.Wait()
}
