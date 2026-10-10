package assembly

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/processors"
	onemodelassembly "linkd/internal/onemodel/assembly"
)

type liveCatalogRow struct {
	ID                                                          int64
	TenantID, SpaceUID, Kind, TableID, DataLabel, PhysicalField string
	Metadata                                                    models.MetricMetadata
}

type liveMetricRead struct {
	Query    models.MetricQuery
	Found    bool
	Invalid  bool
	Metadata models.MetricMetadata
}

type liveCatalogReader struct {
	reader  enrich.MetricReader
	catalog []liveCatalogRow
	reads   []liveMetricRead
	t       *testing.T
}

// 快照由独立的只读 SQL 导出；在内存中选择候选行，核对真实 Reader 的身份与投影。
func (r *liveCatalogReader) FindMetric(ctx context.Context, q models.MetricQuery) (models.MetricMetadata, bool, error) {
	metadata, found, err := r.reader.FindMetric(ctx, q)
	r.reads = append(r.reads, liveMetricRead{Query: q, Found: found, Invalid: err != nil, Metadata: metadata})
	var candidates []liveCatalogRow
	for _, row := range r.catalog {
		if row.TenantID != q.TenantID {
			continue
		}
		if q.MetricID > 0 {
			if row.ID == q.MetricID {
				candidates = append(candidates, row)
			}
			continue
		}
		if q.SpaceUID != "" && row.SpaceUID != q.SpaceUID && row.SpaceUID != "*" {
			continue
		}
		if q.ObjectModelCode != "" && row.Metadata.ObjectModelCode != q.ObjectModelCode {
			continue
		}
		if q.FieldTag == models.CWStrategyFieldTagDerivedMetric {
			if row.Kind == "derived" && row.Metadata.FieldName == q.FieldName {
				candidates = append(candidates, row)
			}
		} else if q.TableID != "" && row.Kind == "native" && row.PhysicalField == q.FieldName && (row.TableID == q.TableID || row.DataLabel == q.TableID) {
			candidates = append(candidates, row)
		}
	}
	if len(candidates) == 1 {
		if err != nil || !found || !reflect.DeepEqual(metadata, candidates[0].Metadata) {
			r.t.Errorf("metric projection differs from independent snapshot: metric_id=%d table=%s field=%s", q.MetricID, q.TableID, q.FieldName)
		}
	} else if found || (len(candidates) > 1 && err == nil) || (len(candidates) == 0 && q.TableID != "" && err != nil) {
		r.t.Errorf("metric uniqueness mismatch: candidates=%d found=%t", len(candidates), found)
	}
	return metadata, found, err
}

