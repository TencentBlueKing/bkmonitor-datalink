// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/processors"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func TestHistoricalRevisionCreatesContentAndReplayKeepsPlan(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"historical revision", "aggregate missing", "name missing", "history missing", "revision missing"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			reads := 0
			db := openReaderTestDB(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(query, "home_application_monitortemplate") {
					return &readerTestRows{columns: []string{"id", "bk_tenant_id", "name"}, values: [][]driver.Value{{int64(7), "tenant", "CPU template"}}}, nil
				}
				reads++
				assertRevisionQuery(t, ctx, query, args)
				payload := revisionTestSnapshot(t)
				revisionItem(payload)["algorithms"].([]any)[0].(map[string]any)["unit_prefix"] = ""
				spec := revisionSpec(payload)
				switch scenario {
				case "aggregate missing":
					delete(spec["strategy_item"].(map[string]any), "agg_method")
				case "name missing":
					delete(spec, "name")
				case "history missing":
					revisionItem(payload)["algorithms"].([]any)[0].(map[string]any)["type"] = "SimpleRingRatio"
				}
				rows := snapshotTestRows(t, payload)
				if scenario == "revision missing" {
					rows.values = nil
				}
				return rows, nil
			})
			reader, err := NewDescriptionConfigurationClient(db)
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := description.NewResolver(reader)
			if err != nil {
				t.Fatal(err)
			}
			builder, err := description.NewBuilder(resolver)
			if err != nil {
				t.Fatal(err)
			}
			strategyClient, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			chain, err := enrich.NewChain([]enrich.Processor{processors.Strategy{}}, enrich.Sources{CWStrategy: strategyClient})
			if err != nil {
				t.Fatal(err)
			}
			repo := memory.New()
			processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, chain, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithAlertContentBuilder(builder))
			if err != nil {
				t.Fatal(err)
			}
			event := storetest.Event("tenant", "event", "fingerprint", "warning")
			strategyID, err := domain.NewNumberScalar(1)
			if err != nil {
				t.Fatal(err)
			}
			version, err := domain.NewNumberScalar(float64(revisionTestVersion))
			if err != nil {
				t.Fatal(err)
			}
			business, err := domain.NewNumberScalar(2)
			if err != nil {
				t.Fatal(err)
			}
			event.Labels = domain.DimensionMap{"strategy_id": strategyID, "strategy_version": version, "bk_biz_id": business}
			event.Content = "source content"
			event.Values = domain.EventValues{"value": 90}
			event.SourceRawData = domain.JSONObject{"values": json.RawMessage(`{"value":90}`)}
			stored, err := repo.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.AlertIDs) != 1 {
				t.Fatalf("alerts=%v", result.AlertIDs)
			}
			alert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			wantContent := "AVG(CPU) > 80.0, 当前值90%"
			if scenario != "historical revision" {
				wantContent = event.Content
			}
			if alert.Alert.Content != wantContent {
				t.Fatalf("content=%q want=%q", alert.Alert.Content, wantContent)
			}
			wantStatus := domain.EnrichStatusSucceeded
			if scenario == "revision missing" {
				wantStatus = domain.EnrichStatusPartial
			}
			if alert.Alert.EnrichStatus != wantStatus {
				t.Fatalf("frozen enrich status=%s want=%s", alert.Alert.EnrichStatus, wantStatus)
			}
			completed, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := processor.ProcessEvent(t.Context(), completed); err != nil {
				t.Fatal(err)
			}
			wantReads := 2 // 普通丰富与文案各读取同一精确历史版本一次。
			if reads != wantReads {
				t.Fatalf("strategy reads=%d want=%d", reads, wantReads)
			}
			kept, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil || kept.Event.Content != event.Content {
				t.Fatalf("source Event changed: %v", err)
			}
		})
	}
}
