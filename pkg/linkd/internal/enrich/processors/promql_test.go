// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// promQLSnapshotFixture 保存 171/PromQL 模板告警的真实发布快照与同一时刻的
// OneModel、指标目录读取结果，避免用合成表达式证明修复。
type promQLSnapshotFixture struct {
	Name                string `json:"name"`
	KACAlarmID          string `json:"kac_alarm_id"`
	MonitorTemplateName string `json:"monitor_template_name"`
	StrategyRevision    struct {
		TenantID        string `json:"bk_tenant_id"`
		BusinessID      int64  `json:"strategy_bk_biz_id"`
		StrategyID      int64  `json:"strategy_id"`
		StrategyVersion int64  `json:"strategy_revision"`
	} `json:"strategy_revision"`
	RuntimeConfig struct {
		Metadata struct {
			Name   string `json:"name"`
			Labels struct {
				BKBizID           int64  `json:"bk_biz_id"`
				BKTenantID        string `json:"bk_tenant_id"`
				ConfigID          string `json:"config_id"`
				IsDefault         bool   `json:"is_default"`
				MonitorTemplateID int64  `json:"monitor_template_id"`
				ObjectModelCode   string `json:"object_model_code"`
			} `json:"labels"`
		} `json:"metadata"`
		Spec models.CWStrategySpec `json:"spec"`
	} `json:"runtime_config"`
	RuntimeItems []json.RawMessage `json:"runtime_items"`
	Event        struct {
		BKTenantID    string            `json:"bk_tenant_id"`
		EventID       string            `json:"event_id"`
		SourceEventID string            `json:"source_event_id"`
		Title         string            `json:"title"`
		Content       string            `json:"content"`
		Dimensions    map[string]any    `json:"dimensions"`
		ExtraData     domain.JSONObject `json:"extra_data"`
		Labels        map[string]any    `json:"labels"`
		Subject       struct {
			System string `json:"system"`
			Type   string `json:"type"`
			ID     string `json:"id"`
			Name   string `json:"name"`
		} `json:"subject"`
		Evaluations []struct {
			Severity string `json:"severity"`
			Action   string `json:"action"`
		} `json:"evaluations"`
		OccurredAt string `json:"occurred_at"`
		ProducedAt string `json:"produced_at"`
		ReceivedAt string `json:"received_at"`
		CreateAt   string `json:"create_at"`
	} `json:"event"`
	Host struct {
		Model struct {
			ModelID   string         `json:"model_id"`
			ModelCode string         `json:"model_code"`
			Fields    map[string]any `json:"fields"`
		} `json:"model"`
		Instance struct {
			ModelCode  string         `json:"model_code"`
			InstanceID string         `json:"instance_id"`
			Fields     map[string]any `json:"fields"`
			Attributes map[string]any `json:"attributes"`
		} `json:"instance"`
		Topology models.ResourceTopology `json:"topology"`
	} `json:"host"`
	Catalog []struct {
		ID              int64  `json:"id"`
		ObjectModelCode string `json:"model_id"`
		FieldName       string `json:"metric_name"`
		FieldCNName     string `json:"display_name"`
		Kind            string `json:"kind"`
		TableID         string `json:"result_table_id"`
		DataLabel       string `json:"data_label"`
		PhysicalField   string `json:"physical_field"`
		Unit            string `json:"unit"`
	} `json:"catalog"`
}

func loadPromQLSnapshotFixture(t *testing.T) promQLSnapshotFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/promql/promql_host_system_metric.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture promQLSnapshotFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.StrategyRevision.StrategyID == 0 || len(fixture.RuntimeConfig.Spec.StrategyItem.QueryConfigs) == 0 || len(fixture.Catalog) == 0 {
		t.Fatalf("fixture is incomplete: %#v", fixture)
	}
	return fixture
}

