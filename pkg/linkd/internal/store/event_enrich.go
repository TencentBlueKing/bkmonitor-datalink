// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"fmt"

	"linkd/internal/domain"
)

// ApplyEventEnrichment 在各后端共享同一完成性检查。已有计划或最终处理结果禁止补写丰富。
// 必须在后端的单对象 CAS 边界内调用，不能用读取成功代替并发版本验证。
func ApplyEventEnrichment(current StoredEvent, result domain.EventEnrichment) (domain.Event, error) {
	if current.Processing.State != domain.EventProcessStateUnprocessed || current.Processing.Plan != nil || current.Processing.PolicyContext != nil {
		return domain.Event{}, fmt.Errorf("%w: enrichment must precede lifecycle plan", ErrInvalidTransition)
	}
	updated, err := current.Event.WithEnrichment(result)
	if err != nil {
		return domain.Event{}, fmt.Errorf("%w: event enrichment: %w", ErrInvalidArgument, err)
	}
	return updated, nil
}
