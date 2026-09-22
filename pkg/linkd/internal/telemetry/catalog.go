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
	"fmt"
	"slices"
	"strings"

	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel/metric/noop"
)

// MetricDimension 描述可出现的维度；不同观察路径不保证同时携带所有维度。
type MetricDimension struct {
	Name           string `json:"name"`            // Name 是原始属性名。
	PrometheusName string `json:"prometheus_name"` // PrometheusName 是导出的标签名。
	Description    string `json:"description"`     // Description 说明维度含义和可选条件。
}

// MetricDefinition 是指标定义，不包含采样值、标签值或实例配置。
type MetricDefinition struct {
	Name           string            `json:"name"`            // Name 是注册名；原生 Prometheus 指标使用 family 名。
	PrometheusName string            `json:"prometheus_name"` // PrometheusName 是可查询的 family 名。
	DisplayName    string            `json:"display_name"`    // DisplayName 是中文短名称。
	Module         string            `json:"module"`          // Module 对应 Catalog.Modules 中的分类。
	Purpose        string            `json:"purpose"`         // Purpose 对应 Catalog.Purposes 中的用途。
	Type           string            `json:"type"`            // Type 保留 OTel up_down_counter 与 gauge 的区别。
	PrometheusType string            `json:"prometheus_type"` // PrometheusType 是导出的指标类型。
	Unit           string            `json:"unit"`            // Unit 保留注册时的单位，空串表示没有声明单位。
	UnitLabel      string            `json:"unit_label"`      // UnitLabel 是便于阅读的中文单位。
	Description    string            `json:"description"`     // Description 说明统计口径；业务指标与采集端 HELP 共用定义。
	Dimensions     []MetricDimension `json:"dimensions"`      // Dimensions 不含公共 scope 和采集端附加标签。
	Series         []string          `json:"series"`          // Series 包含 histogram、summary 的实际查询序列名。
	Origin         string            `json:"origin"`          // Origin 区分 linkd、runtime 和 exporter。
}

// MetricCategory 是稳定分类键和中文名称。
type MetricCategory struct {
	ID   string `json:"id"`   // ID 用于过滤。
	Name string `json:"name"` // Name 用于展示。
}

// Catalog 包含当前二进制的指标定义；目录存在不代表模块已启用或已经产生样本。
type Catalog struct {
	SchemaVersion    int                `json:"schema_version"`    // SchemaVersion 是目录响应格式版本。
	Metrics          []MetricDefinition `json:"metrics"`           // Metrics 按注册名排序。
	Modules          []MetricCategory   `json:"modules"`           // Modules 是功能分类。
	Purposes         []MetricCategory   `json:"purposes"`          // Purposes 是用途分类。
	CommonDimensions []MetricDimension  `json:"common_dimensions"` // CommonDimensions 仅适用于 linkd 来源的 OTel 指标。
	Notes            []string           `json:"notes"`             // Notes 说明目录和实际采集之间的边界。
}

func metricModules() []MetricCategory {
	return []MetricCategory{
		{"pipeline", "处理流水线"}, {"messaging", "消息消费"}, {"cleaner", "接入清洗"},
		{"lifecycle", "告警生命周期"}, {"enrich", "告警丰富"}, {"final_hook", "结果输出"},
		{"store", "存储访问"}, {"elasticsearch", "ES 批次读写"}, {"archiver", "告警归档"},
		{"redis_stream", "Redis Stream"}, {"control_plane", "控制面任务"}, {"dispatch", "任务调度"},
		{"go", "Go 运行时"}, {"process", "进程资源"}, {"exporter", "采集元信息"},
	}
}

func metricPurposes() []MetricCategory {
	return []MetricCategory{{"throughput", "吞吐与结果"}, {"latency", "耗时与时效"}, {"capacity", "容量与积压"}, {"reliability", "可靠性与错误"}, {"state", "状态与配置"}, {"resources", "资源使用"}}
}

// MetricCatalog 复用实际 instrument 注册流程构造完整目录，无需启动 exporter 或连接业务存储。
// Go/process 目录从与运行时相同的 collector 发现，具体条目受构建平台与 Go 版本影响。
func MetricCatalog() (Catalog, error) {
	r := &instrumentRegistry{meter: noop.NewMeterProvider().Meter(instrumentationKey)}
	if _, err := newRegisteredInstruments(r); err != nil {
		return Catalog{}, err
	}
	builtins, err := builtinMetricDefinitions()
	if err != nil {
		return Catalog{}, err
	}
	metrics := append(r.definitions, builtins...)
	slices.SortFunc(metrics, func(a, b MetricDefinition) int { return strings.Compare(a.Name, b.Name) })
	return Catalog{
		SchemaVersion: 1, Metrics: metrics, Modules: metricModules(), Purposes: metricPurposes(),
		CommonDimensions: []MetricDimension{
			{"otel_scope_name", "otel_scope_name", "OTel instrumentation scope，Linkd 固定为 linkd"},
			{"otel_scope_version", "otel_scope_version", "OTel scope 版本，当前为空字符串"},
			{"otel_scope_schema_url", "otel_scope_schema_url", "OTel scope schema URL，当前为空字符串"},
		},
		Notes: []string{
			"目录表示当前二进制可提供的指标，不代表模块已启用、已有样本或已被 Prometheus 采集。",
			"维度列出可能出现的属性；可选属性并非每个样本都有。公共 scope 标签仅适用于 Linkd 业务指标。",
			"Histogram 的 _bucket 序列另带 le 标签；Summary 的分位数序列另带 quantile 标签。",
			"job、instance 等抓取标签由 Prometheus 配置决定；服务版本、角色等 Resource 属性位于 target_info。",
			"Go/process 指标自动发现于同一套内置 collector；条目会随平台、Go 版本和依赖版本变化。",
		},
	}, nil
}

