// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package assembly

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
	onemodelassembly "linkd/internal/onemodel/assembly"
)

type metricResourceLiveCatalog struct {
	ID               int64    `json:"id"`
	MetricsetCode    string   `json:"metricset_code"`
	RefMetricsetCode string   `json:"ref_metricset_code"`
	RecordRuleID     *int64   `json:"record_rule_id"`
	ModelIDs         []string `json:"model_ids"`
}

type metricResourceLiveReader struct {
	reader   enrich.MetricReader
	expected map[int64]metricResourceLiveCatalog
	reads    []metricResourceLiveRead
	t        *testing.T
}

type metricResourceLiveRead struct {
	Query                                            models.MetricQuery
	Found, Invalid                                   bool
	MetricsetCode, RefMetricsetCode, Kind, FieldName string
	ModelIDs                                         []string
	RecordRuleID                                     *int64
}

func (r *metricResourceLiveReader) FindMetric(ctx context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	metadata, found, err := r.reader.FindMetric(ctx, query)
	r.reads = append(r.reads, metricResourceLiveRead{Query: query, Found: found, Invalid: err != nil, MetricsetCode: metadata.MetricsetCode, RefMetricsetCode: metadata.RefMetricsetCode, Kind: metadata.Kind, FieldName: metadata.FieldName, ModelIDs: metadata.ModelIDs, RecordRuleID: metadata.RecordRuleID})
	if expected, ok := r.expected[query.MetricID]; ok {
		if err != nil || !found || metadata.MetricsetCode != expected.MetricsetCode || metadata.RefMetricsetCode != expected.RefMetricsetCode || !reflect.DeepEqual(metadata.RecordRuleID, expected.RecordRuleID) || !reflect.DeepEqual(metadata.ModelIDs, expected.ModelIDs) {
			r.t.Errorf("catalog projection differs from independent snapshot: metric_id=%d", query.MetricID)
		}
	} else {
		r.t.Errorf("no independent snapshot for metric_id=%d", query.MetricID)
	}
	return metadata, found, err
}

