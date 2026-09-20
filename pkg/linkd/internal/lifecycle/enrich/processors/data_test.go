// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestDATAFailureMatrix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		reader     dataSliceReader
		dimensions domain.DimensionMap
		wantDep    string
		wantCode   enrich.DiagnosticCode
		wantStatus domain.EnrichStatus
	}{
		{name: "strategy missing", reader: dataSliceReader{strategyMissing: true}, wantDep: rules.DependencyKingeyeStrategy, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		{name: "strategy error", reader: dataSliceReader{strategyErr: errors.New("strategy unavailable")}, wantDep: rules.DependencyKingeyeStrategy, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		{name: "metric missing", reader: dataSliceReader{model: "cw-Others", metricMissing: true}, wantDep: rules.DependencyMetricLibrary, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		{name: "metric error", reader: dataSliceReader{model: "cw-Others", metricErr: errors.New("metric unavailable")}, wantDep: rules.DependencyMetricLibrary, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		{name: "model missing", reader: dataSliceReader{model: "cw-MySQL", modelMissing: true}, dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-MySQL"), rules.FieldModelInstID: domain.NewStringScalar("17")}, wantDep: rules.DependencyOneModel, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		{name: "instance error", reader: dataSliceReader{model: "cw-MySQL", instanceErr: errors.New("instance unavailable")}, dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-MySQL"), rules.FieldModelInstID: domain.NewStringScalar("17")}, wantDep: rules.DependencyOneModel, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		{name: "invalid identity", reader: dataSliceReader{model: "cw-MySQL"}, dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-Redis"), rules.FieldModelInstID: dataNumber(t, 17)}, wantCode: enrich.DiagnosticCodeInvalidField, wantStatus: domain.EnrichStatusPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := tc.reader
			if reader.model == "" {
				reader.model = "cw-Others"
			}
			alert := processorBaseTargetAlert(t, tc.dimensions)
			original := alert.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: &reader, Metric: &reader, Model: &reader, OneModel: &reader, AlarmSource: &reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			resource := payload.Processors[1][rules.ResourceProcessor]
			if result.Status != tc.wantStatus {
				t.Fatalf("status=%q resource=%#v", result.Status, resource)
			}
			if len(resource.Diagnostics) == 0 || resource.Diagnostics[len(resource.Diagnostics)-1].Code != tc.wantCode || (tc.wantDep != "" && resource.Diagnostics[len(resource.Diagnostics)-1].Dependency != tc.wantDep) {
				t.Fatalf("diagnostics=%#v", resource.Diagnostics)
			}
			if !reflect.DeepEqual(alert, original) {
				t.Fatal("Alert changed")
			}
		})
	}
}

func TestDATACancelledDependencyDoesNotBecomeCachedFailure(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{model: "cw-Others", cancelReads: true}
	scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Resource{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestDATAFunctionMetricPreservesFunctionsAndDisablesEnumMapping(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{model: "cw-MySQL", metricValueMapping: []models.MetricValueMapping{{OriginalValue: "1", MappedValue: "正常"}}}
	strategy := reader.strategyForTest()
	strategy.Spec.StrategyItem.Functions = json.RawMessage(`[ {"id":"rate","params":[{"id":"window","value":"5m"}]} ]`)
	strategy.Spec.StrategyItem.QueryConfigs[0].Functions = json.RawMessage(`[ {"id":"alias","params":[{"id":"field","value":"usage"}]} ]`)
	reader.strategy = strategy
	alert := processorBaseTargetAlert(t, domain.DimensionMap{})
	alert.Content = "rate(usage) >= 1, current value is 1"
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	metric := payload.Processors[3][rules.MetricProcessor]
	var values models.MetricValues
	if err := json.Unmarshal(mustRawObject(t, metric.Value), &values); err != nil {
		t.Fatal(err)
	}
	if values.MetricName != "" {
		t.Fatalf("metric name=%q", values.MetricName)
	}
	params, ok := values.MetricQueryParams.(map[string]any)
	if !ok {
		t.Fatalf("params=%#v", values.MetricQueryParams)
	}
	if _, ok := params["functions"].([]any); !ok {
		t.Fatalf("strategy functions=%#v", params["functions"])
	}
	queries := params["query_configs"].([]any)
	query := queries[0].(map[string]any)
	if _, ok := query["functions"].([]any); !ok {
		t.Fatalf("query functions=%#v", query["functions"])
	}
	var displayContent string
	if err := json.Unmarshal(payload.Processors[2][rules.DisplayProcessor].Value["content"], &displayContent); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(displayContent, "正常") {
		t.Fatalf("function content mapped=%q", displayContent)
	}
}

func TestDATAPrometheusProjectionUsesPromQLIdentity(t *testing.T) {
	t.Parallel()
	query := models.StrategyQueryConfig{DataSourceLabel: rules.DataSourcePrometheus, DataTypeLabel: "time_series", PromQL: "bkmonitor:node:system_cpu_usage", ResultTableID: "ignored", MetricField: "ignored", AggregateMethod: "avg"}
	params := buildMetricQueryParams(models.StrategyItemProjection{BKBizID: 2, QueryConfigs: []models.StrategyQueryConfig{query}}, models.CWStrategy{}, domain.DimensionMap{}, 2)
	got := params.QueryConfigs[0]
	if got.Table != "node" || got.Metrics[0].Field != "system_cpu_usage" || got.PromQL != query.PromQL {
		t.Fatalf("query=%#v", got)
	}
}

func TestDATASpecialQueryTypesUseKACMetricProjection(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{rules.FieldBKObjID: domain.NewStringScalar("host"), rules.FieldBKInstID: domain.NewStringScalar("101"), rules.FieldEventName: domain.NewStringScalar("deploy"), rules.FieldBKBizID: dataNumber(t, 2)}
	for _, tc := range []struct {
		name   string
		query  models.StrategyQueryConfig
		field  string
		table  string
		filter string
	}{
		{name: "custom event", query: models.StrategyQueryConfig{DataSourceLabel: rules.DataSourceCustom, DataTypeLabel: rules.DataTypeEvent, CustomEventName: "deploy", ResultTableID: "custom.table", MetricField: "latency"}, field: rules.MetricFieldIndex, table: "custom.table"},
		{name: "alert", query: models.StrategyQueryConfig{DataSourceLabel: rules.DataSourceBKMonitor, DataTypeLabel: rules.DataTypeAlert, BKStrategyID: 73, ResultTableID: "monitor.alert", MetricField: "latency"}, field: "73", table: rules.ResultTableAlert},
		{name: "fta", query: models.StrategyQueryConfig{DataSourceLabel: rules.DataSourceBKFTA, DataTypeLabel: rules.DataTypeAlert, AlertName: "disk_full", ResultTableID: "fta.alert", MetricField: "latency"}, field: "disk_full", table: rules.ResultTableEvent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projection := models.StrategyItemProjection{BKBizID: 2, Expression: "A", QueryConfigs: []models.StrategyQueryConfig{tc.query}}
			params := buildMetricQueryParams(projection, models.CWStrategy{}, dimensions, 2)
			query := params.QueryConfigs[0]
			if query.Metrics[0].Field != tc.field || query.Table != tc.table || query.BKBizID != 2 {
				t.Fatalf("query=%#v", query)
			}
			if tc.name == "custom event" && query.FilterDict[rules.FieldEventName] != "deploy" {
				t.Fatalf("filter=%#v", query.FilterDict)
			}
		})
	}
}

func TestDATAAlgorithmContentKeepsRawWhenNoMapping(t *testing.T) {
	t.Parallel()
	mapping := []models.MetricValueMapping{{OriginalValue: "50", MappedValue: "中"}}
	for _, tc := range []struct{ name, content, want string }{
		{name: "ring", content: "较前一时刻 50, 当前值 50", want: "较前一时刻 50(中), 当前值 50"},
		{name: "year", content: "上周同一时刻 50, 当前值 50", want: "上周同一时刻 50(中), 当前值 50"},
		{name: "no data", content: "当前指标已经有50个周期无数据上报", want: "当前指标已经有50个周期无数据上报"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := enrichDataAlgorithmContent(tc.content, mapping); got != tc.want {
				t.Fatalf("content=%q", got)
			}
		})
	}
}

func TestDATAUnitAlgorithmUsesSeverityOverride(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{model: "cw-MySQL", metricUnit: "percent"}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{})
	alert.Content = "AVG(usage) >= 80, current value is 90"
	strategy := reader.strategyForTest()
	strategy.Spec.StrategyDetectAlgorithms = []models.CWStrategyDetectAlgorithm{{LevelStatus: "warning", AlgorithmConfig: domain.JSONObject{"algorithmUnit": json.RawMessage(`"ms"`)}}}
	reader.strategy = strategy
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(payload.Processors[2][rules.DisplayProcessor].Value["content"], &got); err != nil {
		t.Fatal(err)
	}
	if got != "AVG(usage) >= 80ms, current value is 90ms" {
		t.Fatalf("content=%q", got)
	}
}

