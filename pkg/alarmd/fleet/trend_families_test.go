// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet_test

import (
	"regexp"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

var alarmdFamily = regexp.MustCompile(`(?:^|[^a-z0-9_])(alarmd_[a-z0-9_]+)`)

// The platform registers a field when it first sees a sample of it, so a
// family a trend line reads must be on /metrics from the start, before any
// work: a counter whose labels nothing has used yet exports nothing, and its
// line would read "field does not exist" until the thing it counts first
// happens. The self-space probe is held to the same.
func TestEveryFamilyATrendReadsIsExportedBeforeAnyWork(t *testing.T) {
	families, err := metric.NewRecorder(metric.BuildInfo{}).Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	exported := map[string]*dto.MetricFamily{}
	for _, family := range families {
		if len(family.GetMetric()) > 0 {
			exported[family.GetName()] = family
		}
	}
	scope := fleet.TrendScope{Namespace: "alarmd-ns", Container: "alarmd"}
	expressions := map[string]string{"self space probe": fleet.SelfSpaceProbe}
	for _, definition := range fleet.TrendCatalog {
		if definition.Space != fleet.TrendSpaceSelf {
			continue
		}
		for _, line := range definition.Lines {
			expressions[definition.Key+"/"+line.Name] = line.PromQL(scope, "5m")
			if line.Fallback != nil {
				expressions[definition.Key+"/"+line.Name+" fallback"] = line.Fallback(scope, "5m")
			}
		}
	}
	if len(expressions) < 2 {
		t.Fatal("the trend catalog has no self-space line")
	}
	for where, expression := range expressions {
		matches := alarmdFamily.FindAllStringSubmatch(expression, -1)
		if len(matches) == 0 {
			t.Errorf("%s reads no alarmd family: %s", where, expression)
		}
		for _, match := range matches {
			if !exportedFamily(exported, match[1]) {
				t.Errorf("%s reads %s, which a fresh process does not export", where, match[1])
			}
		}
	}
}

// exportedFamily also accepts a histogram's _bucket, _sum and _count series,
// which the registry gathers under the family's own name.
func exportedFamily(exported map[string]*dto.MetricFamily, name string) bool {
	if exported[name] != nil {
		return true
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if base, ok := strings.CutSuffix(name, suffix); ok {
			if family := exported[base]; family != nil && family.GetType() == dto.MetricType_HISTOGRAM {
				return true
			}
		}
	}
	return false
}

// The late-objects chart reads every class the census publishes and nothing
// else: one line per class the recorder sets, by the label value it sets.
func TestTheLateObjectsChartReadsEachCensusClass(t *testing.T) {
	var lines []fleet.TrendLine
	for _, definition := range fleet.TrendCatalog {
		if definition.Key == "late_objects" {
			lines = definition.Lines
		}
	}
	if len(lines) != len(metric.ScheduleObjectClasses) {
		t.Fatalf("late_objects has %d lines, want one per census class %v", len(lines), metric.ScheduleObjectClasses)
	}
	scope := fleet.TrendScope{Namespace: "alarmd-ns", Container: "alarmd"}
	for index, class := range metric.ScheduleObjectClasses {
		expression := lines[index].PromQL(scope, "5m")
		if want := `sum(alarmd_schedule_objects{class="` + class + `"})`; expression != want {
			t.Errorf("line %q reads %s, want %s", lines[index].Name, expression, want)
		}
	}
}
