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
	"regexp"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// 运行时和目录共用 collector 配置，新增原生指标无需维护第二份注册清单。
func registerBuiltinCollectors(registry *prometheus.Registry) error {
	// 调度延迟与 GC CPU 可区分外部请求等待和本机运行时竞争；不采集无关的全量运行时指标。
	goMetrics := collectors.WithGoCollectorRuntimeMetrics(collectors.GoRuntimeMetricsRule{
		Matcher: regexp.MustCompile(`^/(sched/latencies:seconds|cpu/classes/gc/.*|cpu/classes/total:cpu-seconds)$`),
	})
	for _, collector := range []prometheus.Collector{collectors.NewGoCollector(goMetrics), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})} {
		if err := registry.Register(collector); err != nil {
			return fmt.Errorf("register builtin collector: %w", err)
		}
	}
	return nil
}

func builtinMetricDefinitions() ([]MetricDefinition, error) {
	registry := prometheus.NewRegistry()
	if err := registerBuiltinCollectors(registry); err != nil {
		return nil, err
	}
	families, err := registry.Gather()
	if err != nil {
		return nil, fmt.Errorf("describe builtin collectors: %w", err)
	}
	definitions := make([]MetricDefinition, 0, len(families)+1)
	for _, family := range families {
		name, kind := family.GetName(), strings.ToLower(family.GetType().String())
		module, unit := "go", "1"
		if strings.HasPrefix(name, "process_") {
			module = "process"
		}
		switch {
		case strings.Contains(name, "_bytes"):
			unit = "By"
		case strings.Contains(name, "_seconds"):
			unit = "s"
		case strings.HasSuffix(name, "_percent"):
			unit = "%"
		}
		switch name {
		case "go_goroutines":
			unit = "{goroutine}"
		case "go_threads", "go_sched_gomaxprocs_threads":
			unit = "{thread}"
		case "process_open_fds", "process_max_fds":
			unit = "{file_descriptor}"
		case "go_memstats_mallocs_total", "go_memstats_frees_total", "go_memstats_heap_objects":
			unit = "{object}"
		}
		dimensions := []MetricDimension{}
		for _, sample := range family.GetMetric() {
			for _, label := range sample.GetLabel() {
				key := label.GetName()
				if !slices.ContainsFunc(dimensions, func(d MetricDimension) bool { return d.Name == key }) {
					description := "内置 collector 标签"
					if key == "version" {
						description = "Go 工具链版本"
					}
					dimensions = append(dimensions, MetricDimension{key, key, description})
				}
			}
		}
		slices.SortFunc(dimensions, func(a, b MetricDimension) int { return strings.Compare(a.Name, b.Name) })
		title, purpose := builtinMetricTitle(name)
		definitions = append(definitions, MetricDefinition{
			Name: name, PrometheusName: name, DisplayName: title, Module: module, Purpose: purpose,
			Type: kind, PrometheusType: kind, Unit: unit, UnitLabel: metricUnitLabel(unit),
			Description: title + "。" + family.GetHelp(), Dimensions: dimensions, Series: metricSeries(name, kind), Origin: "runtime",
		})
	}
	definitions = append(definitions, MetricDefinition{
		Name: "target_info", PrometheusName: "target_info", DisplayName: "服务资源信息", Module: "exporter", Purpose: "state",
		Type: "gauge", PrometheusType: "gauge", Unit: "1", UnitLabel: "无量纲",
		Description: "OTel exporter 的资源信息，值为 1。服务身份、版本和角色存放于此；Resource 默认属性及 OTEL_RESOURCE_ATTRIBUTES 可附加其他标签。",
		Dimensions: []MetricDimension{
			{"service.name", "service_name", "服务名 linkd"}, {"service.namespace", "service_namespace", "服务命名空间 kingeye"},
			{"service.instance.id", "service_instance_id", "进程实例身份"}, {"service.version", "service_version", "Linkd 构建版本"},
			{"linkd.role", "linkd_role", "进程角色"},
			{"telemetry.sdk.name", "telemetry_sdk_name", "OTel SDK 名称"},
			{"telemetry.sdk.language", "telemetry_sdk_language", "OTel SDK 实现语言"},
			{"telemetry.sdk.version", "telemetry_sdk_version", "OTel SDK 版本"},
		},
		Series: []string{"target_info"}, Origin: "exporter",
	})
	return definitions, nil
}