func TestDATAEnumValueMappingEnrichesDisplayContent(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{
		model:              "cw-MySQL",
		metricValueMapping: []models.MetricValueMapping{{OriginalValue: "1", MappedValue: "正常"}},
	}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{})
	alert.Content = "AVG(status) >= 1, current value is 1"
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{
		CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	display := payload.Processors[2][rules.DisplayProcessor]
	var got string
	if err := json.Unmarshal(display.Value["content"], &got); err != nil {
		t.Fatal(err)
	}
	if got != "AVG(status) >= 1(正常), current value is 1(正常)" {
		t.Fatalf("display content=%s", got)
	}
	if !reflect.DeepEqual(alert.Content, "AVG(status) >= 1, current value is 1") {
		t.Fatal("Alert content changed")
	}
}

func TestDATAHostIdentityAndFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		dimensions domain.DimensionMap
		model      string
		found      bool
		instance   enrich.Instance
		wantID     string
		wantStatus domain.EnrichStatus
		wantQuery  enrich.InstanceQuery
	}{
		{name: "host ID", dimensions: domain.DimensionMap{rules.FieldBKTargetHostID: domain.NewStringScalar("101")}, model: rules.HostModelCode, found: true,
			instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Attributes: map[string]any{rules.FieldBKHostID: int64(101), rules.FieldBKBizID: int64(2)}},
			wantID:   "101", wantStatus: domain.EnrichStatusSucceeded, wantQuery: enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: "101"}},
		{name: "address", dimensions: domain.DimensionMap{rules.FieldBKTargetIP: domain.NewStringScalar("192.0.2.11"), rules.FieldBKTargetCloudID: dataNumber(t, 0)}, model: rules.HostModelCode, found: true,
			instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Attributes: map[string]any{rules.FieldBKHostID: int64(101), rules.FieldBKBizID: int64(2)}},
			wantID:   "101", wantStatus: domain.EnrichStatusSucceeded, wantQuery: enrich.InstanceQuery{ModelCode: rules.HostModelCode, AttributeFilters: []enrich.InstanceAttributeFilter{
				{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: "192.0.2.11"},
				{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: int64(0)},
			}}},
		{name: "missing", dimensions: domain.DimensionMap{}, model: rules.HostModelCode, wantStatus: domain.EnrichStatusPartial},
		{name: "wrong response", dimensions: domain.DimensionMap{rules.FieldBKTargetHostID: domain.NewStringScalar("101")}, model: rules.HostModelCode, found: true,
			instance: enrich.Instance{TenantID: "tenant-b", ModelCode: rules.HostModelCode, InstanceID: "101"}, wantStatus: domain.EnrichStatusPartial, wantQuery: enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: "101"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &dataSliceReader{model: tc.model, instance: tc.instance, found: tc.found}
			alert := processorBaseTargetAlert(t, tc.dimensions)
			original := alert.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{
				CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			resource := payload.Processors[1][rules.ResourceProcessor]
			if result.Status != tc.wantStatus || string(resource.Value["model_inst_id"]) != `"`+tc.wantID+`"` || !reflect.DeepEqual(reader.query, tc.wantQuery) {
				t.Fatalf("status=%q resource=%#v query=%#v", result.Status, resource, reader.query)
			}
			if !reflect.DeepEqual(alert, original) {
				t.Fatal("Alert changed")
			}
		})
	}
}