type metricInfo struct {
	displayName, module, purpose string
	dimensions                   []string
}

func describeMetric(name, module, purpose string, dimensions ...string) metricInfo {
	return metricInfo{name, module, purpose, dimensions}
}

// exporter 与目录显式共用命名策略，避免依赖升级后目录名称与实际序列静默分叉。
func metricTranslationStrategy() otlptranslator.TranslationStrategyOption {
	return otlptranslator.UnderscoreEscapingWithSuffixes
}

func (r *instrumentRegistry) register(name, kind, unit, description string, info metricInfo) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(info.displayName) == "" || strings.TrimSpace(description) == "" {
		return fmt.Errorf("metric %q requires name, Chinese display name and description", name)
	}
	if !slices.ContainsFunc(metricModules(), func(c MetricCategory) bool { return c.ID == info.module }) ||
		!slices.ContainsFunc(metricPurposes(), func(c MetricCategory) bool { return c.ID == info.purpose }) {
		return fmt.Errorf("metric %q has unknown module or purpose", name)
	}
	translatorType := otlptranslator.MetricType(otlptranslator.MetricTypeGauge)
	promType := "gauge"
	switch kind {
	case "counter":
		translatorType, promType = otlptranslator.MetricTypeMonotonicCounter, "counter"
	case "histogram":
		translatorType, promType = otlptranslator.MetricTypeHistogram, "histogram"
	case "up_down_counter":
		translatorType = otlptranslator.MetricTypeNonMonotonicCounter
	}
	namer := otlptranslator.NewMetricNamer("", metricTranslationStrategy())
	promName, err := namer.Build(otlptranslator.Metric{Name: name, Unit: unit, Type: translatorType})
	if err != nil {
		return err
	}
	for _, existing := range r.definitions {
		if existing.Name == name || existing.PrometheusName == promName {
			return fmt.Errorf("duplicate metric name %q", name)
		}
	}
	dimensions := make([]MetricDimension, 0, len(info.dimensions))
	for _, key := range info.dimensions {
		d, ok := metricDimension(key)
		if !ok {
			return fmt.Errorf("metric %q has undocumented dimension %q", name, key)
		}
		if slices.ContainsFunc(dimensions, func(v MetricDimension) bool { return v.Name == key }) {
			return fmt.Errorf("metric %q has duplicate dimension %q", name, key)
		}
		dimensions = append(dimensions, d)
	}
	r.definitions = append(r.definitions, MetricDefinition{
		Name: name, PrometheusName: promName, DisplayName: info.displayName, Module: info.module, Purpose: info.purpose,
		Type: kind, PrometheusType: promType, Unit: unit, UnitLabel: metricUnitLabel(unit), Description: description,
		Dimensions: dimensions, Series: metricSeries(promName, promType), Origin: "linkd",
	})
	return nil
}

func metricSeries(name, kind string) []string {
	switch kind {
	case "histogram":
		return []string{name + "_bucket", name + "_sum", name + "_count"}
	case "summary":
		return []string{name, name + "_sum", name + "_count"}
	default:
		return []string{name}
	}
}

func metricUnitLabel(unit string) string {
	labels := map[string]string{"": "未声明", "1": "无量纲", "s": "秒", "By": "字节", "%": "百分比", "{attempt}": "次尝试", "{event}": "事件", "{retry}": "次重试", "{message}": "消息", "{operation}": "次操作", "{transition}": "次转换", "{item}": "项", "{signal}": "信号", "{diagnostic}": "条诊断", "{run}": "轮", "{alert}": "告警", "{entry}": "条目", "{group}": "消费组", "{consumer}": "消费者", "{conflict}": "次冲突", "{task}": "任务", "{worker}": "工作会话", "{source}": "来源", "{partition}": "分区", "{failure}": "次失败", "{check}": "次检查", "{batch}": "批次", "{goroutine}": "协程", "{thread}": "线程", "{object}": "对象", "{file_descriptor}": "文件描述符"}
	if label, ok := labels[unit]; ok {
		return label
	}
	return unit
}