func (f promQLSnapshotFixture) strategy() models.CWStrategy {
	labels := f.RuntimeConfig.Metadata.Labels
	bizID, templateID := labels.BKBizID, labels.MonitorTemplateID
	tenantID, configID := labels.BKTenantID, labels.ConfigID
	isDefault, objectModelCode := labels.IsDefault, labels.ObjectModelCode
	return models.CWStrategy{
		Active: true, Kind: models.CWStrategyKindStrategy, Name: f.RuntimeConfig.Metadata.Name,
		Spec: f.RuntimeConfig.Spec, BKTenantID: &tenantID, BKBizID: &bizID, IsDefault: &isDefault,
		MonitorTemplateID: &templateID, MonitorTemplateName: f.MonitorTemplateName,
		ConfigID: &configID, ObjectModelCode: &objectModelCode,
	}
}

func (f promQLSnapshotFixture) alert(t *testing.T) domain.Event {
	t.Helper()
	parsedAt := func(value string) time.Time {
		t.Helper()
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	evaluations := make([]domain.EventEvaluation, 0, len(f.Event.Evaluations))
	for _, evaluation := range f.Event.Evaluations {
		evaluations = append(evaluations, domain.EventEvaluation{Severity: evaluation.Severity, Action: domain.EventAction(evaluation.Action)})
	}
	return domain.Event{
		EventSourceVersion: 34, EventID: f.Event.EventID, BKTenantID: f.Event.BKTenantID, EventSourceID: "built_in_bk",
		Fingerprint: "be787aaa49e28d74e8c7cd6aeff61fb9", Title: f.Event.Title, Content: f.Event.Content,
		Evaluations: evaluations, Dimensions: fixtureDimensions(t, f.Event.Dimensions), Labels: fixtureDimensions(t, f.Event.Labels),
		SubjectSystem: f.Event.Subject.System, SubjectType: f.Event.Subject.Type, SubjectID: f.Event.Subject.ID,
		SubjectName: f.Event.Subject.Name, SourceEventID: f.Event.SourceEventID,
		ExtraData: f.Event.ExtraData, OccurredAt: parsedAt(f.Event.OccurredAt), ProducedAt: parsedAt(f.Event.ProducedAt),
		ReceivedAt: parsedAt(f.Event.ReceivedAt), CreateAt: parsedAt(f.Event.CreateAt),
		EventEnrichment: domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending},
		Values:          domain.EventValues{}, SourceRawData: domain.JSONObject{},
	}
}

// promQLSnapshotReader 按真实快照返回策略、OneModel 主机和指标目录，
// 并记录目录与实例查询，用于确认 PromQL 分支不再依赖单指标目录引用。
type promQLSnapshotReader struct {
	fixture       promQLSnapshotFixture
	strategy      models.CWStrategy
	withoutMetric bool
	metricErr     error
	metricQueries []models.MetricQuery
	instanceCalls []enrich.InstanceQuery
}

func (r *promQLSnapshotReader) GetByStrategyID(_ context.Context, query models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	revision := r.fixture.StrategyRevision
	if query.TenantID != revision.TenantID || query.ID != revision.StrategyID || query.Version != revision.StrategyVersion {
		return models.CWStrategy{}, false, fmt.Errorf("unexpected strategy revision query %#v", query)
	}
	return r.strategy, true, nil
}

func (*promQLSnapshotReader) IsGlobalBusiness(context.Context, string, int64) (bool, bool, error) {
	return false, false, nil
}

func (r *promQLSnapshotReader) GetModelByCode(_ context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	if modelCode != rules.HostModelCode {
		return enrich.Model{}, false, fmt.Errorf("unexpected model code %q", modelCode)
	}
	return enrich.Model{TenantID: tenantID, ModelID: r.fixture.Host.Model.ModelID, ModelCode: modelCode, Fields: r.fixture.Host.Model.Fields}, true, nil
}

