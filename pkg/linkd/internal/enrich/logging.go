// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"errors"
	"fmt"

	"linkd/internal/domain"
)

// Logger 只接收丰富链中被隔离的错误，不接收来源正文和依赖响应。
type Logger interface {
	WarnContext(context.Context, string, ...any)
}

// WithLogger 注入错误日志；Chain 只读复用，逐次诊断保存在本次 Scope 中。
func WithLogger(logger Logger) ChainOption {
	return func(chain *Chain) { chain.logger = logger }
}

// 依赖错误可能被 Processor 转成 partial/failed 并返回 nil，必须在转换前保留。
// Scope 与其查询缓存只属于一条 Event 的串行调用，不把错误写入丰富快照。
func (s *Scope) recordDependencyError(operation string, err error) {
	if err != nil {
		s.dependencies = append(s.dependencies, operation)
		s.dependencyErrors = append(s.dependencyErrors, fmt.Errorf("%s: %w", operation, err))
	}
}

func (c *Chain) logProcessorFailure(ctx context.Context, scope *Scope, name, outcome string, result ProcessorResult, cause error) {
	if c.logger == nil || ctx.Err() != nil {
		return
	}
	// 可选依赖缺失但处理器按业务约定成功或跳过时，不制造失败日志。
	if cause == nil && result.Status != domain.EnrichStatusFailed && result.Status != domain.EnrichStatusPartial {
		return
	}
	operation := "processor_process"
	switch outcome {
	case ProcessorOutcomeMatchError:
		operation = "processor_match"
	case ProcessorOutcomeInvalidResult:
		operation = "processor_result"
	}
	if cause == nil {
		cause = errors.Join(scope.dependencyErrors...)
	}
	if cause == nil {
		cause = fmt.Errorf("processor returned %s; see diagnostics and trace", result.Status)
	}
	event := scope.original
	c.logger.WarnContext(ctx, "enrich processor failed", "module", "enrich", "operation", operation,
		"processor", name, "dependencies", scope.dependencies, "outcome", outcome, "status", result.Status, "error", cause,
		"bk_tenant_id", event.BKTenantID, "event_source_id", event.EventSourceID, "event_source_version", event.EventSourceVersion,
		"event_id", event.EventID, "severity", scope.evaluation.Severity, "diagnostics", result.Diagnostics, "trace", result.Trace)
}
