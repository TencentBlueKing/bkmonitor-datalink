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
	"strings"
	"testing"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/datasources"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/store/memory"
)

func TestBaseCollectRawEventRunsLifecycleEnrichment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := baseCollectEventSource()
	mapper, err := cleaner.NewMapper(source, config.DefaultSeverityConfig())
	if err != nil {
		t.Fatal(err)
	}
	event, err := mapper.MapMessage(ctx, consume.Message{
		ID: "kafka-linkd-base-collect-0001", TenantID: datasources.SampleTenantID,
		EnqueuedAt: time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC),
		Headers:    map[string][]byte{"bk_tenant_id": []byte(datasources.SampleTenantID)},
		Body:       baseCollectRawPayload(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.EventSourceID != source.EventSourceID || event.SourceEventID != "source-event-1" ||
		event.SourceAlertID != "source-alert-1" || event.SubjectName != "host-101" {
		t.Fatalf("event=%#v", event)
	}

	dataSources := (&datasources.Mock{}).Sources()
	dataSources.CWStrategy = baseCollectCWStrategy{}
	dataSources.OneModel = baseCollectOneModel{}
	dataSources.Metric = baseCollectMetric{}
	dataSources.AlarmSource = baseCollectAlarmSource{}
	router, err := NewRouter([]config.EventSource{source}, dataSources)
	if err != nil {
		t.Fatal(err)
	}
	repository := memory.New()
	storedEvent, err := repository.CreateEvent(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := lifecycle.NewProcessor(
		repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router,
		lifecycle.NoopFinalHook{}, config.DefaultSeverityConfig(), fixedClock{now: time.Date(2026, 9, 1, 0, 0, 3, 0, time.UTC)}, discardLogger{},
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessEvent(ctx, storedEvent.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	storedAlert, err := repository.GetAlert(ctx, datasources.SampleTenantID, processed.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	alert := storedAlert.Alert
	if processed.Outcome != lifecycle.OutcomeAlertCreated || alert.EnrichStatus != domain.EnrichStatusSucceeded {
		t.Fatalf("processed=%#v alert=%#v", processed, alert)
	}
	payload, err := enrich.DecodePayload(alert.Enrich)
	if err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"strategy", "resource", "display", "metric", "source"} {
		if envelope, exists := payload.Processors[index][name]; !exists || envelope.Status != domain.EnrichStatusSucceeded {
			t.Fatalf("processors[%d]=%#v", index, payload.Processors[index])
		}
	}
	assertBaseCollectEnrichment(t, payload)
}

func TestRouterRunsConfiguredBaseCollectSlice(t *testing.T) {
	t.Parallel()
	sources := []config.EventSource{{
		EventSourceID: "built_in_bk",
		Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{
			{Type: "strategy"}, {Type: "resource"}, {Type: "display"}, {Type: "metric"}, {Type: "source"},
		}},
	}, {EventSourceID: "disabled-source"}}
	dataSources := (&datasources.Mock{}).Sources()
	dataSources.OneModel = sampleOneModel{}
	dataSources.Metric = sampleMetric{}
	dataSources.AlarmSource = sampleAlarmSource{}
	router, err := NewRouter(sources, dataSources)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Enrich(context.Background(), lifecycle.EnrichInput{Alert: baseCollectAlert("built_in_bk")})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusSucceeded || len(payload.Processors) != 5 {
		t.Fatalf("result=%#v payload=%#v", result, payload)
	}
	for index, name := range []string{"strategy", "resource", "display", "metric", "source"} {
		if _, exists := payload.Processors[index][name]; !exists {
			t.Fatalf("processors[%d]=%#v", index, payload.Processors[index])
		}
	}
	strategy := payload.Processors[0]["strategy"].Value
	if string(strategy["bk_strategy_id"]) != "123" || string(strategy["strategy_config_id"]) != `"strategyconfig1"` ||
		!strings.Contains(string(strategy["strategy_name"]), "CPU") {
		t.Fatalf("strategy=%#v", strategy)
	}
	if got := string(strategy["url"]); got != `"/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=1\u0026strategy_config_id=strategyconfig1"` {
		t.Fatalf("strategy url=%s", got)
	}
	if _, exists := strategy["monitor_strategy_id"]; exists {
		t.Fatalf("strategy retains monitor_strategy_id: %#v", strategy)
	}
	source := payload.Processors[4]["source"].Value
	if string(source["source_id"]) != `"built_in_bk"` || string(source["source_name"]) != `"鲸眼监控"` ||
		string(source["meta_info"]) != `"source-event-1"` {
		t.Fatalf("source=%#v", source)
	}
	noop, err := router.Enrich(context.Background(), lifecycle.EnrichInput{Alert: baseCollectAlert("disabled-source")})
	if err != nil {
		t.Fatal(err)
	}
	noopPayload, _ := enrich.DecodePayload(noop.Data)
	if len(noopPayload.Processors) != 0 || noop.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("noop=%#v", noop)
	}
}

func TestRouterReportsEnrichChainKind(t *testing.T) {
	t.Parallel()
	router, err := NewRouter([]config.EventSource{
		{EventSourceID: "configured", Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "source"}}}},
		{EventSourceID: "noop"},
	}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if router.EnrichChainKind("configured") != lifecycle.EnrichChainConfigured ||
		router.EnrichChainKind("noop") != lifecycle.EnrichChainNoop ||
		router.EnrichChainKind("missing") != lifecycle.EnrichChainUnknown {
		t.Fatalf("configured=%q noop=%q missing=%q", router.EnrichChainKind("configured"), router.EnrichChainKind("noop"), router.EnrichChainKind("missing"))
	}
}