func (r *promQLSnapshotReader) FindInstance(_ context.Context, tenantID string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.instanceCalls = append(r.instanceCalls, query)
	switch query.ModelCode {
	case "cw-biz":
		return enrich.Instance{TenantID: tenantID, ModelCode: "cw-biz", InstanceID: fmt.Sprint(query.InstanceID), Attributes: map[string]any{rules.FieldBKBizName: "蓝鲸2"}}, true, nil
	case rules.HostModelCode:
		instance := r.fixture.Host.Instance
		if query.InstanceID != instance.InstanceID && !hostFixtureMatchesAddress(query, instance.Attributes) {
			return enrich.Instance{}, false, nil
		}
		return enrich.Instance{TenantID: tenantID, ModelCode: instance.ModelCode, InstanceID: instance.InstanceID, Fields: instance.Fields, Attributes: instance.Attributes}, true, nil
	default:
		return enrich.Instance{}, false, fmt.Errorf("unexpected instance query %#v", query)
	}
}

func (*promQLSnapshotReader) FindRelatedHost(context.Context, string, string, string, string) (enrich.Instance, bool, error) {
	return enrich.Instance{}, false, nil
}

func (r *promQLSnapshotReader) FindHostTopology(_ context.Context, tenantID, hostID string, _ bool) (models.ResourceTopology, bool, error) {
	if hostID != r.fixture.Host.Instance.InstanceID {
		return models.ResourceTopology{}, false, nil
	}
	return r.fixture.Host.Topology, true, nil
}

func (r *promQLSnapshotReader) FindMetric(_ context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	r.metricQueries = append(r.metricQueries, query)
	if r.metricErr != nil {
		return models.MetricMetadata{}, false, r.metricErr
	}
	if r.withoutMetric {
		return models.MetricMetadata{}, false, nil
	}
	for _, row := range r.fixture.Catalog {
		if query.MetricID > 0 && row.ID != query.MetricID {
			continue
		}
		if row.PhysicalField != query.FieldName {
			continue
		}
		if query.TableID != "" && row.TableID != query.TableID && row.DataLabel != query.TableID {
			continue
		}
		return models.MetricMetadata{
			ObjectModelCode: row.ObjectModelCode, Kind: row.Kind, TableID: row.TableID, DataLabel: row.DataLabel,
			PhysicalField: row.PhysicalField, FieldName: row.FieldName, FieldCNName: row.FieldCNName, Unit: row.Unit,
		}, true, nil
	}
	return models.MetricMetadata{}, false, nil
}

func (*promQLSnapshotReader) GetAlarmSource(_ context.Context, _, sourceID string) (models.AlarmSource, bool, error) {
	return models.AlarmSource{Id: sourceID, Name: "鲸眼监控"}, true, nil
}

