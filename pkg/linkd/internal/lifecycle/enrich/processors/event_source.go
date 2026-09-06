// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// EventSource 丰富告警的事件来源信息。
type EventSource struct{}

// Name 返回稳定的 Processor 名称。
func (EventSource) Name() string { return rules.SourceProcessor }

// Match 判断当前告警是否适用 EventSource。
func (EventSource) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 生成内置鲸眼事件来源信息。
func (EventSource) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	alert := scope.Alert()
	values := models.SourceValues{SourceID: alert.EventSourceID, MetaInfo: alert.SourceEventID}
	sourceName, found, err := scope.AlarmSourceName(ctx)
	if err != nil || !found {
		scope.Context().Source.Set(values)
		value, encodeErr := scope.Context().Source.JSONObject()
		if encodeErr != nil {
			return enrich.ProcessorResult{}, encodeErr
		}
		result := failedDependency(rules.DependencyAlarmSource)
		result.Value = value
		return result, nil
	}
	values.SourceName = sourceName
	scope.Context().Source.Set(values)
	value, err := scope.Context().Source.JSONObject()
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: value}, nil
}
