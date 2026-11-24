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
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

// 输入按 alarmd 默认注册算法及标准事件字段构造，预期独立于渲染实现维护。
func TestResolverSupportsAlarmdAlgorithms(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, config, want string
	}{
		{"Threshold", `[[{"method":"gt","threshold":80}]]`, "AVG(CPU) > 80.0%, 当前值99%"},
		{"SimpleRingRatio", `{"floor":10,"ceil":20}`, "AVG(CPU) 检测条件：（较前一时刻下降达到10.0%或较前一时刻上升达到20.0%）, 当前值99%"},
		{"SimpleYearRound", `{"ceil":20}`, "AVG(CPU) 检测条件：（较上周同一时刻上升达到20.0%）, 当前值99%"},
		{"AdvancedRingRatio", `{"floor":10,"floor_interval":3,"ceil":0,"ceil_interval":null}`, "AVG(CPU) 检测条件：（较前3个时间点的均值下降达到10.0%）, 当前值99%"},
		{"AdvancedYearRound", `{"floor":10,"floor_interval":3,"ceil":20,"ceil_interval":7,"fetch_type":"last"}`, "AVG(CPU) 检测条件：（较前3天内同一时刻绝对值的瞬间值下降达到10.0%或较前7天内同一时刻绝对值的瞬间值上升达到20.0%）, 当前值99%"},
		{"RingRatioAmplitude", `{"ratio":1.5,"shock":0,"threshold":10}`, "AVG(CPU) 检测条件：（当前值与前一时刻值均 >= 10.0%，两者差值的绝对值 >= 前一时刻值 × 1.5 + 0.0%）, 当前值99%"},
		{"YearRoundAmplitude", `{"days":3,"method":"gte","ratio":1.5,"shock":2}`, "AVG(CPU) 检测条件：（当前值与前一时刻值的差值绝对值 >= 前3天内任意一天同一时刻差值的绝对值 × 1.5 + 2.0%）, 当前值99%"},
		{"YearRoundRange", `{"days":7,"method":"lt","ratio":0.5,"shock":2}`, "AVG(CPU) 检测条件：（当前值的绝对值 < 前7天内任意一天同一时刻值的绝对值 × 0.5 + 2.0%）, 当前值99%"},
		{"OsRestart", `{}`, "当前服务器在99秒前发生系统重启事件"},
		{"ProcPort", `{}`, "当前进程(nginx)不存在"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			event, evaluation, opening, config := resolverFixture(t)
			config.Algorithms = []RuntimeAlgorithm{{Type: tc.kind, Level: 2, UnitPrefix: "%", Config: json.RawMessage(tc.config)}}
			if tc.kind == "ProcPort" {
				event.Values["value"] = 0
				event.SourceRawData["values"] = json.RawMessage(`{"value":0}`)
				event.Dimensions = domain.DimensionMap{"display_name": domain.NewStringScalar("nginx")}
			}
			if tc.kind == "OsRestart" || tc.kind == "ProcPort" {
				config.Queries[0].MetricID = map[string]string{"OsRestart": "bk_monitor.os_restart", "ProcPort": "bk_monitor.proc_port"}[tc.kind]
			}
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return config, nil }))
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
		})
	}
}

func TestDerivedMetricWithoutTopLevelAggregationUsesMetricName(t *testing.T) {
	t.Parallel()
	event, evaluation, opening, config := resolverFixture(t)
	config.Spec.Name = "常驻内存集（rss）使用量"
	config.Spec.FieldTag = models.CWStrategyFieldTagDerivedMetric
	config.Spec.StrategyItem.AggregateMethod = ""
	config.Algorithms = config.Algorithms[1:]
	resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return config, nil }))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(facts)
	if err != nil || got != "常驻内存集（rss）使用量 > 80.0%, 当前值99%" {
		t.Fatalf("content=%q error=%v", got, err)
	}
}

func TestComparisonConditionsPreserveAlgorithmConnector(t *testing.T) {
	for _, tc := range []struct{ connector, want string }{
		{"and", "CPU 检测条件：（较前一时刻上升达到20.0%）且 检测条件：（较上周同一时刻下降达到10.0%）, 当前值99"},
		{"or", "CPU 检测条件：（较前一时刻上升达到20.0%）或 检测条件：（较上周同一时刻下降达到10.0%）, 当前值99"},
	} {
		t.Run(tc.connector, func(t *testing.T) {
			got, err := Render(Facts{ItemName: "CPU", Value: number(t, "99"), Connector: tc.connector, Algorithms: []Algorithm{{Type: "SimpleRingRatio", Config: json.RawMessage(`{"ceil":20}`)}, {Type: "SimpleYearRound", Config: json.RawMessage(`{"floor":10}`)}}})
			if err != nil || got != tc.want {
				t.Fatalf("content=%q error=%v", got, err)
			}
		})
	}
}

func TestComparisonIncompleteConfigDoesNotInventHistory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, config string }{
		{"SimpleYearRound", `{}`}, {"AdvancedYearRound", `{"ceil":10}`},
		{"AdvancedRingRatio", `{"ceil":10,"ceil_interval":2,"fetch_type":"unknown"}`},
		{"YearRoundRange", `{"days":3,"method":"gt","ratio":2}`},
		{"RingRatioAmplitude", `{"ratio":2,"shock":1}`}, {"SimpleRingRatio", `null`},
	} {
		t.Run(tc.kind+tc.config, func(t *testing.T) {
			_, err := Render(Facts{ItemName: "CPU", Value: number(t, "99"), Connector: "and", Algorithms: []Algorithm{{Type: tc.kind, Config: json.RawMessage(tc.config)}}})
			if err == nil {
				t.Fatal("rendered incomplete comparison")
			}
		})
	}
}

func TestResolverProcPortUsesEventDimensions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ ports, inaccurate, bind, want string }{
		{`[80,81,443]`, `[]`, "", "当前进程(nginx)存在，端口([80-81,443])不存在"},
		{`[]`, `[80]`, "127.0.0.1", "当前进程(nginx)和监听端口([80])存在，监听的IP与CMDB中配置的(127.0.0.1)不符"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			event, evaluation, opening, config := resolverFixture(t)
			event.Values["value"] = 1
			event.SourceRawData["values"] = json.RawMessage(`{"value":1}`)
			event.Dimensions = domain.DimensionMap{"display_name": domain.NewStringScalar("nginx"), "nonlisten": domain.NewStringScalar(tc.ports), "not_accurate_listen": domain.NewStringScalar(tc.inaccurate), "bind_ip": domain.NewStringScalar(tc.bind)}
			config.Algorithms = []RuntimeAlgorithm{{Type: "ProcPort", Level: 2}}
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return config, nil }))
			if err != nil {
				t.Fatal(err)
			}
			facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Render(facts)
			if err != nil || got != tc.want {
				t.Fatalf("content=%q error=%v", got, err)
			}
		})
	}
}
