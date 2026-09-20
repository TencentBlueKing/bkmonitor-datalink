// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

//go:embed testdata/kac_basetarget/*.json
var kacBaseTargetFixtures embed.FS

type kacBaseTargetFixture struct {
	Name           string                    `json:"name"`
	KACCase        string                    `json:"kac_case"`
	Branch         string                    `json:"branch"`
	Alert          kacFixtureAlert           `json:"alert"`
	Strategy       kacFixtureStrategy        `json:"strategy"`
	Model          kacFixtureModel           `json:"model"`
	Instance       kacFixtureInstance        `json:"instance"`
	CollectConfig  *kacFixtureCollectConfig  `json:"collect_config,omitempty"`
	RelatedHost    *kacFixtureInstance       `json:"related_host,omitempty"`
	UptimeTask     *kacFixtureUptimeTask     `json:"uptime_task,omitempty"`
	UptimeNode     *kacFixtureUptimeNode     `json:"uptime_node,omitempty"`
	Business       *kacFixtureInstance       `json:"business,omitempty"`
	CloudHost      *kacFixtureInstance       `json:"cloud_host,omitempty"`
	Topology       kacFixtureTopology        `json:"topology"`
	Metric         models.MetricMetadata     `json:"metric"`
	ExpectedStatus domain.EnrichStatus       `json:"expected_status"`
	ExpectedKAC    map[string]map[string]any `json:"expected_kac"`
	ExpectedData   json.RawMessage           `json:"expected_payload"`
}

type kacFixtureAlert struct {
	StrategyID      int64          `json:"strategy_id"`
	StrategyVersion int64          `json:"strategy_version"`
	BKBizID         int64          `json:"bk_biz_id"`
	SubjectName     string         `json:"subject_name"`
	Content         string         `json:"content"`
	SourceEventID   string         `json:"source_event_id"`
	Dimensions      map[string]any `json:"dimensions"`
}

type kacFixtureStrategy struct {
	ConfigType        string                       `json:"config_type"`
	IsDefault         bool                         `json:"is_default"`
	DataSource        string                       `json:"data_source"`
	Name              string                       `json:"name"`
	AlarmAlias        string                       `json:"alarm_alias"`
	ObjectModelCode   string                       `json:"object_model_code"`
	MetricObjectModel string                       `json:"metric_object_model"`
	BKBizID           int64                        `json:"bk_biz_id"`
	MonitorTemplateID int64                        `json:"monitor_template_id"`
	ConfigID          string                       `json:"config_id"`
	AggregateMethod   string                       `json:"aggregate_method"`
	AggregatePeriod   any                          `json:"aggregate_period"`
	Expression        string                       `json:"expression"`
	QueryConfigs      []models.StrategyQueryConfig `json:"query_configs"`
}

type kacFixtureModel struct {
	ModelID   string         `json:"model_id"`
	ModelCode string         `json:"model_code"`
	Fields    map[string]any `json:"fields"`
}

type kacFixtureInstance struct {
	ModelCode  string         `json:"model_code"`
	InstanceID string         `json:"instance_id"`
	Fields     map[string]any `json:"fields"`
	Attributes map[string]any `json:"attributes"`
}

type kacFixtureCollectConfig struct {
	UID             string `json:"uid"`
	BKCollectTaskID string `json:"bk_collect_task_id"`
	BKObjectCode    string `json:"bk_object_code"`
	BKInstID        int64  `json:"bk_inst_id"`
	FinalBizID      *int64 `json:"final_biz_id"`
	IsRemoteCollect bool   `json:"is_remote_collect"`
}

type kacFixtureUptimeTask struct {
	ID       int64                 `json:"id"`
	TaskID   int64                 `json:"task_id"`
	Name     string                `json:"name"`
	Protocol models.UptimeProtocol `json:"protocol"`
	BKBizID  int64                 `json:"bk_biz_id"`
}

type kacFixtureUptimeNode struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	PlatID int64  `json:"plat_id"`
	IP     string `json:"ip"`
}

type kacFixtureTopology struct {
	HostID       string `json:"host_id"`
	BKBizID      int64  `json:"bk_biz_id"`
	BKBizName    string `json:"bk_biz_name"`
	BKSetID      int64  `json:"bk_set_id"`
	BKSetName    string `json:"bk_set_name"`
	BKModuleID   int64  `json:"bk_module_id"`
	BKModuleName string `json:"bk_module_name"`
}