// TestPromQLPublishedSnapshotEnrichChain 使用触发 linkd-93f26184 的真实发布快照验证
// 整条丰富链路：PromQL 声明既没有 metric_ref_id 也没有表名和字段，仍要生成资源、
// 展示、指标结果，并且查询参数只携带完整表达式。
func TestPromQLPublishedSnapshotEnrichChain(t *testing.T) {
	t.Parallel()
	fixture := loadPromQLSnapshotFixture(t)
	event := fixture.alert(t)
	original := event.Clone()
	reader := &promQLSnapshotReader{fixture: fixture, strategy: fixture.strategy()}
	chain, err := enrich.NewChain(
		[]enrich.Processor{Strategy{}, MonitorResource{}, Display{}, Metric{}, EventSource{}},
		enrich.Sources{CWStrategy: reader, Model: reader, OneModel: reader, CollectTopology: reader, Metric: reader, AlarmSource: reader},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	for index, envelope := range payload.Processors {
		for name, processor := range envelope {
			if processor.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("processor %d %s status=%s diagnostics=%#v", index, name, processor.Status, processor.Diagnostics)
			}
		}
	}
	if result.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("status=%s", result.Status)
	}
	resource := payload.Processors[1][rules.MonitorResourceProcessor]
	var resourceValues models.ResourceValues
	if err := json.Unmarshal(mustRawObject(t, resource.Value), &resourceValues); err != nil {
		t.Fatal(err)
	}
	host := fixture.Host.Instance
	if resourceValues.ModelID != rules.HostModelCode || resourceValues.ModelInstID != host.InstanceID ||
		fmt.Sprint(resourceValues.BKInstID) != host.InstanceID || fmt.Sprint(resourceValues.BKBizID) != "2" ||
		resourceValues.BKBizName != "蓝鲸2" || !slices.Contains(resourceValues.CWLabels, "cw-Host|"+host.InstanceID) {
		t.Fatalf("resource=%#v", resourceValues)
	}
	metric := payload.Processors[3][rules.MetricProcessor]
	var metricValues models.MetricValues
	if err := json.Unmarshal(mustRawObject(t, metric.Value), &metricValues); err != nil {
		t.Fatal(err)
	}
	if metricValues.Unit != "percent" || metricValues.MetricName != "usage" || metricValues.ResultTableID != "system.cpu_summary" ||
		metricValues.MetricUniqueID != "system.cpu_summary.usage" || metricValues.DisplayName != "CPU使用率" ||
		metricValues.TimeInterval != float64(60) {
		t.Fatalf("metric=%#v", metricValues)
	}
	display := payload.Processors[2][rules.DisplayProcessor]
	var displayValues models.DisplayValues
	if err := json.Unmarshal(mustRawObject(t, display.Value), &displayValues); err != nil {
		t.Fatal(err)
	}
	hostName := fmt.Sprint(fixture.Host.Instance.Fields["display_name"])
	// 基于数据的标题沿用旧 KAC 的“监控项名称 + 发生了告警”，主机身份进入 object。
	if displayValues.Title != "CPU使用率发生了告警" || displayValues.Object != hostName || displayValues.Content != event.Content {
		t.Fatalf("display=%#v", displayValues)
	}
	if len(displayValues.Dimensions) != len(event.Dimensions) || displayValues.DimensionText == "" {
		t.Fatalf("PromQL result labels were lost: dimensions=%d want=%d text=%q", len(displayValues.Dimensions), len(event.Dimensions), displayValues.DimensionText)
	}
	for _, dimension := range displayValues.Dimensions {
		if dimension.RealKey == nil || dimension.RealValue == nil || *dimension.RealValue != event.Dimensions[*dimension.RealKey] {
			t.Fatalf("display lost original label identity: %#v", dimension)
		}
	}
	// 目录按冒号段派生的表名和字段反查，不使用目录主键，也不改写发布材料。
	if len(reader.metricQueries) == 0 {
		t.Fatal("metric catalog was not consulted")
	}
	for _, query := range reader.metricQueries {
		if query.TenantID != event.BKTenantID || query.MetricID != 0 ||
			query.TableID != "system.cpu_summary" || query.FieldName != "usage" {
			t.Fatalf("metric query=%#v", query)
		}
	}
	for _, query := range reader.instanceCalls {
		if query.ModelCode != rules.HostModelCode && query.ModelCode != "cw-biz" {
			t.Fatalf("unexpected instance query %#v", query)
		}
	}
	if !slices.ContainsFunc(reader.instanceCalls, func(query enrich.InstanceQuery) bool {
		return query.ModelCode == rules.HostModelCode && query.InstanceID == host.InstanceID
	}) {
		t.Fatalf("host identity was not used: %#v", reader.instanceCalls)
	}
	if !reflect.DeepEqual(event, original) {
		t.Fatal("enrichment modified the source Event")
	}
}