func TestDATAOrdinaryModelUsesCanonicalIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		dimensions  domain.DimensionMap
		instance    enrich.Instance
		found       bool
		wantID      string
		wantCode    enrich.DiagnosticCode
		wantQuery   enrich.InstanceQuery
		wantPartial bool
	}{
		{name: "opaque instance", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-MySQL"), rules.FieldModelInstID: domain.NewStringScalar("svc|opaque-17")},
			instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-MySQL", InstanceID: "svc|opaque-17", Attributes: map[string]any{rules.FieldBKInstID: int64(17), rules.FieldBKBizID: int64(2)}}, found: true,
			wantID: "svc|opaque-17", wantQuery: enrich.InstanceQuery{ModelCode: "cw-MySQL", InstanceID: "svc|opaque-17"}},
		{name: "wrong model", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-Redis"), rules.FieldModelInstID: domain.NewStringScalar("17")}, wantCode: enrich.DiagnosticCodeInvalidField, wantPartial: true},
		{name: "numeric identity", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-MySQL"), rules.FieldModelInstID: dataNumber(t, 17)}, wantCode: enrich.DiagnosticCodeInvalidField, wantPartial: true},
		{name: "not found", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-MySQL"), rules.FieldModelInstID: domain.NewStringScalar("17")}, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantQuery: enrich.InstanceQuery{ModelCode: "cw-MySQL", InstanceID: "17"}, wantPartial: true},
		{name: "response mismatch", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-MySQL"), rules.FieldModelInstID: domain.NewStringScalar("17")}, instance: enrich.Instance{TenantID: "tenant-b", ModelCode: "cw-MySQL", InstanceID: "17"}, found: true, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantQuery: enrich.InstanceQuery{ModelCode: "cw-MySQL", InstanceID: "17"}, wantPartial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &dataSliceReader{model: "cw-MySQL", instance: tc.instance, found: tc.found}
			alert := processorBaseTargetAlert(t, tc.dimensions)
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			resource := payload.Processors[1][rules.ResourceProcessor]
			if string(resource.Value["model_inst_id"]) != `"`+tc.wantID+`"` || !reflect.DeepEqual(reader.query, tc.wantQuery) {
				t.Fatalf("resource=%#v query=%#v", resource, reader.query)
			}
			if tc.wantPartial {
				if result.Status != domain.EnrichStatusPartial || resource.Status != domain.EnrichStatusFailed || len(resource.Diagnostics) == 0 || resource.Diagnostics[len(resource.Diagnostics)-1].Code != tc.wantCode || string(resource.Value["cw_labels"]) != `["bk_biz_id","bk_biz_id|2"]` {
					t.Fatalf("status=%q resource=%#v", result.Status, resource)
				}
			} else if result.Status != domain.EnrichStatusSucceeded || resource.Status != domain.EnrichStatusSucceeded || string(resource.Value["cw_labels"]) != `["bk_biz_id","bk_biz_id|2","cw-MySQL","cw-MySQL|svc|opaque-17"]` {
				t.Fatalf("status=%q resource=%#v", result.Status, resource)
			}
		})
	}
}

func TestDATAHardwareAndMultiModelUseCanonicalIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		table      string
		dimensions domain.DimensionMap
		wantCode   enrich.DiagnosticCode
		wantModel  string
		wantID     string
	}{
		{name: "hardware canonical", table: "hardware_disk", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-Disk"), rules.FieldModelInstID: domain.NewStringScalar("disk|17")}, wantModel: "cw-Disk", wantID: "disk|17"},
		{name: "multi model canonical", table: "plugin.metrics", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-Redis"), rules.FieldModelInstID: domain.NewStringScalar("redis|17")}, wantModel: "cw-Redis", wantID: "redis|17"},
		{name: "hardware legacy only", table: "hardware_disk", dimensions: domain.DimensionMap{rules.FieldLegacyHardwareModelID: dataNumber(t, 9), rules.FieldLegacyHardwareModelInstID: dataNumber(t, 17)}, wantCode: enrich.DiagnosticCodeMissingField},
		{name: "multi model legacy only", table: "plugin.metrics", dimensions: domain.DimensionMap{rules.FieldLegacyCWModelID: dataNumber(t, 9), rules.FieldLegacyCWModelInstID: dataNumber(t, 17)}, wantCode: enrich.DiagnosticCodeMissingField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &dataSliceReader{model: tc.wantModel, table: tc.table, found: tc.wantID != "", instance: enrich.Instance{TenantID: "tenant-a", ModelCode: tc.wantModel, InstanceID: tc.wantID, Attributes: map[string]any{rules.FieldBKInstID: int64(17), rules.FieldBKBizID: int64(2)}}}
			if tc.wantModel == "" {
				reader.model = "cw-Unknown"
			}
			alert := processorBaseTargetAlert(t, tc.dimensions)
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			resource := payload.Processors[1][rules.ResourceProcessor]
			if tc.wantID != "" {
				if result.Status != domain.EnrichStatusSucceeded || reader.query.ModelCode != tc.wantModel || reader.query.InstanceID != tc.wantID || string(resource.Value["model_inst_id"]) != `"`+tc.wantID+`"` {
					t.Fatalf("status=%q query=%#v resource=%#v", result.Status, reader.query, resource)
				}
			} else if result.Status != domain.EnrichStatusPartial || len(resource.Diagnostics) == 0 || resource.Diagnostics[len(resource.Diagnostics)-1].Code != tc.wantCode || reader.query.ModelCode != "" {
				t.Fatalf("status=%q query=%#v resource=%#v", result.Status, reader.query, resource)
			}
		})
	}
}