func builtinMetricTitle(name string) (string, string) {
	titles := map[string]string{
		"go_cpu_classes_gc_mark_assist_cpu_seconds_total":    "GC 辅助标记累计 CPU 时间",
		"go_cpu_classes_gc_mark_dedicated_cpu_seconds_total": "GC 专用标记累计 CPU 时间",
		"go_cpu_classes_gc_mark_idle_cpu_seconds_total":      "GC 空闲标记累计 CPU 时间",
		"go_cpu_classes_gc_pause_cpu_seconds_total":          "GC 暂停累计 CPU 时间",
		"go_cpu_classes_gc_total_cpu_seconds_total":          "GC 累计 CPU 时间",
		"go_cpu_classes_total_cpu_seconds_total":             "Go 累计可用 CPU 时间",
		"go_gc_duration_seconds":                             "GC 全局暂停时长",
		"go_gc_gogc_percent":                                 "GC 堆增长目标百分比",
		"go_gc_gomemlimit_bytes":                             "Go 内存限制",
		"go_goroutines":                                      "当前协程数量", "go_info": "Go 版本信息",
		"go_memstats_alloc_bytes":              "当前堆对象内存",
		"go_memstats_alloc_bytes_total":        "累计堆分配字节数",
		"go_memstats_buck_hash_sys_bytes":      "性能分析桶哈希表内存",
		"go_memstats_frees_total":              "累计释放堆对象数",
		"go_memstats_gc_sys_bytes":             "GC 元数据内存",
		"go_memstats_heap_alloc_bytes":         "当前已分配堆对象内存",
		"go_memstats_heap_idle_bytes":          "堆空闲内存",
		"go_memstats_heap_inuse_bytes":         "堆使用中内存",
		"go_memstats_heap_objects":             "当前堆对象数量",
		"go_memstats_heap_released_bytes":      "已归还系统的堆内存",
		"go_memstats_heap_sys_bytes":           "从系统获取的堆内存",
		"go_memstats_last_gc_time_seconds":     "最近一次 GC 的 Unix 时间",
		"go_memstats_mallocs_total":            "累计分配堆对象数",
		"go_memstats_mcache_inuse_bytes":       "使用中的 mcache 内存",
		"go_memstats_mcache_sys_bytes":         "从系统获取的 mcache 内存",
		"go_memstats_mspan_inuse_bytes":        "使用中的 mspan 内存",
		"go_memstats_mspan_sys_bytes":          "从系统获取的 mspan 内存",
		"go_memstats_next_gc_bytes":            "下次 GC 的堆大小目标",
		"go_memstats_other_sys_bytes":          "其他系统内存分配",
		"go_memstats_stack_inuse_bytes":        "栈使用中内存",
		"go_memstats_stack_sys_bytes":          "从系统获取的栈内存",
		"go_memstats_sys_bytes":                "Go 从系统获取的总内存",
		"go_sched_gomaxprocs_threads":          "最大并行执行线程数",
		"go_sched_latencies_seconds":           "协程可运行后的调度等待",
		"go_threads":                           "已创建系统线程数",
		"process_cpu_seconds_total":            "进程累计 CPU 时间",
		"process_max_fds":                      "进程文件描述符上限",
		"process_open_fds":                     "进程已打开文件描述符数",
		"process_resident_memory_bytes":        "进程常驻内存",
		"process_start_time_seconds":           "进程启动 Unix 时间",
		"process_virtual_memory_bytes":         "进程虚拟内存",
		"process_virtual_memory_max_bytes":     "进程虚拟内存上限",
		"process_network_receive_bytes_total":  "进程累计接收网络字节数",
		"process_network_transmit_bytes_total": "进程累计发送网络字节数",
	}
	title, ok := titles[name]
	if !ok {
		// 新版 collector 新增条目先自动发现，保留原始 HELP；中文短名称可随后完善。
		title = "内置运行时指标（" + name + "）"
	}
	purpose := "resources"
	if strings.Contains(name, "latencies") || name == "go_gc_duration_seconds" {
		purpose = "latency"
	}
	if strings.Contains(name, "time_seconds") || strings.Contains(name, "max_") || name == "go_info" || strings.Contains(name, "gogc") || strings.Contains(name, "gomemlimit") {
		purpose = "state"
	}
	return title, purpose
}
