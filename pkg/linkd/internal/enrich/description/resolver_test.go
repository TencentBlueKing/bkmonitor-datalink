// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package description

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

type configurationReaderFunc func(context.Context, ConfigurationQuery) (Configuration, error)

func (f configurationReaderFunc) ReadConfiguration(ctx context.Context, q ConfigurationQuery) (Configuration, error) {
	return f(ctx, q)
}

func resolverFixture(t *testing.T) (domain.Event, domain.EventEvaluation, domain.Alert, Configuration) {
	t.Helper()
	evaluation := domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered}
	event := domain.Event{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 7, EventID: "event", Fingerprint: "group", Evaluations: []domain.EventEvaluation{evaluation}, Values: domain.EventValues{"value": 99}, SourceRawData: domain.JSONObject{"values": json.RawMessage(`{"value":99}`)}, Labels: domain.DimensionMap{}}
	for key, value := range map[string]float64{"strategy_id": 1, "strategy_version": 1790758008036099, "bk_biz_id": 2} {
		scalar, err := domain.NewNumberScalar(value)
		if err != nil {
			t.Fatal(err)
		}
		event.Labels[key] = scalar
	}
	opening := domain.Alert{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 7, TriggerEventID: "event", Fingerprint: "group", Severity: "warning"}
	configuration := Configuration{Identity: ConfigurationQuery{"tenant", 1, 1790758008036099, 2}, Spec: models.CWStrategySpec{Name: "CPU", StrategyItem: &models.CWStrategyItem{AggregateMethod: "AVG", Connector: "and", QueryConfigs: []models.StrategyQueryConfig{{}}}}, Queries: []models.StrategyQueryConfig{{Unit: "percent"}}, Algorithms: []RuntimeAlgorithm{{Type: "Threshold", Level: 1, UnitPrefix: "%", Config: json.RawMessage(`[[{"method":"gt","threshold":100}]]`)}, {Type: "Threshold", Level: 2, UnitPrefix: "%", Config: json.RawMessage(`[[{"method":"gt","threshold":80}]]`)}}}
	return event, evaluation, opening, configuration
}

func TestResolverRendersOnlySelectedLevelWithPublishedUnit(t *testing.T) {
	t.Parallel()
	event, evaluation, opening, configuration := resolverFixture(t)
	resolver, err := NewResolver(configurationReaderFunc(func(ctx context.Context, q ConfigurationQuery) (Configuration, error) {
		if ctx != t.Context() || q != configuration.Identity {
			t.Fatal("wrong identity/context")
		}
		return configuration, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(facts)
	if err != nil || got != "AVG(CPU) > 80.0%, 当前值99%" {
		t.Fatalf("content=%q error=%v", got, err)
	}
}

func TestResolverRejectsUnprovenFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		change     func(*domain.Event, *Configuration)
	}{
		{"missing value", "observed_value_missing", func(e *domain.Event, _ *Configuration) { delete(e.Values, "value") }},
		{"missing raw type", "observed_number_type_missing", func(e *domain.Event, _ *Configuration) { e.SourceRawData = nil }},
		{"changed value", "observed_value_mismatch", func(e *domain.Event, _ *Configuration) { e.Values["value"] = 100 }},
		{"wrong configuration identity", "configuration_identity_mismatch", func(_ *domain.Event, c *Configuration) { c.Identity.TenantID = "other" }},
		{"special query rewrite", "special_query_rewrite_unverified", func(_ *domain.Event, c *Configuration) { c.Queries[0].MetricID = "bk_monitor.ping-gse" }},
		{"process", "process_evidence_missing", func(_ *domain.Event, c *Configuration) { c.Algorithms[1].Type = "ProcPort" }},
		{"ambiguous unit", "query_unit_ambiguous", func(_ *domain.Event, c *Configuration) {
			c.Queries = append(c.Queries, models.StrategyQueryConfig{Unit: "s"})
		}},
		{"observed unit", "observed_unit_mismatch", func(e *domain.Event, _ *Configuration) {
			e.ExtraData = domain.JSONObject{"unit": json.RawMessage(`"s"`)}
		}},
		{"wrong family", "detection_evidence_missing", func(e *domain.Event, _ *Configuration) {
			e.ExtraData = domain.JSONObject{"evaluation_family": json.RawMessage(`"nodata"`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, configuration := resolverFixture(t)
			tc.change(&event, &configuration)
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(t.Context(), event, evaluation, opening)
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("error=%v want=%s", err, tc.code)
			}
		})
	}
}

func noDataResolverFixture(t *testing.T) (domain.Event, domain.EventEvaluation, domain.Alert, Configuration) {
	t.Helper()
	event, evaluation, opening, configuration := resolverFixture(t)
	event.Dimensions = domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true)}
	event.Values = nil
	event.SourceRawData = nil
	event.ExtraData = domain.JSONObject{
		"evaluation_family": json.RawMessage(`"no_data"`),
		"no_data_periods":   json.RawMessage(`196`),
		// 滚动窗口的计数和起点不能作为累计周期数或延时的来源。
		"window":             json.RawMessage(`{"size":5,"anomalies":5,"required":5}`),
		"anomaly_begin_time": json.RawMessage(`"2026-10-13T18:08:00Z"`),
	}
	configuration.Spec.StrategyItem.NoDataConfig = domain.JSONObject{
		"is_enabled": json.RawMessage(`true`), "level": json.RawMessage(`2`), "continuous": json.RawMessage(`5`),
	}
	// 无数据等级独立于普通指标算法，专用指标的测量值改写也不参与无数据描述。
	configuration.Algorithms = nil
	configuration.Queries[0].MetricID = "bk_monitor.os_restart"
	return event, evaluation, opening, configuration
}

