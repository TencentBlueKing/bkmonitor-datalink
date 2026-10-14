package assembly

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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

// 2026-10-14 test-bkee5-vpn 原始事件的脱敏副本：保留检测事实和版本，
// 替换租户及来源身份，移除资源维度和摘要；普通测试不访问线上依赖。
//
//go:embed testdata/no_data_events.json
var noDataEventFixtures []byte

type noDataConfigurationReader struct {
	configuration description.Configuration
	reads         int
}

func (r *noDataConfigurationReader) ReadConfiguration(ctx context.Context, q description.ConfigurationQuery) (description.Configuration, error) {
	if err := ctx.Err(); err != nil {
		return description.Configuration{}, err
	}
	if q != r.configuration.Identity {
		return description.Configuration{}, errors.New("fixture configuration identity mismatch")
	}
	r.reads++
	return r.configuration, nil
}

func TestNoDataRawEventsCreateImmutableContentAndPreview(t *testing.T) {
	t.Parallel()
	var samples []struct {
		Name     string          `json:"name"`
		Payload  json.RawMessage `json:"payload"`
		ItemName string          `json:"item_name"`
		Expected string          `json:"expected"`
	}
	if err := json.Unmarshal(noDataEventFixtures, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatal("expected log and metric source fixtures")
	}
	for _, sample := range samples {
		for _, failure := range []string{"", "nodata_identity_invalid", "nodata_configuration_invalid", "nodata_evidence_missing", "nodata_evidence_invalid"} {
			t.Run(sample.Name+"/"+failure, func(t *testing.T) {
				t.Parallel()
				payload := sample.Payload
				if failure != "" {
					var raw domain.JSONObject
					if err := json.Unmarshal(payload, &raw); err != nil {
						t.Fatal(err)
					}
					var extra domain.JSONObject
					if err := json.Unmarshal(raw["extra_data"], &extra); err != nil {
						t.Fatal(err)
					}
					switch failure {
					case "nodata_identity_invalid":
						extra["evaluation_family"] = json.RawMessage(`"metric_algorithm"`)
					case "nodata_evidence_missing":
						delete(extra, "no_data_periods")
					case "nodata_evidence_invalid":
						extra["no_data_periods"] = json.RawMessage(`null`)
					}
					var err error
					raw["extra_data"], err = json.Marshal(extra)
					if err != nil {
						t.Fatal(err)
					}
					payload, err = json.Marshal(raw)
					if err != nil {
						t.Fatal(err)
					}
				}
				source := baseCollectEventSource()
				source.Version, source.RelatedTenantID = 7, "tenant-fixture"
				source.Enrich = config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}
				mapper, err := cleaner.NewMapper(source, config.DefaultSeverityConfig())
				if err != nil {
					t.Fatal(err)
				}
				event, err := mapper.MapMessage(t.Context(), consume.Message{ID: "captured-nodata-" + sample.Name + failure, TenantID: source.RelatedTenantID, EnqueuedAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), Body: payload})
				if err != nil {
					t.Fatal(err)
				}
				want := sample.Expected
				if failure != "" {
					want = event.Content
				}
				before, err := json.Marshal(event.SourceRawData)
				if err != nil {
					t.Fatal(err)
				}
				strategy, _ := event.Labels["strategy_id"].NumberValue()
				version, _ := event.Labels["strategy_version"].NumberValue()
				business, _ := event.Labels["bk_biz_id"].NumberValue()
				reader := &noDataConfigurationReader{configuration: description.Configuration{
					Identity: description.ConfigurationQuery{TenantID: event.BKTenantID, StrategyID: int64(strategy), StrategyVersion: int64(version), BusinessID: int64(business)},
					// 样本的两个监控项均为 COUNT，普通阈值等级为 3，无数据等级为 1。
					Spec:       models.CWStrategySpec{Name: sample.ItemName[len("COUNT(") : len(sample.ItemName)-1], StrategyItem: &models.CWStrategyItem{AggregateMethod: "COUNT", QueryConfigs: []models.StrategyQueryConfig{{}}, NoDataConfig: domain.JSONObject{"is_enabled": json.RawMessage(`true`), "level": json.RawMessage(`1`), "continuous": json.RawMessage(`5`)}}},
					Algorithms: []description.RuntimeAlgorithm{{Type: "Threshold", Level: 3}},
				}}
				if failure == "nodata_configuration_invalid" {
					reader.configuration.Spec.StrategyItem.NoDataConfig["is_enabled"] = json.RawMessage(`false`)
				}
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
				processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, discardLogger{}, lifecycle.WithAlertContentBuilder(router))
				if err != nil {
					t.Fatal(err)
				}
				result, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
				if err != nil || len(result.AlertIDs) != 1 {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				alert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
				if err != nil || alert.Alert.Severity != "critical" || alert.Alert.Content != want || reader.reads != 1 {
					t.Fatalf("content=%q reads=%d error=%v", alert.Alert.Content, reader.reads, err)
				}
				if _, err := processor.ProcessEvent(t.Context(), stored.StoredEvent); err != nil {
					t.Fatal(err)
				}
				replayed, err := repo.GetAlert(t.Context(), event.BKTenantID, alert.Alert.AlertID)
				if err != nil || replayed.Alert.Content != want || reader.reads != 1 {
					t.Fatal("replay regenerated opening content")
				}
				encoded, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				service := preview.New(contentLivePreviewSource{source}, func(context.Context, string, string) (domain.Event, error) {
					return domain.Event{}, errors.New("opening preview must not read stored Alert")
				}, func(context.Context, config.EventSource) (preview.Enricher, func() error, error) {
					return router, func() error { return nil }, nil
				})
				candidate, err := service.Preview(t.Context(), preview.Request{BKTenantID: event.BKTenantID, EventSourceID: source.EventSourceID, Input: preview.Input{OpeningEvent: encoded, Severity: "critical"}})
				if err != nil || candidate.CandidateContent == nil || *candidate.CandidateContent != want {
					t.Fatalf("preview error=%v", err)
				}
				kept, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil {
					t.Fatal(err)
				}
				after, err := json.Marshal(kept.Event.SourceRawData)
				if err != nil || string(before) != string(after) || kept.Event.Content != event.Content || len(kept.Event.Values) != 0 {
					t.Fatal("no-data generation changed source facts or invented a value")
				}
			})
		}
	}
}
