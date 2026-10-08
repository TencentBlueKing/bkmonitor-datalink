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
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
)

// contentReviewRecord 仅导出文案审阅所需的字段，不导出来源 payload、维度、
// ExtraData 或实例身份。采集脚本在落入仓库前再处理文本中的敏感信息。
type contentReviewRecord struct {
	Partition       int             `json:"partition"`
	Offset          int64           `json:"offset"`
	Context         json.RawMessage `json:"context"`
	Status          string          `json:"status"`
	ReasonCode      string          `json:"reason_code,omitempty"`
	SourceContent   string          `json:"source_content"`
	ExpectedContent string          `json:"bkmonitor_expected_content,omitempty"`
	Alert           map[string]any  `json:"alert,omitempty"`
	OracleMatched   bool            `json:"oracle_matched"`
	PreviewMatched  bool            `json:"preview_matched"`
	ReplayPreserved bool            `json:"replay_preserved"`
	EventPreserved  bool            `json:"event_preserved"`
	NoPlanOnFailure bool            `json:"no_plan_on_failure,omitempty"`
}

// TestContentReviewLiveSamples 显式重放最多 64 条真实输入并导出成功和拒绝样本。
// MySQL Reader 只读；Alert/Event 只写本地 MemoryRepository，来源配置为本地
// fixture，不发布 EventSource、不写线上 ES、不提交 Kafka offset、不发送 Hook。
func TestContentReviewLiveSamples(t *testing.T) {
	dsn, input, output := os.Getenv("LINKD_CONTENT_TEST_MYSQL_DSN"), os.Getenv("LINKD_CONTENT_TEST_EVENT_FILE"), os.Getenv("LINKD_CONTENT_TEST_REVIEW_FILE")
	if dsn == "" || input == "" || output == "" {
		t.Skip("requires explicit read-only database, captured samples and private review output")
	}
	info, err := os.Stat(input) //nolint:gosec // G703: 操作者指定的集成测试路径，不是服务输入。
	if err != nil || info.Size() > 8<<20 {
		t.Fatal("review input unavailable or exceeds 8 MiB")
	}
	raw, err := os.ReadFile(input) //nolint:gosec // G304: 同上，已限制输入大小。
	if err != nil {
		t.Fatal("read review input failed")
	}
	var samples []struct {
		Payload   json.RawMessage   `json:"payload"`
		Expected  map[string]string `json:"expected"`
		Context   json.RawMessage   `json:"context"`
		Partition int               `json:"partition"`
		Offset    int64             `json:"offset"`
	}
	if json.Unmarshal(raw, &samples) != nil || len(samples) == 0 || len(samples) > 64 {
		t.Fatal("invalid bounded review samples")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open review database failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("review database unavailable")
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error("close review database failed")
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
	records := make([]contentReviewRecord, 0, len(samples))
	for _, sample := range samples {
		record := contentReviewRecord{Partition: sample.Partition, Offset: sample.Offset, Context: sample.Context}
		source := baseCollectEventSource()
		source.Version, source.RelatedTenantID = 7, os.Getenv("LINKD_CONTENT_TEST_TENANT")
		source.Enrich = config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}
		mapper, err := cleaner.NewMapper(source, config.DefaultSeverityConfig())
		if err != nil {
			t.Fatal(err)
		}
		event, err := mapper.MapMessage(t.Context(), consume.Message{ID: "captured-review-event", TenantID: source.RelatedTenantID, EnqueuedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), Body: sample.Payload})
		if err != nil {
			record.Status, record.ReasonCode = "rejected", "standard_mapping_failed"
			records = append(records, record)
			continue
		}
		record.SourceContent = event.Content
		router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(resolver))
		if err != nil {
			t.Fatal(err)
		}
		repo := memory.New()
		stored, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal("persist review Event failed")
		}
		processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, discardLogger{}, lifecycle.WithAlertContentBuilder(router))
		if err != nil {
			t.Fatal(err)
		}
		result, processErr := processor.ProcessEvent(t.Context(), stored.StoredEvent)
		if processErr != nil {
			record.Status, record.ReasonCode = "dependency_error", "dependency_error"
			var permanent interface{ PermanentContentFailure() string }
			if errors.As(processErr, &permanent) {
				record.Status, record.ReasonCode = "blocked", permanent.PermanentContentFailure()
			}
			kept, readErr := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			record.NoPlanOnFailure = readErr == nil && kept.Processing.Plan == nil && kept.Processing.State == domain.EventProcessStateUnprocessed && len(result.AlertIDs) == 0
			record.EventPreserved = readErr == nil && kept.Event.Content == event.Content
			if !record.NoPlanOnFailure || !record.EventPreserved {
				t.Error("failed content generation changed local persistent state")
			}
			records = append(records, record)
			continue
		}
		if len(result.AlertIDs) != 1 {
			record.Status, record.ReasonCode = "no_alert", "no_opening_alert"
			records = append(records, record)
			continue
		}
		storedAlert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
		if err != nil {
			t.Fatal("read review Alert failed")
		}
		alert := storedAlert.Alert
		record.Status = "generated"
		record.Alert = map[string]any{"alert_id": alert.AlertID, "bk_tenant_id": alert.BKTenantID, "event_source_id": alert.EventSourceID, "event_source_version": alert.EventSourceVersion, "title": alert.Title, "content": alert.Content, "severity": alert.Severity, "status": alert.Status, "begin_at": alert.BeginAt, "trigger_event_id": alert.TriggerEventID, "enrich_status": alert.EnrichStatus}
		record.ExpectedContent, record.OracleMatched = sample.Expected[alert.Severity], false
		want, hasOracle := sample.Expected[alert.Severity]
		record.OracleMatched = hasOracle && want == alert.Content
		encodedEvent, err := json.Marshal(event)
		if err != nil {
			t.Fatal("encode review opening Event failed")
		}
		closed := false
		service := preview.New(contentLivePreviewSource{source}, func(context.Context, string, string) (domain.Event, error) {
			t.Error("opening preview unexpectedly read stored Alert")
			return domain.Event{}, nil
		}, func(context.Context, config.EventSource) (preview.Enricher, func() error, error) {
			return router, func() error { closed = true; return nil }, nil
		})
		candidate, previewErr := service.Preview(t.Context(), preview.Request{BKTenantID: event.BKTenantID, EventSourceID: source.EventSourceID, Input: preview.Input{OpeningEvent: encodedEvent, Severity: alert.Severity}})
		record.PreviewMatched = previewErr == nil && candidate.CandidateContent != nil && *candidate.CandidateContent == alert.Content && candidate.EffectiveAlert["content"] == alert.Content && closed
		_, replayErr := processor.ProcessEvent(t.Context(), stored.StoredEvent)
		replayed, readErr := repo.GetAlert(t.Context(), event.BKTenantID, alert.AlertID)
		record.ReplayPreserved = replayErr == nil && readErr == nil && replayed.Alert.Content == alert.Content
		kept, readErr := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
		record.EventPreserved = readErr == nil && kept.Event.Content == event.Content
		if !record.OracleMatched || !record.PreviewMatched || !record.ReplayPreserved || !record.EventPreserved {
			t.Errorf("partition=%d offset=%d review invariant mismatch", sample.Partition, sample.Offset)
		}
		records = append(records, record)
	}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil || len(encoded) > 8<<20 {
		t.Fatal("review output exceeds bound")
	}
	// 只创建新的私有文件，避免覆盖操作者已有结果；不写来源完整 payload。
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304,G703: 显式集成测试输出路径，非服务请求。
	if err != nil {
		t.Fatal("create private review output failed")
	}
	_, writeErr := f.Write(append(encoded, '\n'))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("write review output failed")
	}
	t.Logf("exported %d bounded review records", len(records))
}
