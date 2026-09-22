// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"linkd/internal/config"
)

func TestMetricCatalogWithoutExporter(t *testing.T) {
	t.Parallel()
	runtime, err := Start(context.Background(), config.TelemetryConfig{}, RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if runtime.PrometheusListenAddress() != "" {
		t.Fatal("unexpected exporter")
	}
	catalog, err := MetricCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.SchemaVersion != 1 || len(catalog.Metrics) == 0 {
		t.Fatal("empty catalog")
	}
	byName := map[string]MetricDefinition{}
	for _, m := range catalog.Metrics {
		if _, exists := byName[m.Name]; exists {
			t.Fatalf("duplicate %s", m.Name)
		}
		byName[m.Name] = m
		if !strings.ContainsFunc(m.DisplayName, func(r rune) bool { return unicode.Is(unicode.Han, r) }) || m.Description == "" || m.UnitLabel == "" || len(m.Series) == 0 || m.Dimensions == nil {
			t.Fatalf("incomplete metadata: %+v", m)
		}
	}
	for _, name := range []string{"linkd.enrich.attempt.duration", "linkd.elasticsearch.write_batch.operations", "linkd.dispatch.worker.tasks", "go_goroutines", "target_info"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing inactive metric %s", name)
		}
	}
	if byName["linkd.enrich.inflight"].PrometheusType != "gauge" || byName["linkd.enrich.inflight"].Type != "up_down_counter" {
		t.Fatal("up/down counter semantics lost")
	}
	if byName["linkd.enrich.attempt.duration"].PrometheusName != "linkd_enrich_attempt_duration_seconds" {
		t.Fatal("incorrect histogram name")
	}
	if byName["linkd.cleaner.backpressure.paused"].PrometheusName != "linkd_cleaner_backpressure_paused_ratio" {
		t.Fatal("incorrect unit suffix")
	}
	if !slices.Contains(byName["linkd.enrich.attempt.duration"].Series, "linkd_enrich_attempt_duration_seconds_bucket") {
		t.Fatal("histogram series missing")
	}
}

func TestInstrumentMetadataValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		info        metricInfo
		description string
	}{
		{"missing title", describeMetric("", "enrich", "latency"), "说明"},
		{"missing description", describeMetric("耗时", "enrich", "latency"), ""},
		{"unknown module", describeMetric("耗时", "unknown", "latency"), "说明"},
		{"unknown purpose", describeMetric("耗时", "enrich", "unknown"), "说明"},
		{"unknown dimension", describeMetric("耗时", "enrich", "latency", "alert_id"), "说明"},
		{"duplicate dimension", describeMetric("耗时", "enrich", "latency", "linkd.status", "linkd.status"), "说明"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &instrumentRegistry{meter: noop.NewMeterProvider().Meter("test")}
			_, err := r.Int64Counter("linkd.test", tc.info, metric.WithDescription(tc.description))
			if err == nil {
				t.Fatal("expected invalid metadata rejection")
			}
		})
	}
	r := &instrumentRegistry{meter: noop.NewMeterProvider().Meter("test")}
	for i, name := range []string{"linkd.test", "linkd_test"} {
		_, err := r.Int64Counter(name, describeMetric("测试次数", "enrich", "throughput"), metric.WithDescription("测试说明"))
		if (i == 0) != (err == nil) {
			t.Fatalf("name collision check: %v", err)
		}
	}
}

// 所有业务 instrument 必须走带描述的注册构造器，且声明必须出现在目录中。
// 这也能拦住新增的按需注册路径，避免再次出现只在启用后才可查询的目录。
func TestMetricDeclarationsAreRegistered(t *testing.T) {
	catalog, err := MetricCatalog()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, m := range catalog.Metrics {
		if m.Origin == "linkd" {
			registered[m.Name] = true
		}
	}
	declared := map[string]bool{}
	err = filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == filepath.Join("..", "telemetry", "instrument_registry.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if !slices.Contains([]string{"Int64Counter", "Float64Counter", "Int64UpDownCounter", "Float64UpDownCounter", "Int64Histogram", "Float64Histogram", "Int64Gauge", "Float64Gauge", "Int64ObservableCounter", "Float64ObservableCounter", "Int64ObservableGauge", "Float64ObservableGauge", "Int64ObservableUpDownCounter", "Float64ObservableUpDownCounter"}, selector.Sel.Name) {
				return true
			}
			if len(call.Args) < 2 {
				t.Errorf("%s: instrument missing metadata", path)
				return true
			}
			name, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s: metric name must be a literal", path)
				return true
			}
			value, err := strconv.Unquote(name.Value)
			if err != nil {
				t.Error(err)
				return true
			}
			metadata, ok := call.Args[1].(*ast.CallExpr)
			if !ok {
				t.Errorf("%s: metric %s bypasses metadata", path, value)
				return true
			}
			fn, ok := metadata.Fun.(*ast.Ident)
			if !ok || fn.Name != "describeMetric" {
				t.Errorf("%s: metric %s bypasses registry", path, value)
			}
			if !registered[value] {
				t.Errorf("%s: metric %s not included in catalog", path, value)
			}
			declared[value] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) != len(registered) {
		t.Fatalf("declared %d, registered %d", len(declared), len(registered))
	}
}

func assertScrapeMatchesCatalog(t *testing.T, body string) {
	t.Helper()
	catalog, err := MetricCatalog()
	if err != nil {
		t.Fatal(err)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, family := range families {
		index := slices.IndexFunc(catalog.Metrics, func(m MetricDefinition) bool { return m.PrometheusName == name })
		if index < 0 {
			t.Errorf("scraped family %s absent from catalog", name)
			continue
		}
		definition := catalog.Metrics[index]
		if definition.PrometheusType != strings.ToLower(family.GetType().String()) {
			t.Errorf("%s type mismatch", name)
		}
		if definition.Origin != "linkd" {
			continue
		}
		if family.GetHelp() != definition.Description {
			t.Errorf("%s description differs from HELP", name)
		}
		for _, sample := range family.Metric {
			for _, label := range sample.Label {
				known := func(d MetricDimension) bool { return d.PrometheusName == label.GetName() }
				if !slices.ContainsFunc(definition.Dimensions, known) && !slices.ContainsFunc(catalog.CommonDimensions, known) {
					t.Errorf("%s missing dimension %s", name, label.GetName())
				}
			}
		}
	}
}