func TestDATAOrdinaryModelKeepsPartialUntilIdentityConfirmed(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{model: "cw-MySQL"}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{})
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[1][rules.ResourceProcessor]
	if result.Status != domain.EnrichStatusPartial || resource.Status != domain.EnrichStatusPartial || string(resource.Value["model_id"]) != `"cw-MySQL"` || string(resource.Value["model_inst_id"]) != `""` ||
		len(resource.Diagnostics) != 1 || resource.Diagnostics[0].Code != enrich.DiagnosticCodeMissingField || reader.query.ModelCode != "" {
		t.Fatalf("status=%q resource=%#v query=%#v", result.Status, resource, reader.query)
	}
}

func TestDATAUptimeUsesTaskID(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{model: rules.UptimeModelCode, task: models.UptimeTask{ID: 55, TaskID: 10079, BKBizID: 2, Name: "拨测任务", BKTenantID: "tenant-a", Protocol: models.UptimeProtocolICMP}}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldTaskID: domain.NewStringScalar("10079"), rules.FieldTarget: domain.NewStringScalar("example.com")})
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, Uptime: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	resource, display := payload.Processors[1][rules.ResourceProcessor], payload.Processors[2][rules.DisplayProcessor]
	if result.Status != domain.EnrichStatusSucceeded || string(resource.Value["model_id"]) != `"cw-web_service"` || string(resource.Value["model_inst_id"]) != `"55"` || string(display.Value["object"]) != `"拨测任务"` {
		t.Fatalf("resource=%#v display=%#v status=%q", resource, display, result.Status)
	}
}