func TestKACBaseTargetFixtures(t *testing.T) {
	t.Parallel()
	paths, err := fs.Glob(kacBaseTargetFixtures, "testdata/kac_basetarget/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 10 {
		t.Fatalf("fixture count=%d, want 10", len(paths))
	}
	sort.Strings(paths)
	for _, fixturePath := range paths {
		fixturePath := fixturePath
		t.Run(path.Base(fixturePath), func(t *testing.T) {
			fixture := loadKACBaseTargetFixture(t, fixturePath)
			alert := fixture.alert(t)
			original := alert.Clone()
			strategy := fixture.Strategy.value()
			classification := rules.Classify(strategy, alert.Dimensions)
			branch := string(classification.BaseTarget)
			if classification.Main == rules.MainData {
				branch = string(rules.MainData)
			}
			if branch != fixture.Branch {
				t.Fatalf("classified branch=%q, fixture branch=%q", branch, fixture.Branch)
			}
			reader := &kacFixtureReader{fixture: fixture}
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{
				CWStrategy: reader, Model: reader, OneModel: reader, CollectConfig: reader, CollectTopology: reader,
				Uptime: reader, UptimeNode: reader,
				Metric: reader, AlarmSource: reader,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != fixture.ExpectedStatus {
				t.Fatalf("status=%q, want %q data=%s", result.Status, fixture.ExpectedStatus, mustJSON(t, result.Data))
			}
			if !reflect.DeepEqual(alert, original) {
				t.Fatalf("Alert changed: before=%#v after=%#v", original, alert)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			assertKACProjection(t, payload, fixture.ExpectedKAC)
			assertJSONEqual(t, fixture.ExpectedData, mustJSON(t, result.Data))
		})
	}
}

func loadKACBaseTargetFixture(t *testing.T, path string) kacBaseTargetFixture {
	t.Helper()
	data, err := fs.ReadFile(kacBaseTargetFixtures, path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture kacBaseTargetFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ExpectedStatus == "" {
		fixture.ExpectedStatus = domain.EnrichStatusSucceeded
	}
	if fixture.Name == "" || fixture.KACCase == "" || fixture.Branch == "" {
		t.Fatalf("fixture metadata is incomplete: %#v", fixture)
	}
	return fixture
}

func (f kacBaseTargetFixture) alert(t *testing.T) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{
		EventSourceVersion: 1, AlertID: "fixture-" + f.Name, BKTenantID: "tenant-a", EventSourceID: "built_in_bk",
		Fingerprint: "fixture-" + f.Name, Title: f.Strategy.Name, Content: f.Alert.Content, Severity: "warning",
		SubjectName: f.Alert.SubjectName, SourceEventID: f.Alert.SourceEventID,
		Dimensions: fixtureDimensions(t, f.Alert.Dimensions), Labels: domain.DimensionMap{
			"strategy_id": numberScalar(t, f.Alert.StrategyID), "strategy_version": numberScalar(t, f.Alert.StrategyVersion),
			"bk_biz_id": numberScalar(t, f.Alert.BKBizID),
		}, ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive,
		LatestEventID: "event-1", TriggerEventID: "event-1", LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func fixtureDimensions(t *testing.T, values map[string]any) domain.DimensionMap {
	t.Helper()
	result := make(domain.DimensionMap, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			result[key] = domain.NewStringScalar(typed)
		case float64:
			result[key] = numberScalar(t, int64(typed))
		case bool:
			result[key] = domain.NewBoolScalar(typed)
		default:
			t.Fatalf("dimension %s has unsupported type %T", key, value)
		}
	}
	return result
}

func optionalModelCode(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func numberScalar(t *testing.T, value int64) domain.Scalar {
	t.Helper()
	result, err := domain.NewNumberScalar(float64(value))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f kacFixtureStrategy) value() models.CWStrategy {
	period, _ := json.Marshal(f.AggregatePeriod)
	bizID, templateID, configID, modelCode := f.BKBizID, f.MonitorTemplateID, f.ConfigID, f.ObjectModelCode
	return models.CWStrategy{
		ObjectModelCode: optionalModelCode(modelCode), BKBizID: &bizID, MonitorTemplateID: &templateID, ConfigID: &configID,
		IsDefault: &f.IsDefault,
		Spec: models.CWStrategySpec{ConfigType: f.ConfigType, Name: f.Name, AlarmAlias: f.AlarmAlias, DataSource: f.DataSource, TableID: f.QueryConfigs[0].ResultTableID,
			FieldName: f.QueryConfigs[0].MetricField, StrategyItem: &models.CWStrategyItem{
				AggregateMethod: f.AggregateMethod, AggregatePeriod: period, Expression: f.Expression,
				Functions: json.RawMessage(`[]`), QueryConfigs: f.QueryConfigs,
			}},
	}
}

type kacFixtureReader struct{ fixture kacBaseTargetFixture }

func (r *kacFixtureReader) GetByBKStrategyID(_ context.Context, tenantID string, strategyID int64) (models.CWStrategy, bool, error) {
	if tenantID != "tenant-a" || strategyID != r.fixture.Alert.StrategyID {
		return models.CWStrategy{}, false, fmt.Errorf("unexpected strategy query tenant=%s strategy=%d", tenantID, strategyID)
	}
	return r.fixture.Strategy.value(), true, nil
}

func (*kacFixtureReader) IsGlobalBusiness(context.Context, string, int64) (bool, bool, error) {
	return false, false, nil
}

func (r *kacFixtureReader) GetModelByCode(_ context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	if tenantID != "tenant-a" || modelCode != r.fixture.Model.ModelCode {
		return enrich.Model{}, false, fmt.Errorf("unexpected model query tenant=%s model=%s", tenantID, modelCode)
	}
	return enrich.Model{TenantID: tenantID, ModelID: r.fixture.Model.ModelID, ModelCode: modelCode, Fields: r.fixture.Model.Fields}, true, nil
}

func (r *kacFixtureReader) GetCollectConfig(_ context.Context, tenantID, taskID string) (models.CollectConfig, bool, error) {
	if r.fixture.CollectConfig == nil {
		return models.CollectConfig{}, false, nil
	}
	config := r.fixture.CollectConfig
	if tenantID != "tenant-a" || taskID != config.BKCollectTaskID {
		return models.CollectConfig{}, false, fmt.Errorf("unexpected collect config query tenant=%s task=%s", tenantID, taskID)
	}
	return models.CollectConfig{UID: config.UID, BKTenantID: tenantID, BKCollectTaskID: config.BKCollectTaskID,
		BKObjectCode: config.BKObjectCode, BKInstID: config.BKInstID, FinalBizID: config.FinalBizID,
		IsRemoteCollect: config.IsRemoteCollect}, true, nil
}

func (r *kacFixtureReader) FindInstance(_ context.Context, tenantID string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if r.fixture.Branch == "data" && query.ModelCode == "cw-biz" {
		v := r.fixture.Business
		if v != nil {
			return enrich.Instance{TenantID: tenantID, ModelCode: v.ModelCode, InstanceID: v.InstanceID, Fields: v.Fields, Attributes: v.Attributes}, true, nil
		}
		return enrich.Instance{TenantID: tenantID, ModelCode: "cw-biz", InstanceID: fmt.Sprint(r.fixture.Alert.BKBizID), Attributes: map[string]any{rules.FieldBKBizName: "fixture-biz-5"}}, true, nil
	}
	if r.fixture.Branch == "data" && query.ModelCode == "cw-MySQL" {
		v := r.fixture.Instance
		return enrich.Instance{TenantID: tenantID, ModelCode: v.ModelCode, InstanceID: v.InstanceID, Fields: v.Fields, Attributes: v.Attributes}, true, nil
	}
	if query.ModelCode == "cw-biz" || query.ModelCode == rules.HostModelCode && r.fixture.CloudHost != nil {
		fixture := r.fixture.Business
		if query.ModelCode == rules.HostModelCode {
			fixture = r.fixture.CloudHost
		}
		if fixture == nil {
			return enrich.Instance{}, false, nil
		}
		return enrich.Instance{TenantID: tenantID, ModelCode: fixture.ModelCode, InstanceID: fixture.InstanceID, Fields: fixture.Fields, Attributes: fixture.Attributes}, true, nil
	}
	if tenantID != "tenant-a" || query.ModelCode != r.fixture.Instance.ModelCode || query.InstanceID != r.fixture.Instance.InstanceID {
		return enrich.Instance{}, false, fmt.Errorf("unexpected instance query tenant=%s query=%#v", tenantID, query)
	}
	return enrich.Instance{TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: query.InstanceID, Fields: r.fixture.Instance.Fields, Attributes: r.fixture.Instance.Attributes}, true, nil
}

func (r *kacFixtureReader) FindRelatedHost(_ context.Context, tenantID, modelCode, instanceID, _ string) (enrich.Instance, bool, error) {
	if r.fixture.RelatedHost == nil {
		return enrich.Instance{}, false, nil
	}
	host := r.fixture.RelatedHost
	if tenantID != "tenant-a" || modelCode != r.fixture.Instance.ModelCode || instanceID != r.fixture.Instance.InstanceID {
		return enrich.Instance{}, false, fmt.Errorf("unexpected related host query tenant=%s model=%s instance=%s", tenantID, modelCode, instanceID)
	}
	return enrich.Instance{TenantID: tenantID, ModelCode: host.ModelCode, InstanceID: host.InstanceID, Fields: host.Fields, Attributes: host.Attributes}, true, nil
}

func (r *kacFixtureReader) GetUptimeTask(_ context.Context, tenantID, taskID string) (models.UptimeTask, bool, error) {
	if r.fixture.UptimeTask == nil {
		return models.UptimeTask{}, false, nil
	}
	v := r.fixture.UptimeTask
	if tenantID != "tenant-a" || taskID != fmt.Sprint(v.TaskID) {
		return models.UptimeTask{}, false, fmt.Errorf("unexpected uptime task query: %s/%s", tenantID, taskID)
	}
	return models.UptimeTask{ID: v.ID, TaskID: v.TaskID, Name: v.Name, Protocol: v.Protocol, BKBizID: v.BKBizID, BKTenantID: tenantID}, true, nil
}

func (r *kacFixtureReader) GetUptimeNode(_ context.Context, tenantID, nodeID string) (models.UptimeNode, bool, error) {
	if r.fixture.UptimeNode == nil {
		return models.UptimeNode{}, false, nil
	}
	v := r.fixture.UptimeNode
	if tenantID != "tenant-a" || nodeID != fmt.Sprintf("%d:%s", v.PlatID, v.IP) {
		return models.UptimeNode{}, false, fmt.Errorf("unexpected uptime node query: %s/%s", tenantID, nodeID)
	}
	return models.UptimeNode{ID: v.ID, Name: v.Name, PlatID: v.PlatID, IP: v.IP, BKTenantID: tenantID}, true, nil
}

func (r *kacFixtureReader) FindHostTopology(_ context.Context, tenantID, hostID string) (models.ResourceTopology, bool, error) {
	if tenantID != "tenant-a" || hostID != r.fixture.Topology.HostID {
		return models.ResourceTopology{}, false, fmt.Errorf("unexpected topology query tenant=%s host=%s", tenantID, hostID)
	}
	v := r.fixture.Topology
	return models.ResourceTopology{BKBizID: v.BKBizID, BKBizName: v.BKBizName, BKSetID: v.BKSetID,
		BKSetName: v.BKSetName, BKModuleID: v.BKModuleID, BKModuleName: v.BKModuleName}, true, nil
}

func (r *kacFixtureReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	metadata := r.fixture.Metric
	if metadata.ObjectModelCode == "" && r.fixture.Branch != "data" {
		metadata.ObjectModelCode = r.fixture.Model.ModelCode
	}
	if r.fixture.Strategy.MetricObjectModel != "" {
		metadata.ObjectModelCode = r.fixture.Strategy.MetricObjectModel
	}
	return metadata, true, nil
}

func (*kacFixtureReader) GetAlarmSourceName(context.Context, string, string) (string, bool, error) {
	return "基础监控", true, nil
}

func assertKACProjection(t *testing.T, payload enrich.Payload, expected map[string]map[string]any) {
	t.Helper()
	if len(payload.Processors) != 5 {
		t.Fatalf("processor count=%d", len(payload.Processors))
	}
	for processor, fields := range expected {
		var envelope enrich.ProcessorEnvelope
		found := false
		for _, entry := range payload.Processors {
			if value, exists := entry[processor]; exists {
				envelope, found = value, true
				break
			}
		}
		if !found {
			t.Fatalf("processor %s is missing", processor)
		}
		for field, want := range fields {
			raw, exists := envelope.Value[field]
			if !exists {
				t.Fatalf("processor %s field %s is missing", processor, field)
			}
			var got any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("processor %s field %s=%#v, want %#v", processor, field, got, want)
			}
		}
	}
}

func assertJSONEqual(t *testing.T, expected json.RawMessage, actual []byte) {
	t.Helper()
	var want, got any
	if len(expected) == 0 || string(expected) == "null" {
		t.Fatal("fixture expected_payload is empty")
	}
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload mismatch\ngot:  %s\nwant: %s", actual, expected)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
