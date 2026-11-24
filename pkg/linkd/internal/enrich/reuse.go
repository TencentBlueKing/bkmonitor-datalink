// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package enrich

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"linkd/internal/domain"
)

// WithReuseOnActive 指定可沿用活跃 Alert 成功快照的步骤；空列表保持逐 Event 执行。
func WithReuseOnActive(processors ...string) ChainOption {
	return func(chain *Chain) {
		chain.reuseOnActive = make(map[string]bool, len(processors))
		for _, name := range processors {
			chain.reuseOnActive[name] = true
		}
	}
}

func (c *Chain) activeProcessorResults(input Input) (map[string]ProcessorEnvelope, error) {
	active := input.ActiveAlert
	// 活跃身份与快照等级共同限定复用范围；原地升级后的当前等级不能替代快照等级。
	// 缺少快照等级时直接正常执行，不补查 opening Event，也不回填历史告警。
	if len(c.reuseOnActive) == 0 || input.Preview || active == nil || active.EnrichSeverity == "" ||
		active.Status != domain.AlertStatusActive || active.BKTenantID != input.Event.BKTenantID ||
		active.EventSourceID != input.Event.EventSourceID || active.Fingerprint != input.Event.Fingerprint ||
		!slices.ContainsFunc(input.Event.Evaluations, func(e domain.EventEvaluation) bool { return e.Severity == active.EnrichSeverity }) {
		return nil, nil
	}
	// 全等级关闭可以与新的触发共处一条 Event；该触发会开启新生命周期，不能带入旧快照。
	if slices.ContainsFunc(input.Event.Evaluations, func(e domain.EventEvaluation) bool { return e.Severity == domain.SeverityAll }) {
		return nil, nil
	}
	// Alert 已经过仓储读取边界；这里只解码需要复用的结果，不重复校验整个快照。
	var entries []ProcessorEntry
	if err := json.Unmarshal(active.Enrich["processors"], &entries); err != nil {
		return nil, fmt.Errorf("decode active alert enrich processors: %w", err)
	}
	results := make(map[string]ProcessorEnvelope)
	for _, entry := range entries {
		for name, saved := range entry {
			if c.reuseOnActive[name] && saved.Status == domain.EnrichStatusSucceeded {
				results[name] = saved
			}
		}
	}
	return results, nil
}

// restoreProcessorContext 恢复内置步骤的类型化上下文；仅重放补丁会使后续 Display/Metric
// 丢失资源属性。自定义步骤直接通过补丁工作对象传递结果，不需要额外上下文。
func restoreProcessorContext(context *EnrichContext, name string, saved ProcessorEnvelope) error {
	var target any
	switch name {
	case "strategy":
		context.Strategy = StrategyContext{}
		target = &context.Strategy.Values
	case "resource", "monitor-resource":
		context.Resource = ResourceContext{}
		target = &context.Resource.Values
	case "display":
		context.Display = DisplayContext{}
		target = &context.Display.Values
	case "metric":
		context.Metric = MetricContext{}
		target = &context.Metric.Values
	case "source":
		context.Source = SourceContext{}
		target = &context.Source.Values
	case "log":
		context.Log = LogContext{}
		target = &context.Log.Values
	case "apm":
		context.APM = APMContext{}
		target = &context.APM.Values
	case "k8s":
		context.K8s = K8sContext{}
		target = &context.K8s.Values
	default:
		return nil
	}
	value := saved.Value
	if saved.Patches != nil {
		value = processorValueView(name, saved.Patches)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode reused processor context: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("restore reused processor context: %w", err)
	}
	return nil
}