func derivedTag(enabled bool) models.CWStrategyFieldTag {
	if enabled {
		return models.CWStrategyFieldTagDerivedMetric
	}
	return ""
}

func TestDataDerivedAndMultiMetricQuerySemantics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		reader     dataSliceReader
		wantName   string
		wantUnique string
		wantField  string
		wantTag    models.CWStrategyFieldTag
	}{
		{name: "derived", reader: dataSliceReader{model: "cw-MySQL", derived: true}, wantName: "derived_usage", wantUnique: "system.cpu.derived_usage", wantField: "derived_usage", wantTag: models.CWStrategyFieldTagDerivedMetric},
		{name: "multi", reader: dataSliceReader{model: "cw-MySQL", multi: true}, wantName: "", wantUnique: "system.cpu.requests", wantField: "requests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := tc.reader
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Display{}, Metric{}}, enrich.Sources{CWStrategy: &reader, Metric: &reader, Model: &reader, OneModel: &reader, AlarmSource: &reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			metric := payload.Processors[2][rules.MetricProcessor]
			var values models.MetricValues
			if err := json.Unmarshal(mustRawObject(t, metric.Value), &values); err != nil {
				t.Fatal(err)
			}
			if values.MetricName != tc.wantName || values.MetricUniqueID != tc.wantUnique {
				t.Fatalf("metric=%#v", values)
			}
			params, ok := values.MetricQueryParams.(map[string]any)
			if !ok {
				t.Fatalf("params=%#v", values.MetricQueryParams)
			}
			queries := params["query_configs"].([]any)
			if len(queries) != func() int {
				if tc.reader.multi {
					return 2
				}
				return 1
			}() {
				t.Fatalf("queries=%#v", queries)
			}
			if tc.wantTag != "" && params["field_tag"] != string(tc.wantTag) {
				t.Fatalf("params=%#v", params)
			}
		})
	}
}

func mustRawObject(t *testing.T, value domain.JSONObject) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func dataNumber(t *testing.T, value float64) domain.Scalar {
	t.Helper()
	n, err := domain.NewNumberScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type dataSliceReader struct {
	strategyMissing    bool
	strategyErr        error
	metricMissing      bool
	metricErr          error
	modelMissing       bool
	modelErr           error
	instanceErr        error
	cancelReads        bool
	model              string
	table              string
	derived            bool
	multi              bool
	instance           enrich.Instance
	found              bool
	query              enrich.InstanceQuery
	metricQuery        models.MetricLibraryQuery
	metricValueMapping []models.MetricValueMapping
	metricUnit         string
	strategy           models.CWStrategy
	task               models.UptimeTask
}

func (r *dataSliceReader) strategyForTest() models.CWStrategy {
	biz := int64(2)
	return models.CWStrategy{BKBizID: &biz, ObjectModelCode: optionalModelCode(r.model), Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, Name: "CPU", TableID: "system.cpu", FieldName: "usage", StrategyItem: &models.CWStrategyItem{Expression: "A", QueryConfigs: []models.StrategyQueryConfig{{ResultTableID: "system.cpu", MetricField: "usage"}}}}}
}