// TestMetricCatalogLiveActiveEvents 显式选择环境后，在内存中重放真实活跃告警的最新事件。
// 仅查询 MySQL/OneModel，不写告警、发布材料或 Kafka；普通测试不会连接外部服务。
// Report 只保留策略身份、指标查询与状态，不包含事件原文、资源身份或连接凭据。
func TestMetricCatalogLiveActiveEvents(t *testing.T) {
	connectionFile := os.Getenv("LINKD_METRIC_CATALOG_LIVE_CONNECTION_FILE")
	fixtureFile := os.Getenv("LINKD_METRIC_CATALOG_LIVE_FIXTURE_FILE")
	reportFile := os.Getenv("LINKD_METRIC_CATALOG_LIVE_REPORT_FILE")
	if connectionFile == "" || fixtureFile == "" || reportFile == "" {
		t.Skip("requires explicit read-only environment, active events, catalog snapshot and report path")
	}
	read := func(path string, limit int64, output any) {
		t.Helper()
		info, err := os.Stat(path) //nolint:gosec // G703: 操作者显式指定的集成测试文件，读取前检查硬上限。
		if err != nil || info.Size() > limit {
			t.Fatal("live test file unavailable or oversized")
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: 同上，不接受服务请求输入。
		if err != nil || json.Unmarshal(raw, output) != nil {
			t.Fatal("invalid live test file")
		}
	}
	var connection struct {
		MySQL     datasources.MySQLConfig `json:"mysql"`
		ESAddress string                  `json:"es_address"`
	}
	read(connectionFile, 16<<10, &connection)
	var fixture struct {
		Samples []struct {
			Event domain.Event `json:"event"`
			Alert struct {
				TenantID      string             `json:"bk_tenant_id"`
				Status        domain.AlertStatus `json:"status"`
				LatestEventID string             `json:"latest_event_id"`
			} `json:"alert"`
		}
		Catalog []liveCatalogRow
	}
	read(fixtureFile, 8<<20, &fixture)
	if len(fixture.Samples) == 0 || len(fixture.Samples) > 32 || len(fixture.Catalog) > 3000 {
		t.Fatal("invalid live test bounds")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	runtime, err := datasources.Open(ctx, datasources.Config{MySQL: &connection.MySQL}, 4)
	if err != nil {
		t.Fatal("open read-only datasource failed")
	}
	t.Cleanup(func() {
		if runtime.Close() != nil {
			t.Error("close datasource failed")
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
	recorder := &liveCatalogReader{reader: sources.Metric, catalog: fixture.Catalog, t: t}
	sources.Metric, sources.OneModel, sources.CollectTopology = recorder, client, client
	chain, err := enrich.NewChain([]enrich.Processor{processors.Strategy{}, processors.Resource{}, processors.Display{}, processors.Metric{}}, sources)
	if err != nil {
		t.Fatal(err)
	}
	type evaluationReport struct {
		Severity   string
		Processors any
	}
	type eventReport struct {
		StrategyID  int64
		Status      domain.EnrichStatus
		Reads       []liveMetricRead
		Evaluations []evaluationReport
	}
	report := make([]eventReport, 0, len(fixture.Samples))
	for _, sample := range fixture.Samples {
		if sample.Alert.Status != domain.AlertStatusActive || sample.Alert.TenantID != sample.Event.BKTenantID || sample.Alert.LatestEventID != sample.Event.EventID {
			t.Fatal("fixture must bind an active alert to its same-tenant latest event")
		}
		ids, diagnostics := enrich.ValidateRequiredIDs(sample.Event)
		if len(diagnostics) != 0 {
			t.Fatal("invalid source event identity")
		}
		_, found, readErr := sources.CWStrategy.GetByStrategyID(ctx, models.StrategyQuery{TenantID: sample.Event.BKTenantID, ID: ids.StrategyID, Version: ids.StrategyVersion})
		if readErr != nil || !found {
			t.Fatalf("matching publication unavailable: strategy=%d", ids.StrategyID)
		}
		before := sample.Event.Clone()
		recorder.reads = nil
		result, enrichErr := chain.Enrich(ctx, enrich.Input{Event: sample.Event, Preview: true})
		if enrichErr != nil {
			t.Fatalf("enrich failed: strategy=%d", ids.StrategyID)
		}
		if !reflect.DeepEqual(sample.Event, before) {
			t.Fatal("live source event mutated")
		}
		row := eventReport{StrategyID: ids.StrategyID, Status: result.Status, Reads: append([]liveMetricRead(nil), recorder.reads...)}
		for _, evaluation := range result.Data.Evaluations {
			payload, decodeErr := enrich.DecodePayload(evaluation.Data)
			if decodeErr != nil {
				t.Fatal("invalid enrich result")
			}
			// 仅记录状态与指标输出；Display/Resource 的业务载荷不写入报告。
			outputs := make(map[string]any)
			for _, entries := range payload.Processors {
				for name, output := range entries {
					entry := map[string]any{"status": output.Status, "diagnostics": output.Diagnostics}
					if name == "metric" {
						for _, read := range recorder.reads {
							if read.Found && read.Metadata.Unit != "" {
								var unit string
								if json.Unmarshal(output.Value["unit"], &unit) != nil || unit != read.Metadata.Unit {
									t.Errorf("metric unit not projected: strategy=%d", ids.StrategyID)
								}
							}
						}
						values := make(domain.JSONObject)
						for _, key := range []string{"unit", "display_name", "metric_name", "result_table_id"} {
							if value, exists := output.Value[key]; exists {
								values[key] = value
							}
						}
						entry["values"] = values
					}
					outputs[name] = entry
				}
			}
			row.Evaluations = append(row.Evaluations, evaluationReport{Severity: evaluation.Severity, Processors: outputs})
		}
		report = append(report, row)
		t.Logf("strategy=%d status=%s metric_reads=%d", ids.StrategyID, result.Status, len(recorder.reads))
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal("encode live report failed")
	}
	if os.WriteFile(reportFile, raw, 0o600) != nil { //nolint:gosec // G703: 操作者显式指定的本地报告路径，权限为 0600。
		t.Fatal("write live report failed")
	}
}
