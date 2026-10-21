package processors

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestCatalogProcessorsDoNotRequireSourceBusiness(t *testing.T) {
	for _, biz := range []string{"missing", "negative", "different", "invalid"} {
		for _, processor := range []enrich.Processor{Resource{}, Display{}, Metric{}} {
			t.Run(biz+"/"+processor.Name(), func(t *testing.T) {
				reader := &metricLocationReader{dataSliceReader: dataSliceReader{model: "cw-K8s_Container"}, metadata: models.MetricMetadata{Kind: "native", ObjectModelCode: "cw-K8s_Container", FieldName: "usage", TableID: "k8s", PhysicalField: "usage", Unit: "percent"}}
				reader.strategy = reader.strategyForTest()
				reader.strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID = 219
				reader.strategy.Spec.ConfigType = models.CWStrategyConfigTypeData
				event := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-K8s_Container"), rules.FieldModelInstID: domain.NewStringScalar("container-1")})
				switch biz {
				case "missing":
					delete(event.Labels, "bk_biz_id")
				case "negative":
					event.Labels["bk_biz_id"] = mustNumberScalar(t, -255)
				case "different":
					event.Labels["bk_biz_id"] = mustNumberScalar(t, 3)
				case "invalid":
					event.Labels["bk_biz_id"] = domain.NewStringScalar("invalid")
				}
				before := event.Clone()
				reader.instance = enrich.Instance{TenantID: event.BKTenantID, ModelCode: "cw-K8s_Container", InstanceID: "container-1", Fields: map[string]any{"bk_biz_id": int64(2)}, Attributes: map[string]any{"bk_inst_name": "container"}}
				reader.found = true
				business := &strategyBusinessReader{err: context.DeadlineExceeded}
				scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, Business: business})
				if err != nil {
					t.Fatal(err)
				}
				got, err := processor.Process(t.Context(), scope)
				if err != nil || got.Status != domain.EnrichStatusSucceeded {
					t.Fatalf("status=%s diagnostics=%+v error=%v", got.Status, got.Diagnostics, err)
				}
				if business.calls != 0 {
					t.Fatal("unexpected strategy business query")
				}
				if !reflect.DeepEqual(before, event) {
					t.Fatal("source event changed")
				}
			})
		}
	}
}

func TestCatalogDerivedMetricWithoutLegacyFieldTag(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		wantExpression   string
		functions        json.RawMessage
	}{
		{name: "identity", expression: "a"},
		{name: "calculation", expression: "a * 100", wantExpression: "a * 100"},
		{name: "function", expression: "a", functions: json.RawMessage(`[{"id":"rate"}]`), wantExpression: "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &metricLocationReader{metadata: models.MetricMetadata{Kind: "derived", ObjectModelCode: "cw-K8s_Container", FieldName: "container_memory_resource_limits_ratio", FieldCNName: "限制内存使用率", Unit: "percent"}}
			reader.strategy = reader.strategyForTest()
			reader.strategy.Spec.TableID = ""
			reader.strategy.Spec.FieldName = "stale_name"
			reader.strategy.Spec.StrategyItem.Expression = tc.expression
			reader.strategy.Spec.StrategyItem.Functions = tc.functions
			reader.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{QueryConfigType: "catalog", MetricRefID: 219, MetricField: "stale_name", Alias: "a", AggregatePeriod: 60}}
			before, _ := json.Marshal(reader.strategy)
			scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (Metric{}).Process(t.Context(), scope)
			if err != nil || got.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("status=%s diagnostics=%+v error=%v", got.Status, got.Diagnostics, err)
			}
			var values models.MetricValues
			if err := json.Unmarshal(mustRawObject(t, got.Value), &values); err != nil {
				t.Fatal(err)
			}
			paramsRaw, _ := json.Marshal(values.MetricQueryParams)
			var params metricQueryParams
			if err := json.Unmarshal(paramsRaw, &params); err != nil {
				t.Fatal(err)
			}
			if values.MetricName != reader.metadata.FieldName || values.Unit != "percent" || values.ResultTableID != "" || values.MetricUniqueID != "" || params.FieldTag != models.CWStrategyFieldTagDerivedMetric || params.Expression != tc.wantExpression || params.QueryConfigs[0].Metrics[0].Field != reader.metadata.FieldName {
				t.Fatalf("values=%+v params=%+v", values, params)
			}
			after, _ := json.Marshal(reader.strategy)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("strategy mutated")
			}
			if len(reader.queries) != 1 || reader.queries[0] != (models.MetricQuery{TenantID: "tenant-a", MetricID: 219}) {
				t.Fatalf("queries=%+v", reader.queries)
			}
		})
	}
}

func TestUnrelatedProcessorsSkipWithoutBusiness(t *testing.T) {
	reader := &dataSliceReader{}
	event := processorBaseTargetAlert(t, domain.DimensionMap{})
	event.Labels["bk_biz_id"] = mustNumberScalar(t, -255)
	for _, p := range []enrich.Processor{APM{}, Log{}} {
		scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader})
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.Process(t.Context(), scope)
		if err != nil || result.Status != domain.EnrichStatusSkipped {
			t.Fatalf("%s status=%s diagnostics=%+v error=%v", p.Name(), result.Status, result.Diagnostics, err)
		}
	}
}

func TestAPMBranchStillRequiresBusiness(t *testing.T) {
	for _, p := range []enrich.Processor{APM{}, Resource{}} {
		t.Run(p.Name(), func(t *testing.T) {
			reader := &dataSliceReader{table: "namespace_bkapm_metric_app.service", model: rules.APMApplicationModelCode}
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			event.Labels["bk_biz_id"] = mustNumberScalar(t, -255)
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader})
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range got.Diagnostics {
				for _, f := range d.Fields {
					if f == "labels.bk_biz_id" && d.Code == enrich.DiagnosticCodeInvalidField {
						found = true
					}
				}
			}
			if !found || got.Status == domain.EnrichStatusSucceeded {
				t.Fatalf("status=%s diagnostics=%+v", got.Status, got.Diagnostics)
			}
		})
	}
}

func TestDerivedCatalogFailureDoesNotInventPhysicalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		missing  bool
		metadata models.MetricMetadata
	}{
		{name: "missing reference", missing: true},
		{name: "missing derived name", metadata: models.MetricMetadata{Kind: "derived"}},
		{name: "unknown kind", metadata: models.MetricMetadata{Kind: "unknown", FieldName: "usage"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &metricLocationReader{metadata: tc.metadata, missing: tc.missing}
			r.strategy = r.strategyForTest()
			r.strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID = 219
			scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: r, Metric: r})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (Metric{}).Process(t.Context(), scope)
			if err != nil || got.Status != domain.EnrichStatusFailed || len(got.Diagnostics) != 1 || got.Diagnostics[0].Dependency != rules.DependencyMetricCatalog {
				t.Fatalf("result=%+v error=%v", got, err)
			}
		})
	}
}

func TestDerivedReferenceRejectsCatalogKindChange(t *testing.T) {
	r := &metricLocationReader{metadata: models.MetricMetadata{Kind: "native", FieldName: "usage", PhysicalField: "usage", TableID: "system.cpu"}}
	r.strategy = r.strategyForTest()
	r.strategy.Spec.FieldTag = models.CWStrategyFieldTagDerivedMetric
	r.strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID = 219
	scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: r, Metric: r})
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Metric{}).Process(t.Context(), scope)
	if err != nil || got.Status != domain.EnrichStatusFailed {
		t.Fatalf("result=%+v error=%v", got, err)
	}
}