func (r *dataSliceReader) GetByBKStrategyID(ctx context.Context, _ string, _ int64) (models.CWStrategy, bool, error) {
	if r.cancelReads {
		return models.CWStrategy{}, false, ctx.Err()
	}
	if r.strategyErr != nil {
		return models.CWStrategy{}, false, r.strategyErr
	}
	if r.strategyMissing {
		return models.CWStrategy{}, false, nil
	}
	if r.strategy.Spec.StrategyItem != nil {
		return r.strategy, true, nil
	}
	biz := int64(2)
	table := r.table
	if table == "" {
		table = "system.cpu"
	}
	if r.model == rules.UptimeModelCode {
		table = "uptimecheck.icmp"
	}
	query := models.StrategyQueryConfig{ResultTableID: table, MetricField: "usage"}
	if r.derived {
		query.MetricField = "derived_usage"
	}
	if r.multi {
		query = models.StrategyQueryConfig{Alias: "A", ResultTableID: table, MetricField: "requests", AggregateMethod: "avg"}
		second := models.StrategyQueryConfig{Alias: "B", ResultTableID: table, MetricField: "errors", AggregateMethod: "sum"}
		return models.CWStrategy{BKBizID: &biz, ObjectModelCode: optionalModelCode(r.model), Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, Name: "多指标", TableID: table, FieldName: "requests", StrategyItem: &models.CWStrategyItem{Expression: "A / B", QueryConfigs: []models.StrategyQueryConfig{query, second}}}}, true, nil
	}
	return models.CWStrategy{BKBizID: &biz, ObjectModelCode: optionalModelCode(r.model), Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, Name: "CPU", TableID: table, FieldName: query.MetricField, FieldTag: derivedTag(r.derived), StrategyItem: &models.CWStrategyItem{Expression: "A", QueryConfigs: []models.StrategyQueryConfig{query}}}}, true, nil
}

func (r *dataSliceReader) FindMetricLibrary(ctx context.Context, query models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	if r.cancelReads {
		return models.MetricMetadata{}, false, ctx.Err()
	}
	r.metricQuery = query
	if r.metricErr != nil {
		return models.MetricMetadata{}, false, r.metricErr
	}
	if r.metricMissing {
		return models.MetricMetadata{}, false, nil
	}
	return models.MetricMetadata{ObjectModelCode: r.model, FieldName: "usage", Unit: r.metricUnit, ValueMapping: r.metricValueMapping}, true, nil
}
func (r *dataSliceReader) GetModelByCode(ctx context.Context, tenant, code string) (enrich.Model, bool, error) {
	if r.cancelReads {
		return enrich.Model{}, false, ctx.Err()
	}
	if r.modelErr != nil {
		return enrich.Model{}, false, r.modelErr
	}
	if r.modelMissing {
		return enrich.Model{}, false, nil
	}
	return enrich.Model{TenantID: tenant, ModelID: "1", ModelCode: code, Fields: map[string]any{rules.FieldObjectModelName: "主机"}}, true, nil
}
func (r *dataSliceReader) FindInstance(ctx context.Context, _ string, q enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if r.cancelReads {
		return enrich.Instance{}, false, ctx.Err()
	}
	if q.ModelCode == "cw-biz" {
		return enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{rules.FieldBKBizName: "业务"}}, true, nil
	}
	r.query = q
	if r.instanceErr != nil {
		return enrich.Instance{}, false, r.instanceErr
	}
	return r.instance, r.found, nil
}

func (r *dataSliceReader) GetUptimeTask(_ context.Context, _, id string) (models.UptimeTask, bool, error) {
	if id != "10079" {
		return models.UptimeTask{}, false, errors.New("unexpected task ID")
	}
	return r.task, true, nil
}
func (*dataSliceReader) GetAlarmSourceName(context.Context, string, string) (string, bool, error) {
	return "鲸眼监控", true, nil
}
