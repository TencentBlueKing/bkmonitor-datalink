// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/assembly"
	"linkd/internal/enrich/view"
	"linkd/internal/eventgen"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
)

// 使用实际生成器、Cleaner、Lifecycle 和 fields Enrich；存储为内存，不代表外部 CMDB 集成。
func TestEventgenHostLifecycleAndEnrichment(t *testing.T) {
	raw, err := os.ReadFile("../../configs/eventgen/hosts.json")
	if err != nil {
		t.Fatal(err)
	}
	var c eventgen.Config
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	scenario := &c.Scenarios[0]
	scenario.Arrival = eventgen.Arrival{PerSecond: 1, Count: 1}
	for i := range scenario.Stages {
		if !scenario.Stages[i].Terminal {
			scenario.Stages[i].Duration = eventgen.Span{Fixed: "1s"}
			scenario.Stages[i].Every = eventgen.Span{}
		}
	}
	scenario.Stages[2].Next = []eventgen.Branch{{Stage: "recover", Weight: 1}}
	records, err := eventgen.Preview(c, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("expected warning, critical, lower, recovered; got %d", len(records))
	}
	var source config.EventSource
	if err = json.Unmarshal([]byte(`{"event_source_id":"eventgen-hosts","enabled":true,"cleaner":{"type":"standard"},"enrich":{"processors":[{"type":"fields","config":{"rules":[{"id":"host","operations":[{"id":"identity","type":"assign","assignments":[{"target":"$.labels.test_host","value":{"jsonpath":"$.event.subject_id"}}]}]}]}}]}}`), &source); err != nil {
		t.Fatal(err)
	}
	source.Storage = config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"localhost:9092"}, Topic: "eventgen-test", ConsumerGroup: "eventgen-test"}}
	mapper, err := cleaner.NewMapper(source, config.DefaultSeverityConfig())
	if err != nil {
		t.Fatal(err)
	}
	router, err := assembly.NewRouter([]config.EventSource{source}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	repo := memory.New()
	hook := &eventgenHook{}
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, []lifecycle.NamedFinalHook{{Name: "output", Hook: hook}}, eventgenSeverity{}, lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, record := range records {
		event, err := mapper.MapMessage(t.Context(), consume.Message{ID: record.SourceEventID, TenantID: c.TenantID, Body: record.Body, EnqueuedAt: record.Timestamp})
		if err != nil {
			t.Fatal(err)
		}
		created, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		result, err := processor.ProcessEvent(t.Context(), created.StoredEvent)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Event.EnrichStatus != domain.EnrichStatusSucceeded {
			t.Fatalf("enrich failed: %+v", stored.Event)
		}
		effective, err := view.EnrichedEvent(stored.Event, event.Evaluations[0].Severity)
		if err != nil {
			t.Fatal(err)
		}
		labels, err := json.Marshal(effective.Labels)
		if err != nil || !strings.Contains(string(labels), `"test_host":"101"`) {
			t.Fatalf("dataset identity did not reach enrich: %s %v", labels, err)
		}
		if len(result.AlertIDs) == 0 {
			t.Fatal(result)
		}
		ids = append(ids, result.AlertIDs[len(result.AlertIDs)-1])
	}
	if ids[0] == ids[1] || ids[1] == ids[2] || ids[2] != ids[3] {
		t.Fatalf("upgrade/downgrade/recovery identities=%v", ids)
	}
	last, err := repo.GetAlert(t.Context(), c.TenantID, ids[3])
	if err != nil || last.Alert.Status != domain.AlertStatusRecovered || last.Alert.Severity != "warning" {
		t.Fatal(last, err)
	}
	if hook.calls < 4 {
		t.Fatalf("output hooks did not observe lifecycle: %d", hook.calls)
	}
}

type eventgenSeverity struct{}

func (eventgenSeverity) Priority(name string) (int, bool) {
	values := map[string]int{"critical": 1, "warning": 2, "info": 3}
	v, ok := values[name]
	return v, ok
}

type eventgenHook struct{ calls int }

func (h *eventgenHook) Execute(ctx context.Context, _ lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	h.calls++
	return lifecycle.FinalHookResult{}, ctx.Err()
}
