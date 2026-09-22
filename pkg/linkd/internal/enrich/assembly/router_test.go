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
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/processors"
	"linkd/internal/enrich/rules"
	"linkd/internal/lifecycle"
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
	if event.BKTenantID != datasources.SampleTenantID || event.EventSourceID != source.EventSourceID ||
		event.SourceEventID != "source-event-1" || event.SourceAlertID != "source-alert-1" ||
		event.SubjectName != "host-101" ||
		!event.OccurredAt.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) ||
		!event.ProducedAt.Equal(time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)) ||
		!event.ReceivedAt.Equal(time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC)) {
		t.Fatalf("event=%#v", event)
	}
	strategyID, strategyIDOK := event.Labels["strategy_id"].NumberValue()
	strategyVersion, strategyVersionOK := event.Labels["strategy_version"].NumberValue()
	bizID, bizIDOK := event.Labels["bk_biz_id"].NumberValue()
	if !strategyIDOK || strategyID != 123 || !strategyVersionOK || strategyVersion != 1 || !bizIDOK || bizID != 2 ||
		string(event.ExtraData["additional_dimensions"]) != `{"bk_host_id":101}` ||
		string(event.ExtraData["anomaly_begin_time"]) != `"2026-09-01T00:00:00Z"` {
		t.Fatalf("event labels=%#v extra_data=%#v", event.Labels, event.ExtraData)
	}

	dataSources := (&datasources.Mock{}).Sources()
	dataSources.CWStrategy = baseCollectCWStrategy{}
	dataSources.OneModel = baseCollectOneModel{}
	dataSources.Model = baseCollectModel{}
	dataSources.CollectTopology = baseCollectTopology{}
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
		nil, config.DefaultSeverityConfig(), fixedClock{now: time.Date(2026, 9, 1, 0, 0, 3, 0, time.UTC)}, discardLogger{},
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessEvent(ctx, storedEvent.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	storedAlert, err := repository.GetAlert(ctx, datasources.SampleTenantID, processed.AlertIDs[len(processed.AlertIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	alert := storedAlert.Alert
	if !alert.LastOccurredAt.Equal(event.OccurredAt) || !alert.BeginAt.Equal(event.OccurredAt) ||
		!alert.CreateAt.Equal(event.CreateAt) || string(alert.ExtraData["additional_dimensions"]) != `{"bk_host_id":101}` {
		t.Fatalf("alert did not inherit opening event facts: alert=%#v event=%#v", alert, event)
	}
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

// TestStandardNoDataMarkerReachesEnrich 固定标准事件到 Alert 的分类链路；Kingeye 原始
// event.tags 的映射属于上游契约，此测试仅验证已经投影为标准 dimensions 的标记。
func TestStandardNoDataMarkerReachesEnrich(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name               string
		markerInDimensions bool
		wantBranch         rules.BaseTargetBranch
	}{
		{name: "standard dimension", markerInDimensions: true, wantBranch: rules.BaseTargetNoData},
		{name: "source tag only", wantBranch: rules.BaseTargetSystemMetric},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var payload map[string]any
			if err := json.Unmarshal(baseCollectRawPayload(), &payload); err != nil {
				t.Fatal(err)
			}
			dimensions := map[string]any{"model_id": rules.HostModelCode, "model_inst_id": 101, "bk_biz_id": 2}
			if test.markerInDimensions {
				dimensions[rules.FieldNoDataDimension] = true
			} else {
				payload["event"] = map[string]any{"tags": []any{map[string]any{"key": rules.FieldNoDataDimension, "value": true}}}
			}
			payload["dimensions"] = dimensions
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			source := baseCollectEventSource()
			mapper, err := cleaner.NewMapper(source, config.DefaultSeverityConfig())
			if err != nil {
				t.Fatal(err)
			}
			event, err := mapper.MapMessage(ctx, consume.Message{
				ID: "no-data-fixture", TenantID: datasources.SampleTenantID,
				EnqueuedAt: time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC),
				Body:       body, Headers: map[string][]byte{"bk_tenant_id": []byte(datasources.SampleTenantID)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := rules.Classify(models.CWStrategy{ObjectModelCode: pointerTo(rules.HostModelCode)}, event.Dimensions).BaseTarget; got != test.wantBranch {
				t.Fatalf("event classification=%q want=%q", got, test.wantBranch)
			}
			dataSources := (&datasources.Mock{}).Sources()
			dataSources.CWStrategy = baseCollectCWStrategy{}
			dataSources.OneModel = baseCollectOneModel{}
			dataSources.Model = baseCollectModel{}
			dataSources.CollectTopology = baseCollectTopology{}
			dataSources.Metric = baseCollectMetric{}
			dataSources.AlarmSource = baseCollectAlarmSource{}
			router, err := NewRouter([]config.EventSource{source}, dataSources)
			if err != nil {
				t.Fatal(err)
			}
			repository := memory.New()
			stored, err := repository.CreateEvent(ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			processor, err := lifecycle.NewProcessor(repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router,
				nil, config.DefaultSeverityConfig(), fixedClock{now: time.Date(2026, 9, 1, 0, 0, 3, 0, time.UTC)}, discardLogger{})
			if err != nil {
				t.Fatal(err)
			}
			processed, err := processor.ProcessEvent(ctx, stored.StoredEvent)
			if err != nil {
				t.Fatal(err)
			}
			if len(processed.AlertIDs) == 0 {
				t.Fatal("processed alert IDs are empty")
			}
			storedAlert, err := repository.GetAlert(ctx, datasources.SampleTenantID, processed.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			alert := storedAlert.Alert
			if _, ok := alert.Dimensions[rules.FieldNoDataDimension]; ok != test.markerInDimensions {
				t.Fatalf("alert marker present=%t, want=%t", ok, test.markerInDimensions)
			}
			if got := rules.Classify(models.CWStrategy{ObjectModelCode: pointerTo(rules.HostModelCode)}, alert.Dimensions).BaseTarget; got != test.wantBranch {
				t.Fatalf("alert classification=%q want=%q", got, test.wantBranch)
			}
			results, err := enrich.DecodePayload(alert.Enrich)
			if err != nil {
				t.Fatal(err)
			}
			resource := results.Processors[1][rules.ResourceProcessor]
			if test.markerInDimensions {
				if resource.Status != domain.EnrichStatusSucceeded || string(resource.Value["model_inst_id"]) != `"101"` {
					t.Fatalf("resource=%#v", resource)
				}
			} else if resource.Status != domain.EnrichStatusFailed || string(resource.Value["model_inst_id"]) != `""` {
				t.Fatalf("unmapped source tag changed resource identity: %#v", resource)
			}
		})
	}
}

func pointerTo(value string) *string { return &value }

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
	dataSources.Model = baseCollectModel{}
	dataSources.CollectTopology = baseCollectTopology{}
	dataSources.Metric = sampleMetric{}
	dataSources.AlarmSource = sampleAlarmSource{}
	router, err := NewRouter(sources, dataSources)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Enrich(context.Background(), enrich.Input{Alert: baseCollectAlert("built_in_bk")})
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
	if string(strategy["strategy_id"]) != "123" || string(strategy["strategy_version"]) != "1" ||
		string(strategy["strategy_config_id"]) != `"strategyconfig1"` ||
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
	noop, err := router.Enrich(context.Background(), enrich.Input{Alert: baseCollectAlert("disabled-source")})
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
	if router.EnrichChainKind("configured") != enrich.ChainConfigured ||
		router.EnrichChainKind("noop") != enrich.ChainNoop ||
		router.EnrichChainKind("missing") != enrich.ChainUnknown {
		t.Fatalf("configured=%q noop=%q missing=%q", router.EnrichChainKind("configured"), router.EnrichChainKind("noop"), router.EnrichChainKind("missing"))
	}
}

func TestRouterPassesProcessorConfigToStrategy(t *testing.T) {
	t.Parallel()
	processor, err := newProcessor(config.EnrichProcessorConfig{
		Type:   "strategy",
		Config: map[string]any{"web_saas_module_url": "https://example.com/kingeye/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	strategy, ok := processor.(processors.Strategy)
	if !ok || strategy.WebSaaSModuleURL() != "https://example.com/kingeye/" {
		t.Fatalf("processor=%#v", processor)
	}
}

func TestRouterRejectsUnknownSourceAndProcessor(t *testing.T) {
	t.Parallel()
	router, err := NewRouter([]config.EventSource{{EventSourceID: "known"}}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Enrich(context.Background(), enrich.Input{Alert: baseCollectAlert("unknown")}); err == nil {
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
	delete(alert.Labels, "strategy_id")
	result, err := router.Enrich(context.Background(), enrich.Input{Alert: alert})
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
		Enrich: config.EnrichConfig{DataSources: testEnrichDataSources(), Processors: []config.EnrichProcessorConfig{
			{Type: "strategy"}, {Type: "resource"}, {Type: "display"}, {Type: "metric"}, {Type: "source"},
		}},
		Storage: config.EventSourceStorageConfig{Type: config.StorageTypeKafka, Kafka: config.KafkaStorageConfig{
			Brokers: []string{"127.0.0.1:9092"}, Topic: "linkd-base-collect",
			ConsumerGroup: "linkd-base-collect-cleaner",
		}},
	}.WithDefaults()
}

func testEnrichDataSources() *config.EnrichDataSources {
	return &config.EnrichDataSources{
		MySQL:         &config.EnrichMySQLDataSource{Address: "mysql.example.com:3306", Database: "kingeye", Username: "reader"},
		Elasticsearch: &config.EnrichElasticsearchDataSource{Addresses: []string{"http://onemodel.example.com:9200"}},
	}
}

func baseCollectRawPayload() []byte {
	return []byte(`{"alert_id":"source-alert-1","bk_tenant_id":"tenant-1","content":"Host 10.0.0.1 CPU usage reached 92.5%","dimensions":{"bk_inst_id":101,"bk_target_cloud_id":0,"bk_target_ip":"10.0.0.1"},"evaluations":[{"action":"triggered","action_reason":"","severity":"warning"}],"event_id":"source-event-1","extra_data":{"additional_dimensions":{"bk_host_id":101},"anomaly_begin_time":"2026-09-01T00:00:00Z"},"labels":{"bk_biz_id":2,"strategy_id":123,"strategy_version":1},"occurred_at":"2026-09-01T00:00:00Z","produced_at":"2026-09-01T00:00:01Z","subject":{"id":"101","name":"host-101","system":"cmdb","type":"host"},"title":"CPU usage is high"}`)
}

func assertBaseCollectEnrichment(t *testing.T, payload enrich.Payload) {
	t.Helper()
	strategy := payload.Processors[0]["strategy"].Value
	if string(strategy["strategy_id"]) != "123" || string(strategy["strategy_version"]) != "1" ||
		string(strategy["strategy_config_id"]) != `"strategyconfig1"` {
		t.Fatalf("strategy=%#v", strategy)
	}
	resource := payload.Processors[1]["resource"].Value
	if string(resource["model_id"]) != `"cw-Host"` || string(resource["bk_inst_id"]) != "101" ||
		string(resource["bk_biz_name"]) != `"业务 2"` {
		t.Fatalf("resource=%#v", resource)
	}
	display := payload.Processors[2]["display"].Value
	if string(display["title"]) != `"CPU 使用率过高"` || string(display["object"]) != `"10.0.0.1"` ||
		string(display["dimension_text"]) != `"bk_host_id(101)"` {
		t.Fatalf("display=%#v", display)
	}
	var dimensions []models.DimensionDisplay
	if err := json.Unmarshal(display["dimensions"], &dimensions); err != nil {
		t.Fatal(err)
	}
	if len(dimensions) != 1 || dimensions[0].Name != "bk_host_id" || dimensions[0].RealKey == nil ||
		*dimensions[0].RealKey != "bk_host_id" || dimensions[0].RealValue == nil {
		t.Fatalf("display dimensions=%#v", dimensions)
	}
	metric := payload.Processors[3]["metric"].Value
	if string(metric["display_name"]) != `"CPU 使用率"` || string(metric["metric_name"]) != `"usage"` ||
		string(metric["unit"]) != `"percent"` || string(metric["result_table_id"]) != `"system.cpu"` ||
		string(metric["where_condition"]) != `"bk_inst_id='101' and bk_target_cloud_id='0' and bk_target_ip='10.0.0.1'"` ||
		string(metric["anomaly_begin_time"]) != `"2026-09-01T00:00:00Z"` {
		t.Fatalf("metric=%#v", metric)
	}
	var queryParams struct {
		BKBizID int64 `json:"bk_biz_id"`
	}
	if err := json.Unmarshal(metric["metric_query_params"], &queryParams); err != nil || queryParams.BKBizID != 2 {
		t.Fatalf("metric query params=%s error=%v", metric["metric_query_params"], err)
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
	tenantID string,
	strategyID int64,
) (models.CWStrategy, bool, error) {
	strategy, found, err := (&datasources.MockCWStrategyClient{}).GetByBKStrategyID(ctx, tenantID, strategyID)
	if err != nil || !found {
		return strategy, found, err
	}
	strategy.Spec.StrategyItem.AggregateMethod = "avg"
	strategy.Spec.StrategyItem.AggregatePeriod = json.RawMessage(`60`)
	strategy.Spec.StrategyItem.Functions = json.RawMessage(`[]`)
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

type baseCollectModel struct{}

func (baseCollectModel) GetModelByCode(ctx context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	if err := ctx.Err(); err != nil {
		return enrich.Model{}, false, err
	}
	if tenantID != datasources.SampleTenantID || modelCode != rules.HostModelCode {
		return enrich.Model{}, false, nil
	}
	return enrich.Model{TenantID: tenantID, ModelID: "36", ModelCode: modelCode, Fields: map[string]any{
		rules.FieldObjectModelName: "主机", rules.FieldBKCMDBObjectID: "host",
	}}, true, nil
}

type baseCollectTopology struct{}

func (baseCollectTopology) FindRelatedHost(context.Context, string, string, string, string) (enrich.Instance, bool, error) {
	return enrich.Instance{}, false, nil
}

func (baseCollectTopology) FindHostTopology(ctx context.Context, tenantID, hostID string) (models.ResourceTopology, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.ResourceTopology{}, false, err
	}
	if tenantID != datasources.SampleTenantID || hostID != "101" {
		return models.ResourceTopology{}, false, nil
	}
	return models.ResourceTopology{BKBizID: 2, BKBizName: "业务 2", BKSetID: 3, BKSetName: "集群 3", BKModuleID: 4, BKModuleName: "模块 4"}, true, nil
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
	if tenantID != datasources.SampleTenantID || query.ModelCode != "cw-Host" || query.InstanceID != "101" {
		return enrich.Instance{}, false, nil
	}
	return enrich.Instance{
		TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: "101",
		Fields: map[string]any{
			"bk_tenant_id": tenantID, "model_id": query.ModelCode,
			"model_inst_id": "101", "entity_uid": query.ModelCode + "|101", "bk_biz_ids": []int64{2},
		},
		Attributes: map[string]any{
			"bk_obj_id": "host", "bk_host_id": int64(101), "bk_biz_id": int64(2), "bk_biz_name": "业务 2",
			"bk_host_innerip": "10.0.0.1", "bk_cloud_id": int64(0), "bk_cloud_name": "默认区域",
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
	if tenantID != datasources.SampleTenantID || query.ModelCode != "cw-Host" || query.InstanceID != "101" {
		return enrich.Instance{}, false, nil
	}
	return enrich.Instance{
		TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: "101",
		Fields: map[string]any{
			"bk_tenant_id": tenantID, "model_id": query.ModelCode,
			"model_inst_id": "101", "entity_uid": query.ModelCode + "|101", "bk_biz_ids": []int64{2},
		},
		Attributes: map[string]any{
			"bk_obj_id": "host", "bk_host_id": int64(101), "bk_biz_id": datasources.SampleBizID,
		},
	}, true, nil
}

type panicSources struct{}

func (panicSources) IsGlobalBusiness(context.Context, string, int64) (bool, bool, error) {
	panic("data source called")
}

func (panicSources) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
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
	return enrich.Sources{CWStrategy: p, Business: p, Metric: p, OneModel: p, AlarmSource: p}
}

func baseCollectAlert(source string) domain.Alert {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hostID, _ := domain.NewNumberScalar(101)
	strategyID, _ := domain.NewNumberScalar(float64(datasources.SampleStrategyID))
	strategyVersion, _ := domain.NewNumberScalar(float64(datasources.SampleStrategyVersion))
	bizID, _ := domain.NewNumberScalar(float64(datasources.SampleBizID))
	return domain.Alert{
		EventSourceVersion: 1,
		AlertID:            "alert-1", BKTenantID: datasources.SampleTenantID, EventSourceID: source, Fingerprint: "fp",
		Title: "CPU high", Content: "CPU usage is high", Severity: "warning", SubjectName: "host-101",
		SourceEventID: "source-event-1", Dimensions: domain.DimensionMap{"bk_inst_id": hostID},
		Labels:    domain.DimensionMap{"strategy_id": strategyID, "strategy_version": strategyVersion, "bk_biz_id": bizID},
		ExtraData: domain.JSONObject{"sample": json.RawMessage(`true`)}, Status: domain.AlertStatusActive,
		LatestEventID: "event-1", TriggerEventID: "event-1", LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func TestRouterTestProcessorHasNoDataSourceDependency(t *testing.T) {
	source := config.EventSource{EventSourceID: "test-source", Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "test", Config: map[string]any{"fields": map[string]any{"region": "local", "nested": map[string]any{"enabled": true}}, "datasource": map[string]any{"calls": 2}}}}}}
	if _, err := source.Enrich.SelectDataSources(); err != nil {
		t.Fatal(err)
	}
	sources := panicSources{}.Sources()
	sources.Test = datasources.TestClient{}
	router, err := NewRouter([]config.EventSource{source}, sources)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Enrich(t.Context(), enrich.Input{Alert: baseCollectAlert(source.EventSourceID)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusSucceeded || string(payload.Processors[0]["test"].Value["region"]) != `"local"` {
		t.Fatalf("test enrich=%#v", result)
	}
	source.Enrich.Processors[0].Config["datasource"] = map[string]any{"error_rate": 2}
	if _, err := NewRouter([]config.EventSource{source}, enrich.Sources{}); err == nil {
		t.Fatal("invalid test configuration accepted")
	}
}
