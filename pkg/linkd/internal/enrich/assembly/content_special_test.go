package assembly

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/preview"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
)

// Kingeye 358357f8 docs/references/alarmd-linkd-event-fields.md 的 5.1/5.4
// 原始 JSON 示例；重启和自定义级别用例在同一协议上替换检测值/级别。
//
//go:embed testdata/special_events.json
var specialEventFixtures []byte

func TestSpecialRawEventsCreateContentAndPreview(t *testing.T) {
	t.Parallel()
	var samples map[string]domain.JSONObject
	if err := json.Unmarshal(specialEventFixtures, &samples); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, algorithm, metric, severity, value, want, reason string }{
		{name: "process", algorithm: "ProcPort", metric: "bk_monitor.proc_port", severity: "warning", want: "当前进程(nginx)存在，端口([443])不存在"},
		{name: "restart", algorithm: "OsRestart", metric: "bk_monitor.os_restart", severity: "critical", value: "120", want: "当前服务器在120秒前发生系统重启事件"},
		{name: "year amplitude", algorithm: "YearRoundAmplitude", metric: "plain", severity: "critical", reason: "detection_evidence_missing"},
		{name: "year range", algorithm: "YearRoundRange", metric: "plain", severity: "critical", reason: "detection_evidence_missing"},
		{name: "custom severity", algorithm: "Threshold", metric: "plain", severity: "minor", reason: "severity_mapping_missing"},
		{name: "unnamed custom severity", algorithm: "Threshold", metric: "plain", severity: "level_4", reason: "severity_mapping_missing"},
		{name: "missing process facts", algorithm: "ProcPort", metric: "bk_monitor.proc_port", severity: "critical", reason: "process_evidence_missing"},
		{name: "old uptime in triggered window", algorithm: "OsRestart", metric: "bk_monitor.os_restart", severity: "critical", value: "601", reason: "detection_evidence_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := samples["metric"].Clone()
			if tc.name == "process" {
				raw = samples["process"].Clone()
			}
			raw["evaluations"] = json.RawMessage(`[{"severity":"` + tc.severity + `","action":"triggered","action_reason":""}]`)
			if tc.value != "" {
				raw["values"] = json.RawMessage(`{"value":` + tc.value + `}`)
			}
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			source := baseCollectEventSource()
			source.Version = 7
			source.RelatedTenantID = "system"
			source.Enrich = config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}
			levels := config.DefaultSeverityConfig()
			levels.Levels = append(levels.Levels, config.SeverityLevel{Name: "minor", Priority: 4}, config.SeverityLevel{Name: "level_4", Priority: 5})
			source.SeverityMapping = map[string]string{"critical": "critical", "warning": "warning", "minor": "minor", "level_4": "level_4"}
			mapper, err := cleaner.NewMapper(source, levels)
			if err != nil {
				t.Fatal(err)
			}
			event, err := mapper.MapMessage(t.Context(), consume.Message{ID: tc.name, TenantID: "system", EnqueuedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), Body: body})
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			strategy, _ := event.Labels["strategy_id"].NumberValue()
			version, _ := event.Labels["strategy_version"].NumberValue()
			business, _ := event.Labels["bk_biz_id"].NumberValue()
			level := int64(1)
			if tc.severity == "warning" {
				level = 2
			}
			if tc.severity == "minor" || tc.severity == "level_4" {
				level = 4
			}
			reader := &noDataConfigurationReader{configuration: description.Configuration{
				Identity: description.ConfigurationQuery{TenantID: event.BKTenantID, StrategyID: int64(strategy), StrategyVersion: int64(version), BusinessID: int64(business)},
				Spec:     models.CWStrategySpec{Name: "示例指标", MonitorItemType: "event", StrategyItem: &models.CWStrategyItem{}},
				Queries:  []models.StrategyQueryConfig{{MetricID: tc.metric}}, Algorithms: []description.RuntimeAlgorithm{{Type: tc.algorithm, Level: level, Config: json.RawMessage(`[[{"method":"gt","threshold":1}]]`)}},
			}}
			resolver, err := description.NewResolver(reader)
			if err != nil {
				t.Fatal(err)
			}
			router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(resolver))
			if err != nil {
				t.Fatal(err)
			}
			repo := memory.New()
			stored, err := repo.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, levels, lifecycle.SystemClock{}, slog.New(slog.NewJSONHandler(&logs, nil)), lifecycle.WithAlertContentBuilder(router))
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
			if err != nil || len(result.AlertIDs) != 1 || result.EventState != domain.EventProcessStateAccepted {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			want := tc.want
			if tc.reason != "" {
				want = event.Content
				if !strings.Contains(logs.String(), `"reason_code":"`+tc.reason+`"`) || !strings.Contains(logs.String(), "source_fallback") || strings.Contains(logs.String(), event.Content) {
					t.Fatalf("diagnostic=%s", logs.String())
				}
			}
			alert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
			if err != nil || alert.Alert.Content != want || alert.Alert.Severity != tc.severity {
				t.Fatalf("content=%q severity=%s error=%v", alert.Alert.Content, alert.Alert.Severity, err)
			}
			if _, err := processor.ProcessEvent(t.Context(), stored.StoredEvent); err != nil || reader.reads != 1 {
				t.Fatalf("replay reads=%d error=%v", reader.reads, err)
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			service := preview.New(contentLivePreviewSource{source}, func(context.Context, string, string) (domain.Event, error) {
				return domain.Event{}, errors.New("preview must not read stored Alert")
			}, func(context.Context, config.EventSource) (preview.Enricher, func() error, error) {
				return router, func() error { return nil }, nil
			})
			candidate, err := service.Preview(t.Context(), preview.Request{BKTenantID: event.BKTenantID, EventSourceID: source.EventSourceID, Input: preview.Input{OpeningEvent: encoded, Severity: tc.severity}})
			if err != nil || candidate.CandidateContent == nil || *candidate.CandidateContent != want {
				t.Fatalf("preview=%+v error=%v", candidate, err)
			}
			kept, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(kept.Event)
			// 生命周期会更新派生状态；检测输入和原始文案不能被展示处理覆盖。
			if err != nil || kept.Event.Content != event.Content || !bytes.Equal(kept.Event.SourceRawData["values"], event.SourceRawData["values"]) {
				t.Fatalf("source changed: %s", after)
			}
			original, err := json.Marshal(event)
			if err != nil || !bytes.Equal(before, original) {
				t.Fatal("caller Event mutated")
			}
		})
	}
}
