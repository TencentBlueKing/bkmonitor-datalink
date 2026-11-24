// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func TestBKMonitorContentRouteRendersRepeatedTrigger(t *testing.T) {
	ctx := t.Context()
	calls := 0
	resolver := contentFactsFunc(func(_ context.Context, event domain.Event, evaluation domain.EventEvaluation, candidate domain.Alert) (description.Facts, error) {
		calls++
		if candidate.TriggerEventID != event.EventID || candidate.EventSourceVersion != event.EventSourceVersion || candidate.Severity != evaluation.Severity {
			t.Fatal("content did not use the current trigger identity")
		}
		raw, _ := json.Marshal(event.Values["value"])
		value, err := description.ParseNumber(raw)
		if err != nil {
			return description.Facts{}, err
		}
		return description.Facts{ItemName: "CPU", Unit: "percent", Value: value, Connector: "and", Algorithms: []description.Algorithm{{Type: "Threshold", Groups: [][]description.Condition{{{Method: "gt", Threshold: 5}}}}}}, nil
	})
	source := config.EventSource{EventSourceID: "bk", Version: 7, Enrich: config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}}
	router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(resolver))
	if err != nil {
		t.Fatal(err)
	}
	repo := memory.New()
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, discardLogger{}, lifecycle.WithAlertContentBuilder(router))
	if err != nil {
		t.Fatal(err)
	}
	opening := storetest.Event("tenant-a", "opening", "fingerprint", "warning")
	opening.EventSourceID, opening.EventSourceVersion = "bk", 6
	opening.Content, opening.Values = "original source", domain.EventValues{"value": 10}
	stored, err := repo.CreateEvent(ctx, opening)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(ctx, stored.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.GetAlert(ctx, opening.BKTenantID, result.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	repeated := opening.Clone()
	repeated.EventID, repeated.EventSourceVersion = "repeated", 7
	repeated.Content, repeated.Values = "value=20 from alarmd", domain.EventValues{"value": 20}
	repeated.OccurredAt = repeated.OccurredAt.Add(time.Minute)
	stored, err = repo.CreateEvent(ctx, repeated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessEvent(ctx, stored.StoredEvent); err != nil {
		t.Fatal(err)
	}
	after, err := repo.GetAlert(ctx, opening.BKTenantID, before.Alert.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Alert.Content != "CPU > 5.0, 当前值20%" {
		t.Fatalf("repeated content=%q; want rendered latest value", after.Alert.Content)
	}
	if after.Alert.TriggerEventID != opening.EventID || after.Alert.EventSourceVersion != opening.EventSourceVersion || !after.Alert.BeginAt.Equal(before.Alert.BeginAt) || !reflect.DeepEqual(after.Alert.Enrich, before.Alert.Enrich) {
		t.Fatal("rendering changed the opening facts")
	}
	if after.Alert.Revision != before.Alert.Revision+1 || after.Alert.LatestEventID != repeated.EventID || !after.Alert.LastOccurredAt.Equal(repeated.OccurredAt) {
		t.Fatal("content and lifecycle did not advance together")
	}
	kept, err := repo.GetEvent(ctx, repeated.BKTenantID, repeated.EventID)
	if err != nil || kept.Event.Content != repeated.Content {
		t.Fatal("rendering changed the source Event")
	}
	if _, err := processor.ProcessEvent(ctx, kept); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("content resolver calls=%d; want one per new trigger and none on completed replay", calls)
	}
}

func TestTriggerContentModesAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, mode, source, want string
		failure, cancel, upgrade bool
	}{
		{name: "source text", mode: config.ContentModeSource, source: "latest source", want: "latest source"},
		{name: "source empty", mode: config.ContentModeSource, want: ""},
		{name: "description failure", mode: config.ContentModeBKMonitorDescription, failure: true, source: "latest source", want: "latest source"},
		{name: "description failure without source", mode: config.ContentModeBKMonitorDescription, failure: true, want: enrich.DefaultAlertContent},
		{name: "cancelled render", mode: config.ContentModeBKMonitorDescription, cancel: true},
		{name: "severity upgrade", mode: config.ContentModeBKMonitorDescription, upgrade: true, source: "latest source", want: "CPU > 5.0, 当前值20%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			resolver := contentFactsFunc(func(_ context.Context, event domain.Event, _ domain.EventEvaluation, _ domain.Alert) (description.Facts, error) {
				if tc.mode == config.ContentModeSource {
					t.Fatal("source mode queried description facts")
				}
				if event.EventID == "repeated" {
					if tc.cancel {
						cancel()
						return description.Facts{}, ctx.Err()
					}
					if tc.failure {
						return description.Facts{}, &description.Error{Code: "strategy_revision_missing"}
					}
				}
				value, _ := description.ParseNumber(json.RawMessage(`20`))
				return description.Facts{ItemName: "CPU", Unit: "percent", Value: value, Connector: "and", Algorithms: []description.Algorithm{{Type: "Threshold", Groups: [][]description.Condition{{{Method: "gt", Threshold: 5}}}}}}, nil
			})
			source := config.EventSource{EventSourceID: "bk", Version: 7, Enrich: config.EnrichConfig{ContentMode: tc.mode}}
			router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(resolver))
			if err != nil {
				t.Fatal(err)
			}
			repo := memory.New()
			processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, discardLogger{}, lifecycle.WithAlertContentBuilder(router), lifecycle.WithSeverityUpgradePolicy("update_current"))
			if err != nil {
				t.Fatal(err)
			}
			event := storetest.Event("tenant-a", "opening", "fingerprint", "warning")
			event.EventSourceID, event.EventSourceVersion, event.Content = "bk", 7, "opening source"
			stored, err := repo.CreateEvent(ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.ProcessEvent(ctx, stored.StoredEvent)
			if err != nil {
				t.Fatal(err)
			}
			before, err := repo.GetAlert(ctx, event.BKTenantID, result.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			event.EventID, event.Content = "repeated", tc.source
			if tc.upgrade {
				event.Evaluations[0].Severity = "critical"
			}
			stored, err = repo.CreateEvent(ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			_, err = processor.ProcessEvent(ctx, stored.StoredEvent)
			if tc.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("render cancellation error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			after, err := repo.GetAlert(t.Context(), event.BKTenantID, before.Alert.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				if !reflect.DeepEqual(after.Alert, before.Alert) {
					t.Fatal("cancelled rendering changed the persisted Alert")
				}
			} else if after.Alert.Content != tc.want {
				t.Fatalf("content=%q want=%q", after.Alert.Content, tc.want)
			}
		})
	}
}
