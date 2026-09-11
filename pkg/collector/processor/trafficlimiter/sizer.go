// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trafficlimiter

import (
	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

const (
	serviceNameKey     = "service.name"
	unknownServiceName = "__unknown__"
)

// serviceSizer 无状态，可被多个 goroutine 共享；用于计算的临时 pdata 必须是调用栈上的局部变量。
type serviceSizer struct {
	tracesSizer  ptrace.Sizer
	metricsSizer pmetric.Sizer
	logsSizer    plog.Sizer
}

func newServiceSizer() *serviceSizer {
	return &serviceSizer{
		tracesSizer:  ptrace.NewProtoMarshaler().(ptrace.Sizer),
		metricsSizer: pmetric.NewProtoMarshaler().(pmetric.Sizer),
		logsSizer:    plog.NewProtoMarshaler().(plog.Sizer),
	}
}

// Sizes 按 Resource 中的 service.name 计算各服务的 OTLP Protobuf 逻辑字节数。
// 不支持的 RecordType 会被忽略，避免该处理器影响非 OTLP Pipeline。
func (s *serviceSizer) Sizes(record *define.Record) (map[string]int, error) {
	switch record.RecordType {
	case define.RecordTraces:
		data, ok := record.Data.(ptrace.Traces)
		if !ok {
			return nil, errors.Errorf("unexpected traces data type %T", record.Data)
		}
		return s.tracesSizes(data), nil

	case define.RecordMetrics:
		data, ok := record.Data.(pmetric.Metrics)
		if !ok {
			return nil, errors.Errorf("unexpected metrics data type %T", record.Data)
		}
		return s.metricsSizes(data), nil

	case define.RecordLogs:
		data, ok := record.Data.(plog.Logs)
		if !ok {
			return nil, errors.Errorf("unexpected logs data type %T", record.Data)
		}
		return s.logsSizes(data), nil
	}

	return nil, nil
}

// DropRejected 从已经解析的 pdata 中移除超限服务，返回是否仍有可继续处理的数据。
// 过滤粒度与 Sizes 保持一致，都是 Resource Group，避免同一服务的部分数据被误放行。
func (s *serviceSizer) DropRejected(record *define.Record, rejected map[string]struct{}) (bool, error) {
	switch record.RecordType {
	case define.RecordTraces:
		data, ok := record.Data.(ptrace.Traces)
		if !ok {
			return false, errors.Errorf("unexpected traces data type %T", record.Data)
		}
		groups := data.ResourceSpans()
		groups.RemoveIf(rejectedResourceGroup[ptrace.ResourceSpans](rejected))
		return groups.Len() > 0, nil

	case define.RecordMetrics:
		data, ok := record.Data.(pmetric.Metrics)
		if !ok {
			return false, errors.Errorf("unexpected metrics data type %T", record.Data)
		}
		groups := data.ResourceMetrics()
		groups.RemoveIf(rejectedResourceGroup[pmetric.ResourceMetrics](rejected))
		return groups.Len() > 0, nil

	case define.RecordLogs:
		data, ok := record.Data.(plog.Logs)
		if !ok {
			return false, errors.Errorf("unexpected logs data type %T", record.Data)
		}
		groups := data.ResourceLogs()
		groups.RemoveIf(rejectedResourceGroup[plog.ResourceLogs](rejected))
		return groups.Len() > 0, nil
	}

	return false, nil
}

// Resource 分组是 OTLP 请求的顶层 repeated 字段，因此各分组的 Protobuf 贡献之和严格等于整包大小。
// 单服务请求直接计算原始 pdata；多服务请求逐个分组借用到临时 pdata 中计算，
// 可以在不深拷贝数据的前提下得到按服务归属的字节数：
// MoveTo 只交换底层指针，算完立即搬回原位，函数返回时原始数据已完整复原。
//
// 借出期间 Resource 处于暂时清空的状态，因此调用方必须独占该 Record。
// PreCheck 阶段满足这一前提（每个请求在自己的 goroutine 上串行执行各 PreCheck 处理器），
// 但绝不能让多个 goroutine 并发对同一个 Record 调用，否则会互相搬空数据并算出偏小的字节数。
func (s *serviceSizer) tracesSizes(data ptrace.Traces) map[string]int {
	resourceSpans := data.ResourceSpans()
	return serviceSizes(
		resourceSpans.Len(),
		resourceSpans.At,
		func() int { return s.tracesSizer.TracesSize(data) },
		lazyGroupSizer(
			func() (ptrace.Traces, ptrace.ResourceSpans) {
				scratch := ptrace.NewTraces()
				return scratch, scratch.ResourceSpans().AppendEmpty()
			},
			s.tracesSizer.TracesSize,
		),
	)
}

func (s *serviceSizer) metricsSizes(data pmetric.Metrics) map[string]int {
	resourceMetrics := data.ResourceMetrics()
	return serviceSizes(
		resourceMetrics.Len(),
		resourceMetrics.At,
		func() int { return s.metricsSizer.MetricsSize(data) },
		lazyGroupSizer(
			func() (pmetric.Metrics, pmetric.ResourceMetrics) {
				scratch := pmetric.NewMetrics()
				return scratch, scratch.ResourceMetrics().AppendEmpty()
			},
			s.metricsSizer.MetricsSize,
		),
	)
}

func (s *serviceSizer) logsSizes(data plog.Logs) map[string]int {
	resourceLogs := data.ResourceLogs()
	return serviceSizes(
		resourceLogs.Len(),
		resourceLogs.At,
		func() int { return s.logsSizer.LogsSize(data) },
		lazyGroupSizer(
			func() (plog.Logs, plog.ResourceLogs) {
				scratch := plog.NewLogs()
				return scratch, scratch.ResourceLogs().AppendEmpty()
			},
			s.logsSizer.LogsSize,
		),
	)
}

type resourceWithAttributes interface {
	Resource() pcommon.Resource
}

type movableResourceGroup[T any] interface {
	resourceWithAttributes
	MoveTo(T)
}

// serviceSizes 收敛三种 pdata 的共同流程：空数据、单服务快速路径和多服务逐组计量。
// groupSize 负责各 pdata 类型特有的 MoveTo 与 Sizer 调用。
func serviceSizes[T resourceWithAttributes](
	count int,
	at func(int) T,
	wholeSize func() int,
	groupSize func(T) int,
) map[string]int {
	if count == 0 {
		return nil
	}
	if name, ok := singleServiceName(count, at); ok {
		return map[string]int{name: wholeSize()}
	}

	sizes := make(map[string]int, 1)
	for i := 0; i < count; i++ {
		item := at(i)
		sizes[serviceName(item.Resource())] += groupSize(item)
	}
	return sizes
}

// lazyGroupSizer 延迟创建临时 pdata，使单服务快速路径不会产生无用分配；
// 多服务路径复用同一个 holder，统一完成 Resource Group 的借出、计量和复原。
func lazyGroupSizer[D any, T movableResourceGroup[T]](
	newScratch func() (D, T),
	size func(D) int,
) func(T) int {
	var scratch D
	var holder T
	initialized := false
	return func(item T) int {
		if !initialized {
			scratch, holder = newScratch()
			initialized = true
		}
		item.MoveTo(holder)
		bytes := size(scratch)
		holder.MoveTo(item)
		return bytes
	}
}

func rejectedResourceGroup[T resourceWithAttributes](rejected map[string]struct{}) func(T) bool {
	return func(item T) bool {
		_, ok := rejected[serviceName(item.Resource())]
		return ok
	}
}

// singleServiceName 判断全部 Resource Group 是否属于同一个服务。
// 返回 true 时可以直接计算整包大小，避免创建临时 pdata 和搬移 Resource Group。
func singleServiceName[T resourceWithAttributes](count int, at func(int) T) (string, bool) {
	name := serviceName(at(0).Resource())
	for i := 1; i < count; i++ {
		if serviceName(at(i).Resource()) != name {
			return "", false
		}
	}
	return name, true
}

func serviceName(resource pcommon.Resource) string {
	value, ok := resource.Attributes().Get(serviceNameKey)
	if !ok {
		return unknownServiceName
	}
	name := value.AsString()
	if name == "" {
		return unknownServiceName
	}
	return name
}
