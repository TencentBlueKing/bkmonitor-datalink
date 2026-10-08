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
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/preview"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
)

// 显式接收有界的真实 Kafka 样本与 bk-monitor 源码 oracle 文案。策略只读，
// Event/Alert 写入本地 MemoryRepository；不提交 Kafka offset，不写线上 ES。
func TestContentLiveEventWithBoundConfiguration(t *testing.T) {
	dsn, file := os.Getenv("LINKD_CONTENT_TEST_MYSQL_DSN"), os.Getenv("LINKD_CONTENT_TEST_EVENT_FILE")
	if dsn == "" || file == "" {
		t.Skip("requires read-only MySQL and captured event fixture")
	}
	info, err := os.Stat(file) //nolint:gosec // G703: 显式集成测试读取操作者指定的本地临时 fixture，不接受服务请求路径。
	if err != nil || info.Size() > 8<<20 {
		t.Fatal("integration fixture unavailable or exceeds bound")
	}
	raw, err := os.ReadFile(file) //nolint:gosec // G304: 同上；文件大小已限制为 8 MiB，线上不使用此入口。
	if err != nil {
		t.Fatal("read integration fixture failed")
	}
	var samples []struct {
		Payload   json.RawMessage   `json:"payload"`
		Expected  map[string]string `json:"expected"`
		Partition int               `json:"partition"`
		Offset    int64             `json:"offset"`
	}
	if json.Unmarshal(raw, &samples) != nil || len(samples) == 0 || len(samples) > 20 {
		t.Fatal("invalid bounded integration samples")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open integration database failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("integration database unavailable")
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error("close integration database failed")
		}
	})
	reader, err := datasources.NewDescriptionConfigurationClient(db)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := description.NewResolver(reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range samples {
		source := baseCollectEventSource()
		source.Version = 7
		source.RelatedTenantID = os.Getenv("LINKD_CONTENT_TEST_TENANT")
		source.Enrich = config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}
		mapper, err := cleaner.NewMapper(source, config.DefaultSeverityConfig())
		if err != nil {
			t.Fatal(err)
		}
		event, err := mapper.MapMessage(t.Context(), consume.Message{ID: "captured-alarmd-event", TenantID: source.RelatedTenantID, EnqueuedAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Body: sample.Payload})
		if err != nil {
			t.Fatal("standard event mapping failed")
		}
		router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(resolver))
		if err != nil {
			t.Fatal(err)
		}
		repo := memory.New()
		stored, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal("local event persistence failed")
		}
		processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, discardLogger{}, lifecycle.WithAlertContentBuilder(router))
		if err != nil {
			t.Fatal(err)
		}
		result, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
		if err != nil {
			t.Fatalf("partition=%d offset=%d lifecycle error=%v", sample.Partition, sample.Offset, err)
		}
		// Lifecycle 在空仓库中只按最高 triggered 级别创建一个 Alert；
		// 同事件其他级别的 oracle 结果用于核对选择，不能预期每级别都创建。
		selectedSeverity := ""
		for _, level := range config.DefaultSeverityConfig().Levels {
			if _, ok := sample.Expected[level.Name]; ok {
				selectedSeverity = level.Name
				break
			}
		}
		if selectedSeverity == "" || len(result.AlertIDs) != 1 {
			t.Fatalf("partition=%d offset=%d alert count mismatch", sample.Partition, sample.Offset)
		}
		for _, id := range result.AlertIDs {
			alert, err := repo.GetAlert(t.Context(), event.BKTenantID, id)
			if err != nil {
				t.Fatal("read local Alert failed")
			}
			want, ok := sample.Expected[alert.Alert.Severity]
			if !ok || alert.Alert.Severity != selectedSeverity || alert.Alert.Content != want || alert.Alert.Content == event.Content {
				t.Fatalf("partition=%d offset=%d severity=%s description differs from source oracle", sample.Partition, sample.Offset, alert.Alert.Severity)
			}
		}
		encodedEvent, err := json.Marshal(event)
		if err != nil {
			t.Fatal("encode opening preview failed")
		}
		closed := false
		service := preview.New(contentLivePreviewSource{source}, func(context.Context, string, string) (domain.Event, error) {
			t.Fatal("opening preview must not read stored Alert")
			return domain.Event{}, nil
		}, func(context.Context, config.EventSource) (preview.Enricher, func() error, error) {
			return router, func() error { closed = true; return nil }, nil
		})
		candidate, err := service.Preview(t.Context(), preview.Request{BKTenantID: event.BKTenantID, EventSourceID: source.EventSourceID, Input: preview.Input{OpeningEvent: encodedEvent, Severity: selectedSeverity}})
		if err != nil {
			t.Fatalf("partition=%d offset=%d preview failed: %v", sample.Partition, sample.Offset, err)
		}
		want := sample.Expected[selectedSeverity]
		if candidate.CandidateContent == nil || *candidate.CandidateContent != want || candidate.EffectiveAlert["content"] != want || candidate.Original["content"] != event.Content || !closed {
			t.Fatalf("partition=%d offset=%d preview description differs from persisted result", sample.Partition, sample.Offset)
		}
		if _, err := processor.ProcessEvent(t.Context(), stored.StoredEvent); err != nil {
			t.Fatal("replay failed")
		}
		kept, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
		if err != nil || kept.Event.Content != event.Content {
			t.Fatal("source content changed")
		}
		t.Logf("partition=%d offset=%d alerts=%d description matched upstream source, local persistence and opening preview", sample.Partition, sample.Offset, len(result.AlertIDs))
	}
}

// 来源发布是本地显式 fixture；集成验证不发布或修改线上 EventSource。
type contentLivePreviewSource struct{ source config.EventSource }

func (s contentLivePreviewSource) Get(context.Context, string) (eventsource.Record, error) {
	return eventsource.Record{ID: s.source.EventSourceID, Published: s.source.Version}, nil
}

func (s contentLivePreviewSource) GetRelease(context.Context, string, int64) (eventsource.Release, error) {
	return eventsource.Release{Version: s.source.Version, Spec: s.source}, nil
}