func TestResolverNoDataUsesReportedPeriodsWithoutMeasurementOrDelay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ periods, want string }{
		{"1", "当前指标(AVG(CPU))已经有1个周期无数据上报"},
		{"196", "当前指标(AVG(CPU))已经有196个周期无数据上报"},
		{"1000000", "当前指标(AVG(CPU))已经有1000000个周期无数据上报"},
	} {
		t.Run(tc.periods, func(t *testing.T) {
			event, evaluation, opening, configuration := noDataResolverFixture(t)
			event.ExtraData["no_data_periods"] = json.RawMessage(tc.periods)
			before, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
			if err != nil {
				t.Fatal(err)
			}
			builder, err := NewBuilder(resolver)
			if err != nil {
				t.Fatal(err)
			}
			got, err := builder.BuildContent(t.Context(), event, evaluation, opening)
			if err != nil || got != tc.want {
				t.Fatalf("content=%q error=%v want=%q", got, err, tc.want)
			}
			after, err := json.Marshal(event)
			if err != nil || string(after) != string(before) {
				t.Fatal("description changed source Event")
			}
		})
	}
}

func TestResolverNoDataRejectsMissingInvalidOrConflictingFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		change     func(*domain.Event, *Configuration)
	}{
		{"missing periods", "nodata_evidence_missing", func(e *domain.Event, _ *Configuration) { delete(e.ExtraData, "no_data_periods") }},
		{"missing family", "nodata_identity_invalid", func(e *domain.Event, _ *Configuration) { delete(e.ExtraData, "evaluation_family") }},
		{"wrong family", "nodata_identity_invalid", func(e *domain.Event, _ *Configuration) {
			e.ExtraData["evaluation_family"] = json.RawMessage(`"metric_algorithm"`)
		}},
		{"missing marker", "nodata_identity_invalid", func(e *domain.Event, _ *Configuration) { e.Dimensions = nil }},
		{"false marker", "nodata_identity_invalid", func(e *domain.Event, _ *Configuration) {
			e.Dimensions["__NO_DATA_DIMENSION__"] = domain.NewBoolScalar(false)
		}},
		{"disabled no data", "nodata_configuration_invalid", func(_ *domain.Event, c *Configuration) {
			c.Spec.StrategyItem.NoDataConfig["is_enabled"] = json.RawMessage(`false`)
		}},
		{"wrong level", "nodata_configuration_invalid", func(_ *domain.Event, c *Configuration) {
			c.Spec.StrategyItem.NoDataConfig["level"] = json.RawMessage(`1`)
		}},
		{"missing level", "nodata_configuration_invalid", func(_ *domain.Event, c *Configuration) { delete(c.Spec.StrategyItem.NoDataConfig, "level") }},
		{"missing item", "strategy_item_invalid", func(_ *domain.Event, c *Configuration) { c.Spec.StrategyItem = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, configuration := noDataResolverFixture(t)
			tc.change(&event, &configuration)
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(t.Context(), event, evaluation, opening)
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("error=%v want=%s", err, tc.code)
			}
		})
	}
	for _, raw := range []string{`null`, `true`, `"196"`, `0`, `-1`, `1.5`, `1000001`, `9223372036854775808`, `{}`, `[]`} {
		t.Run("periods="+raw, func(t *testing.T) {
			event, evaluation, opening, configuration := noDataResolverFixture(t)
			event.ExtraData["no_data_periods"] = json.RawMessage(raw)
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(t.Context(), event, evaluation, opening)
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != "nodata_evidence_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestResolverFalseNoDataMarkerKeepsMetricDescription(t *testing.T) {
	t.Parallel()
	event, evaluation, opening, configuration := resolverFixture(t)
	event.Dimensions = domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(false)}
	event.ExtraData = domain.JSONObject{"evaluation_family": json.RawMessage(`"metric_algorithm"`)}
	resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(facts)
	if err != nil || got != "AVG(CPU) > 80.0%, 当前值99%" {
		t.Fatalf("content=%q error=%v", got, err)
	}
}

func TestResolverPreservesWireNumberType(t *testing.T) {
	t.Parallel()
	event, _, _, _ := resolverFixture(t)
	event.Values["value"] = 10
	for _, raw := range []string{"10", "10.0"} {
		event.SourceRawData["values"] = json.RawMessage(`{"value":` + raw + `}`)
		value, err := eventObservedNumber(event)
		if err != nil || value.integer != (raw == "10") {
			t.Fatalf("raw=%s value=%v error=%v", raw, value, err)
		}
	}
}

func TestResolverDependencyAndCancellation(t *testing.T) {
	t.Parallel()
	event, evaluation, opening, _ := resolverFixture(t)
	dependency := errors.New("unavailable")
	calls := 0
	resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) {
		calls++
		return Configuration{}, dependency
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(t.Context(), event, evaluation, opening); !errors.Is(err, dependency) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver.Resolve(ctx, event, evaluation, opening); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err)
	}
	if _, err := NewResolver(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestConfigurationThresholdAcceptsPublishedNumericStrings(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`[[{"method":"gte","threshold":"90"}]]`, `[[{"method":"gte","threshold":90}]]`} {
		groups, err := configurationThreshold(json.RawMessage(raw))
		if err != nil || len(groups) != 1 || len(groups[0]) != 1 || groups[0][0].Threshold != 90 {
			t.Fatalf("groups=%v error=%v", groups, err)
		}
	}
	for _, raw := range []string{`[[{"method":"gte","threshold":"NaN"}]]`, `[[{"method":"gte","threshold":null}]]`, `[[{"method":"gte","threshold":true}]]`} {
		if _, err := configurationThreshold(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid threshold accepted")
		}
	}
}

func TestResolverPingUsesOriginalLossValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, content string }{
		{"unreachable", "1", "Ping不可达"},
		{"loss float", "1.0", "Ping不可达"},
		{"reachable", "0.5", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, configuration := resolverFixture(t)
			number, err := ParseNumber(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			event.Values["value"] = number.value
			event.SourceRawData["values"] = json.RawMessage(`{"value":` + tc.raw + `}`)
			configuration.Spec.MonitorItemType = "event"
			configuration.Spec.StrategyItem.AggregateMethod = ""
			configuration.Spec.StrategyItem.QueryConfigs = nil
			configuration.Queries = []models.StrategyQueryConfig{{MetricID: "bk_monitor.ping-gse", Unit: "percentunit"}}
			configuration.Algorithms = []RuntimeAlgorithm{{Type: "PingUnreachable", Level: 2}}
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
			if err != nil {
				t.Fatal(err)
			}
			facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Render(facts)
			if content != tc.content || ((err != nil) != (tc.content == "")) {
				t.Fatalf("content=%q error=%v", content, err)
			}
		})
	}
}

func TestConfigurationItemNameUsesPublishedDisplayFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want, code string
		change           func(*models.CWStrategySpec)
	}{
		{name: "item aggregate", want: "AVG(CPU)"},
		{name: "query aggregate", want: "SUM(CPU)", change: func(s *models.CWStrategySpec) {
			s.StrategyItem.AggregateMethod = ""
			s.StrategyItem.QueryConfigs[0].AggregateMethod = "SUM"
		}},
		{name: "alias", want: "AVG(alias)", change: func(s *models.CWStrategySpec) { s.Name = " "; s.AliasName = "alias" }},
		{name: "derived without aggregate", want: "CPU", change: func(s *models.CWStrategySpec) {
			s.FieldTag = models.CWStrategyFieldTagDerivedMetric
			s.StrategyItem.AggregateMethod = ""
		}},
		{name: "promql", want: "sum(cpu)", change: func(s *models.CWStrategySpec) { s.Name = ""; s.StrategyItem.QueryConfigs[0].PromQL = "sum(cpu)" }},
		{name: "missing name", code: "item_name_missing", change: func(s *models.CWStrategySpec) { s.Name = "" }},
		{name: "missing aggregate", code: "aggregate_method_missing", change: func(s *models.CWStrategySpec) { s.StrategyItem.AggregateMethod = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, configuration := resolverFixture(t)
			if tc.change != nil {
				tc.change(&configuration.Spec)
			}
			got, err := configurationItemName(configuration.Spec)
			if tc.code != "" {
				var failure *Error
				if !errors.As(err, &failure) || failure.Code != tc.code {
					t.Fatalf("error=%v want=%s", err, tc.code)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("name=%q error=%v want=%q", got, err, tc.want)
			}
		})
	}
}