// TestPromQLWithoutCatalogKeepsDeclaredIdentity 确认目录缺数据时 PromQL 仍能给出
// 表达式派生的指标身份，并且不把目录未命中报告成依赖失败。
func TestPromQLWithoutCatalogKeepsDeclaredIdentity(t *testing.T) {
	t.Parallel()
	fixture := loadPromQLSnapshotFixture(t)
	event := fixture.alert(t)
	delete(event.Dimensions, rules.FieldBKTargetHostID)
	delete(event.Dimensions, rules.FieldBKTargetIP)
	delete(event.Dimensions, rules.FieldBKHostID)
	delete(event.Dimensions, rules.FieldIP)
	event.SubjectSystem, event.SubjectType, event.SubjectID = "", "", ""
	reader := &promQLSnapshotReader{fixture: fixture, strategy: fixture.strategy(), withoutMetric: true}
	chain, err := enrich.NewChain(
		[]enrich.Processor{Strategy{}, MonitorResource{}, Display{}, Metric{}},
		enrich.Sources{CWStrategy: reader, Model: reader, OneModel: reader, CollectTopology: reader, Metric: reader},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[1][rules.MonitorResourceProcessor]
	if resource.Status != domain.EnrichStatusPartial || len(resource.Diagnostics) != 1 ||
		resource.Diagnostics[0].Code != enrich.DiagnosticCodeMissingField ||
		resource.Diagnostics[0].Dependency != "" {
		t.Fatalf("resource=%#v", resource)
	}
	metric := payload.Processors[3][rules.MetricProcessor]
	var metricValues models.MetricValues
	if err := json.Unmarshal(mustRawObject(t, metric.Value), &metricValues); err != nil {
		t.Fatal(err)
	}
	if metric.Status != domain.EnrichStatusSucceeded || metricValues.Unit != "" ||
		metricValues.ResultTableID != "system.cpu_summary" || metricValues.MetricUniqueID != "system.cpu_summary.usage" ||
		metricValues.DisplayName != fixture.RuntimeConfig.Spec.Name {
		t.Fatalf("metric=%#v values=%#v", metric, metricValues)
	}
}

// TestPromQLIdentityExtraction 覆盖函数、标签、时间窗口、聚合和多序列表达式。
// 只有能唯一确定冒号段序列的表达式才给出结果表与字段。
func TestPromQLIdentityExtraction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expression, field, table string
	}{
		{name: "bare metric", expression: "bkmonitor:system:cpu_summary:usage", field: "usage", table: "system.cpu_summary"},
		{name: "function label and window", expression: `avg_over_time(bkmonitor:system:cpu_summary:usage{bk_biz_id="2"}[60s])`, field: "usage", table: "system.cpu_summary"},
		{name: "aggregation by list", expression: `sum(rate(bkmonitor:system:cpu_summary:usage{device_name="cpu-total"}[5m])) by (hostname)`, field: "usage", table: "system.cpu_summary"},
		{name: "label value with colon", expression: `bkmonitor:system:cpu_summary:usage{device="a:b:c"}`, field: "usage", table: "system.cpu_summary"},
		{name: "label value with brace", expression: `bkmonitor:system:cpu_summary:usage{device="}"}`, field: "usage", table: "system.cpu_summary"},
		{name: "escaped quote in label value", expression: `bkmonitor:system:cpu_summary:usage{device="a\"b"}`, field: "usage", table: "system.cpu_summary"},
		{name: "offset", expression: `bkmonitor:system:cpu_summary:usage offset 5m`, field: "usage", table: "system.cpu_summary"},
		{name: "colon and plain series", expression: `bkmonitor:system:cpu_summary:usage / process_cpu_seconds_total`},
		{name: "plain and colon series", expression: `process_cpu_seconds_total / bkmonitor:system:cpu_summary:usage`},
		{name: "label matching lists", expression: `sum by (hostname) (bkmonitor:system:cpu_summary:usage)`, field: "usage", table: "system.cpu_summary"},
		{name: "aggregation compact labels", expression: `sum by(hostname)(bkmonitor:system:cpu_summary:usage)`, field: "usage", table: "system.cpu_summary"},
		{name: "binary matching lists", expression: `bkmonitor:system:cpu_summary:usage / on (hostname) group_left (instance) process_cpu_seconds_total`},
		{name: "unnamed selector mixed with named", expression: `bkmonitor:system:cpu_summary:usage + {__name__="process_cpu_seconds_total"}`},
		{name: "unnamed selector after logical operator", expression: `bkmonitor:system:cpu_summary:usage or {__name__="process_cpu_seconds_total"}`},
		{name: "scalar multiplier", expression: `bkmonitor:system:cpu_summary:usage * 100`, field: "usage", table: "system.cpu_summary"},
		{name: "two series", expression: `bkmonitor:system:cpu_summary:usage + bkmonitor:system:cpu_summary:idle`},
		{name: "name matcher only", expression: `{__name__="bkmonitor:system:cpu_summary:usage"}`},
		{name: "bare metric without table", expression: "usage"},
		{name: "unbalanced expression", expression: `avg_over_time(bkmonitor:system:cpu_summary:usage{bk_biz_id="2"}`},
		{name: "unclosed quote", expression: `bkmonitor:system:cpu_summary:usage{device="a}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			field, table := parsePromQLIdentity(tc.expression)
			if field != tc.field || table != tc.table {
				t.Fatalf("field=%q table=%q", field, table)
			}
		})
	}
}

// TestPromQLQueryConfigTypeDrivesDataSource 确认声明快照缺失 data_source_label 时，
// query_config_type=prometheus 仍会被识别为 Prometheus 查询。
func TestPromQLQueryConfigTypeDrivesDataSource(t *testing.T) {
	t.Parallel()
	fixture := loadPromQLSnapshotFixture(t)
	strategy := fixture.strategy()
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		t.Fatal(err)
	}
	query := projection.QueryConfigs[0]
	if !query.IsPromQL() || query.DataSourceLabel != rules.DataSourcePrometheus || query.DataTypeLabel != "time_series" {
		t.Fatalf("query=%#v", query)
	}
	if query.MetricField != "" || query.ResultTableID != "" || query.MetricRefID != 0 {
		t.Fatalf("published query unexpectedly carries catalog identity: %#v", query)
	}
	// 执行快照同样没有目录引用；PromQL 身份只能来自表达式。
	for _, item := range fixture.RuntimeItems {
		var runtime struct {
			QueryConfigs []struct {
				PromQL      string `json:"promql"`
				MetricRefID int64  `json:"metric_ref_id"`
			} `json:"query_configs"`
		}
		if err := json.Unmarshal(item, &runtime); err != nil {
			t.Fatal(err)
		}
		for _, compiled := range runtime.QueryConfigs {
			if compiled.PromQL == "" || compiled.MetricRefID != 0 {
				t.Fatalf("compiled query=%#v", compiled)
			}
		}
	}
}

type promQLFallbackLogger struct{ records []map[string]any }

func (l *promQLFallbackLogger) WarnContext(_ context.Context, _ string, args ...any) {
	record := make(map[string]any)
	for i := 0; i+1 < len(args); i += 2 {
		record[fmt.Sprint(args[i])] = args[i+1]
	}
	l.records = append(l.records, record)
}

// TestPromQLCatalogAmbiguityFallback 验证可选反查多义时仍保留名称和可信主机，
// 显式引用和基础设施故障继续失败，不能把所有目录错误都视为未命中。
func TestPromQLCatalogAmbiguityFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		metricID int64
		readErr  error
		failed   bool
	}{
		{name: "optional ambiguity", readErr: enrich.ErrAmbiguousMetricLocator},
		{name: "explicit reference", metricID: 9, readErr: enrich.ErrAmbiguousMetricLocator, failed: true},
		{name: "database unavailable", readErr: errors.New("database unavailable"), failed: true},
		{name: "invalid response", readErr: enrich.ErrInvalidDataSourceResponse, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := loadPromQLSnapshotFixture(t)
			strategy := fixture.strategy()
			strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID = tc.metricID
			reader := &promQLSnapshotReader{fixture: fixture, strategy: strategy, metricErr: tc.readErr}
			logger := &promQLFallbackLogger{}
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, MonitorResource{}, Display{}, Metric{}}, enrich.Sources{CWStrategy: reader, Model: reader, OneModel: reader, CollectTopology: reader, Metric: reader}, enrich.WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: fixture.alert(t)})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range payload.Processors[1:] {
				for name, output := range entry {
					if tc.failed {
						if output.Status != domain.EnrichStatusFailed {
							t.Fatalf("%s=%#v", name, output)
						}
					} else if output.Status != domain.EnrichStatusPartial || len(output.Diagnostics) != 1 || output.Diagnostics[0].Dependency != rules.DependencyMetricCatalog {
						t.Fatalf("%s=%#v", name, output)
					}
				}
			}
			if len(logger.records) != 3 {
				t.Fatalf("missing failure or degradation logs: %#v", logger.records)
			}
			for _, record := range logger.records {
				cause, ok := record["error"].(error)
				if !ok || !errors.Is(cause, tc.readErr) {
					t.Fatalf("original cause lost: %#v", record)
				}
			}
			if !tc.failed {
				// 多次中文名/单位/维度读取复用同一 Scope 缓存，不能重复查询目录。
				if len(reader.metricQueries) != 1 {
					t.Fatalf("catalog calls=%d", len(reader.metricQueries))
				}
				var display models.DisplayValues
				if err := json.Unmarshal(mustRawObject(t, payload.Processors[2][rules.DisplayProcessor].Value), &display); err != nil {
					t.Fatal(err)
				}
				if display.Title != fixture.RuntimeConfig.Spec.Name+"发生了告警" {
					t.Fatalf("display=%#v", display)
				}
				var resource models.ResourceValues
				if err := json.Unmarshal(mustRawObject(t, payload.Processors[1][rules.MonitorResourceProcessor].Value), &resource); err != nil {
					t.Fatal(err)
				}
				if resource.ModelInstID != fixture.Host.Instance.InstanceID {
					t.Fatalf("resource=%#v", resource)
				}
			}
		})
	}
}

// TestPromQLMixedSeriesEnrich 不把比值表达式套用其中某个指标的单位或中文名。
func TestPromQLMixedSeriesEnrich(t *testing.T) {
	t.Parallel()
	fixture := loadPromQLSnapshotFixture(t)
	strategy := fixture.strategy()
	expression := "bkmonitor:system:cpu_summary:usage / process_cpu_seconds_total"
	strategy.Spec.StrategyItem.QueryConfigs[0].PromQL = expression
	reader := &promQLSnapshotReader{fixture: fixture, strategy: strategy}
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, MonitorResource{}, Display{}, Metric{}}, enrich.Sources{CWStrategy: reader, Model: reader, OneModel: reader, CollectTopology: reader, Metric: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: fixture.alert(t)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	var metric models.MetricValues
	if err := json.Unmarshal(mustRawObject(t, payload.Processors[3][rules.MetricProcessor].Value), &metric); err != nil {
		t.Fatal(err)
	}
	if metric.Unit != "" || metric.MetricName != "" || metric.ResultTableID != "" || metric.MetricUniqueID != "" || metric.DisplayName != strategy.Spec.Name || len(reader.metricQueries) != 0 {
		t.Fatalf("metric=%#v catalog=%#v", metric, reader.metricQueries)
	}
	var display models.DisplayValues
	if err := json.Unmarshal(mustRawObject(t, payload.Processors[2][rules.DisplayProcessor].Value), &display); err != nil {
		t.Fatal(err)
	}
	if display.Title != strategy.Spec.Name+"发生了告警" || result.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("title=%q status=%s", display.Title, result.Status)
	}
}