func TestRouterRejectsUnknownSourceAndProcessor(t *testing.T) {
	t.Parallel()
	router, err := NewRouter([]config.EventSource{{EventSourceID: "known"}}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Enrich(context.Background(), lifecycle.EnrichInput{Alert: baseCollectAlert("unknown")}); err == nil {
		t.Fatal("unknown source was accepted")
	}
	_, err = NewRouter([]config.EventSource{{EventSourceID: "known", Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "unknown"}}}}}, enrich.Sources{})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unknown processor error=%v", err)
	}
}

func TestRouterInvalidInputFailsBeforeDataSources(t *testing.T) {
	t.Parallel()
	router, err := NewRouter([]config.EventSource{{EventSourceID: "built_in_bk", Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "strategy"}}}}}, panicSources{}.Sources())
	if err != nil {
		t.Fatal(err)
	}
	alert := baseCollectAlert("built_in_bk")
	delete(alert.Labels, "bk_strategy_id")
	result, err := router.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := enrich.DecodePayload(result.Data)
	entry := payload.Processors[0]["strategy"]
	if result.Status != domain.EnrichStatusFailed || entry.Diagnostics[0].Code != enrich.DiagnosticCodeMissingField {
		t.Fatalf("result=%#v entry=%#v", result, entry)
	}
}

func baseCollectEventSource() config.EventSource {
	return config.EventSource{
		EventSourceID: "built_in_bk", Enabled: true,
		Cleaner:         config.CleanerConfig{Type: config.CleanerTypeStandard},
		FingerprintMode: config.FingerprintModeField, FingerprintField: "source_alert_id",
		DefaultSeverity: "warning",
		Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{
			{Type: "strategy"}, {Type: "resource"}, {Type: "display"}, {Type: "metric"}, {Type: "source"},
		}},
		Storage: config.EventSourceStorageConfig{Type: config.StorageTypeKafka, Kafka: config.KafkaStorageConfig{
			Brokers: []string{"127.0.0.1:9092"}, Topic: "linkd-base-collect",
			ConsumerGroup: "linkd-base-collect-cleaner",
		}},
	}.WithDefaults()
}

func baseCollectRawPayload() []byte {
	return []byte(`{
		"event_id":"source-event-1","alert_id":"source-alert-1",
		"title":"CPU usage is high","content":"Host 10.0.0.1 CPU usage reached 92.5%",
		"severity":"warning","action":"triggered","action_reason":"",
		"condition_key":"cpu_usage","condition_name":"CPU 使用率",
		"dimensions":{"bk_inst_id":101,"bk_target_ip":"10.0.0.1","bk_target_cloud_id":0},
		"subject":{"system":"cmdb","type":"host","id":"101","name":"host-101"},
		"occurred_at":"2026-09-01T00:00:00Z","produced_at":"2026-09-01T00:00:01Z",
		"labels":{"bk_strategy_id":123,"bk_strategy_history_id":70001,"bk_biz_id":2},
		"extra_data":{"anomaly_begin_time":"2026-09-01T00:00:00Z"}
	}`)
}