// TestMetricResourceLiveReplay 用显式提供的 Admin 告警/Event 样本和目录快照，
// 重放当前发布的完整丰富链。只读 MySQL/OneModel，不写告警、消息或发布材料。
// 普通单测跳过；报告只保留关联身份、目录投影、状态和指标字段，不保存事件正文及连接配置。
func TestMetricResourceLiveReplay(t *testing.T) {
	connectionFile := os.Getenv("LINKD_METRIC_RESOURCE_LIVE_CONNECTION_FILE")
	fixtureFile := os.Getenv("LINKD_METRIC_RESOURCE_LIVE_FIXTURE_FILE")
	reportFile := os.Getenv("LINKD_METRIC_RESOURCE_LIVE_REPORT_FILE")
	if connectionFile == "" || fixtureFile == "" || reportFile == "" {
		t.Skip("requires explicit read-only environment, Admin alert/Event samples, catalog snapshot and report path")
	}
	read := func(path string, limit int64, target any) {
		t.Helper()
		info, err := os.Stat(path) //nolint:gosec // G703: 操作者显式选择的本地集成测试输入，检查大小上限。
		if err != nil || info.Size() > limit {
			t.Fatal("live input unavailable or oversized")
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: 同上。
		if err != nil || json.Unmarshal(raw, target) != nil {
			t.Fatal("invalid live input")
		}
	}
	var connection struct {
		MySQL     datasources.MySQLConfig `json:"mysql"`
		ESAddress string                  `json:"es_address"`
	}
	read(connectionFile, 16<<10, &connection)
	var fixture struct {
		TenantID string `json:"bk_tenant_id"`
		Samples  []struct {
			Event    domain.Event                   `json:"event"`
			AlertID  string                         `json:"alert_id"`
			Expected map[string]domain.EnrichStatus `json:"expected"`
		} `json:"samples"`
		Catalog       []metricResourceLiveCatalog `json:"catalog"`
		Enrich        config.EnrichConfig         `json:"enrich"`
		SourceVersion int64                       `json:"source_version"`
	}
	read(fixtureFile, 8<<20, &fixture)
	if fixture.TenantID == "" || fixture.SourceVersion <= 0 || len(fixture.Samples) == 0 || len(fixture.Samples) > 32 || len(fixture.Catalog) > 256 {
		t.Fatal("invalid live fixture bounds")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runtime, err := datasources.Open(ctx, datasources.Config{MySQL: &connection.MySQL}, 4)
	if err != nil {
		t.Fatal("open read-only MySQL failed")
	}
	t.Cleanup(func() {
		if runtime.Close() != nil {
			t.Error("close MySQL failed")
		}
	})
	client, transport, err := onemodelassembly.Open(&config.OneModelResource{Addresses: []string{connection.ESAddress}}, 4, 5*time.Second)
	if err != nil {
		t.Fatal("open read-only OneModel failed")
	}
	t.Cleanup(func() {
		if transport.Close() != nil {
			t.Error("close OneModel failed")
		}
	})
	sources := runtime.Sources()
	sources.OneModel, sources.CMDB, sources.CollectTopology = client, client, client
	sources.K8s, err = datasources.NewOneModelK8sReader(client)
	if err != nil {
		t.Fatal("create K8s reader failed")
	}
	recorder := &metricResourceLiveReader{reader: sources.Metric, expected: map[int64]metricResourceLiveCatalog{}, t: t}
	for _, row := range fixture.Catalog {
		recorder.expected[row.ID] = row
	}
	sources.Metric = recorder
	facts, err := description.NewResolver(sources.DescriptionConfiguration)
	if err != nil {
		t.Fatal("create description resolver failed")
	}
	type sampleReport struct {
		AlertID                      string
		EventID                      string
		IntakeVersion, SourceVersion int64
		Status                       domain.EnrichStatus
		Reads                        []metricResourceLiveRead
		Evaluations                  []map[string]any
	}
	var report []sampleReport
	for _, sample := range fixture.Samples {
		if sample.AlertID == "" || sample.Event.EventID == "" || sample.Event.BKTenantID != fixture.TenantID || !slices.Contains(sample.Event.RelatedAlertIDs, sample.AlertID) {
			t.Fatal("missing sample identity")
		}
		version := fixture.SourceVersion
		source := config.EventSource{EventSourceID: sample.Event.EventSourceID, Version: version, RelatedTenantID: sample.Event.BKTenantID, Enrich: fixture.Enrich}
		router, err := NewRouter([]config.EventSource{source}, sources, WithDescriptionFacts(facts))
		if err != nil {
			t.Fatal("assemble published enrich chain failed")
		}
		before := sample.Event.Clone()
		recorder.reads = nil
		result, err := router.Enrich(ctx, enrich.Input{Event: sample.Event, Preview: true})
		if err != nil {
			t.Fatalf("replay failed: event_id=%s", sample.Event.EventID)
		}
		if !reflect.DeepEqual(sample.Event, before) {
			t.Fatal("source Event mutated")
		}
		row := sampleReport{AlertID: sample.AlertID, EventID: sample.Event.EventID, IntakeVersion: sample.Event.EventSourceVersion, SourceVersion: result.SourceVersion, Status: result.Status, Reads: recorder.reads}
		for _, evaluation := range result.Data.Evaluations {
			payload, err := enrich.DecodePayload(evaluation.Data)
			if err != nil {
				t.Fatal("invalid enrich payload")
			}
			outputs := map[string]any{}
			seen := map[string]bool{}
			for _, entries := range payload.Processors {
				for name, output := range entries {
					seen[name] = true
					if expected, ok := sample.Expected[name]; ok && expected != output.Status {
						t.Errorf("event_id=%s processor=%s status=%s want=%s diagnostics=%+v", sample.Event.EventID, name, output.Status, expected, output.Diagnostics)
					}
					entry := map[string]any{"status": output.Status, "diagnostics": output.Diagnostics}
					if name == "metric" {
						values := domain.JSONObject{}
						for _, key := range []string{"metric_name", "unit", "result_table_id", "metric_unique_id"} {
							if value, ok := output.Value[key]; ok {
								values[key] = value
							}
						}
						entry["values"] = values
					}
					outputs[name] = entry
				}
			}
			for name := range sample.Expected {
				if !seen[name] {
					t.Errorf("expected processor %s missing", name)
				}
			}
			row.Evaluations = append(row.Evaluations, outputs)
		}
		report = append(report, row)
		t.Logf("alert_id=%s event_id=%s status=%s catalog_reads=%d", row.AlertID, row.EventID, row.Status, len(row.Reads))
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal("encode report failed")
	}
	if os.WriteFile(reportFile, raw, 0o600) != nil { //nolint:gosec // G703: 操作者显式选择的本地报告路径。
		t.Fatal("write report failed")
	}
}