func assertBaseCollectEnrichment(t *testing.T, payload enrich.Payload) {
	t.Helper()
	strategy := payload.Processors[0]["strategy"].Value
	if string(strategy["bk_strategy_id"]) != "123" || string(strategy["strategy_config_id"]) != `"strategyconfig1"` {
		t.Fatalf("strategy=%#v", strategy)
	}
	resource := payload.Processors[1]["resource"].Value
	if string(resource["model_id"]) != `"cw-Host"` || string(resource["bk_inst_id"]) != "101" ||
		string(resource["bk_biz_name"]) != `"业务 2"` {
		t.Fatalf("resource=%#v", resource)
	}
	display := payload.Processors[2]["display"].Value
	if string(display["title"]) != `"CPU 使用率过高"` || string(display["object"]) != `"host-101"` {
		t.Fatalf("display=%#v", display)
	}
	metric := payload.Processors[3]["metric"].Value
	if string(metric["display_name"]) != `"CPU 使用率"` || string(metric["metric_name"]) != `"usage"` ||
		string(metric["unit"]) != `"percent"` || string(metric["result_table_id"]) != `"system.cpu"` ||
		string(metric["anomaly_begin_time"]) != `"2026-09-01T00:00:00Z"` {
		t.Fatalf("metric=%#v", metric)
	}
	source := payload.Processors[4]["source"].Value
	if string(source["source_id"]) != `"built_in_bk"` || string(source["source_name"]) != `"鲸眼监控"` ||
		string(source["meta_info"]) != `"source-event-1"` {
		t.Fatalf("source=%#v", source)
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type discardLogger struct{}

func (discardLogger) WarnContext(context.Context, string, ...any) {}

type baseCollectCWStrategy struct{}

func (baseCollectCWStrategy) GetByBKStrategyID(
	ctx context.Context,
	strategyID int64,
) (models.CWStrategy, bool, error) {
	strategy, found, err := (&datasources.MockCWStrategyClient{}).GetByBKStrategyID(ctx, strategyID)
	if err != nil || !found {
		return strategy, found, err
	}
	strategy.Spec.StrategyItem = &models.CWStrategyItem{
		AggregateMethod: "avg", AggregatePeriod: json.RawMessage(`60`), Functions: json.RawMessage(`[]`),
	}
	return strategy, true, nil
}

func (baseCollectCWStrategy) GetByMonitorTemplateID(
	ctx context.Context,
	monitorTemplateID int64,
) (models.CWStrategy, bool, error) {
	strategy, found, err := (&datasources.MockCWStrategyClient{}).GetByMonitorTemplateID(ctx, monitorTemplateID)
	if err != nil || !found {
		return strategy, found, err
	}
	strategy.Spec.StrategyItem = &models.CWStrategyItem{
		AggregateMethod: "avg", AggregatePeriod: json.RawMessage(`60`), Functions: json.RawMessage(`[]`),
	}
	return strategy, true, nil
}

type baseCollectMetric struct{}

func (baseCollectMetric) FindMetricLibrary(
	ctx context.Context,
	query models.MetricLibraryQuery,
) (models.MetricMetadata, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.MetricMetadata{}, false, err
	}
	if query.TenantID != datasources.SampleTenantID || query.TableID != "system.cpu" || query.FieldName != "usage" ||
		(query.ObjectModelCode != "" && query.ObjectModelCode != "cw-Host") {
		return models.MetricMetadata{}, false, nil
	}
	return models.MetricMetadata{
		FieldCNName: "CPU 使用率", Unit: "percent",
		Dimensions: []models.MetricDimension{{Key: "bk_target_ip", Name: "目标IP"}, {Key: "bk_target_cloud_id", Name: "云区域"}},
	}, true, nil
}

type baseCollectAlarmSource struct{}

func (baseCollectAlarmSource) GetAlarmSourceName(
	ctx context.Context,
	tenantID, sourceID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if tenantID != datasources.SampleTenantID || sourceID != "built_in_bk" {
		return "", false, nil
	}
	return "鲸眼监控", true, nil
}

type baseCollectOneModel struct{}

func (baseCollectOneModel) FindInstance(
	ctx context.Context,
	tenantID string,
	query enrich.InstanceQuery,
) (enrich.Instance, bool, error) {
	if err := ctx.Err(); err != nil {
		return enrich.Instance{}, false, err
	}
	if tenantID != datasources.SampleTenantID || query.ModelCode != "cw-Host" ||
		query.Filters["cw_object_model_inst_id"] != "101" {
		return enrich.Instance{}, false, nil
	}
	return enrich.Instance{
		TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: "101",
		Fields: map[string]any{
			"bk_tenant_id": tenantID, "cw_object_model_code": query.ModelCode,
			"cw_object_model_inst_id": "101", "bk_obj_id": "host", "bk_host_id": int64(101),
			"bk_biz_id": int64(2), "bk_biz_name": "业务 2", "bk_host_innerip": "10.0.0.1",
			"bk_cloud_id": int64(0), "bk_cloud_name": "默认区域",
		},
	}, true, nil
}

type sampleMetric struct{}

func (sampleMetric) FindMetricLibrary(
	ctx context.Context,
	query models.MetricLibraryQuery,
) (models.MetricMetadata, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.MetricMetadata{}, false, err
	}
	if query.TenantID != datasources.SampleTenantID || query.TableID != "system.cpu" ||
		query.FieldName != "usage" || query.ObjectModelCode != "cw-Host" {
		return models.MetricMetadata{}, false, nil
	}
	return models.MetricMetadata{FieldCNName: "CPU 使用率"}, true, nil
}

type sampleAlarmSource struct{}

func (sampleAlarmSource) GetAlarmSourceName(
	ctx context.Context,
	tenantID, sourceID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if tenantID != datasources.SampleTenantID || sourceID != "built_in_bk" {
		return "", false, nil
	}
	return "鲸眼监控", true, nil
}

type sampleOneModel struct{}

func (sampleOneModel) FindInstance(
	ctx context.Context,
	tenantID string,
	query enrich.InstanceQuery,
) (enrich.Instance, bool, error) {
	if err := ctx.Err(); err != nil {
		return enrich.Instance{}, false, err
	}
	if tenantID != datasources.SampleTenantID || query.ModelCode != "cw-Host" || query.Filters["cw_object_model_inst_id"] != "101" {
		return enrich.Instance{}, false, nil
	}
	return enrich.Instance{
		TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: "101",
		Fields: map[string]any{
			"bk_tenant_id": tenantID, "cw_object_model_code": query.ModelCode,
			"cw_object_model_inst_id": "101", "bk_obj_id": "host", "bk_host_id": int64(101),
			"bk_biz_id": datasources.SampleBizID,
		},
	}, true, nil
}

type panicSources struct{}

func (panicSources) GetStrategyHistory(context.Context, int64, int64) (models.BkStrategyHistory, bool, error) {
	panic("data source called")
}
func (panicSources) GetStrategy(context.Context, int64) (models.BkStrategy, bool, error) {
	panic("data source called")
}
func (panicSources) GetByBKStrategyID(context.Context, int64) (models.CWStrategy, bool, error) {
	panic("data source called")
}
func (panicSources) GetByMonitorTemplateID(context.Context, int64) (models.CWStrategy, bool, error) {
	panic("data source called")
}
func (panicSources) GetAlarmSourceName(context.Context, string, string) (string, bool, error) {
	panic("data source called")
}
func (panicSources) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	panic("data source called")
}
func (panicSources) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	panic("data source called")
}
func (p panicSources) Sources() enrich.Sources {
	return enrich.Sources{BKStrategy: p, CWStrategy: p, Metric: p, OneModel: p, AlarmSource: p}
}

func baseCollectAlert(source string) domain.Alert {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hostID, _ := domain.NewNumberScalar(101)
	strategyID, _ := domain.NewNumberScalar(float64(datasources.SampleStrategyID))
	historyID, _ := domain.NewNumberScalar(float64(datasources.SampleHistoryID))
	bizID, _ := domain.NewNumberScalar(float64(datasources.SampleBizID))
	return domain.Alert{
		AlertID: "alert-1", BKTenantID: datasources.SampleTenantID, EventSourceID: source, Fingerprint: "fp",
		Title: "CPU high", Content: "CPU usage is high", Severity: "warning", SubjectName: "host-101",
		SourceEventID: "source-event-1", Dimensions: domain.DimensionMap{"bk_inst_id": hostID},
		Labels:    domain.DimensionMap{"bk_strategy_id": strategyID, "bk_strategy_history_id": historyID, "bk_biz_id": bizID},
		ExtraData: domain.JSONObject{"sample": json.RawMessage(`true`)}, Status: domain.AlertStatusActive,
		LatestEventID: "event-1", TriggerEventID: "event-1", LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}
