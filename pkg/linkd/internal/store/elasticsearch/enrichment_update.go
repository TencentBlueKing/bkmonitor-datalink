// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"encoding/json"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// eventWithoutEnrichment 用显式空字段遮蔽 Event 内嵌丰富字段，仅编码来源与 processing。
// 字段集与 EventEnrichment 保持一致；等价测试防止新增字段导致预算漂移。
type eventWithoutEnrichment struct {
	domain.Event
	EnrichStatus        *domain.EnrichStatus  `json:"enrich_status,omitempty"`
	Enrich              json.RawMessage       `json:"enrich,omitempty"`
	EnrichedAt          *time.Time            `json:"enriched_at,omitempty"`
	EnrichConfigDigest  string                `json:"enrich_config_digest,omitempty"`
	EnrichSourceVersion int64                 `json:"enrich_source_version,omitempty"`
	Processing          store.EventProcessing `json:"processing"`
}

// encodeEnrichmentUpdate 复用一次丰富编码构造局部写入，并检查等价的完整文档大小。
// 两个非空 JSON 对象合并时去掉一对花括号并增加一个逗号，长度为两者之和减一。
func encodeEnrichmentUpdate(event domain.Event, processing store.EventProcessing) ([]byte, int, error) {
	normalized, err := processing.Normalize()
	if err != nil {
		return nil, 0, err
	}
	source, err := json.Marshal(eventWithoutEnrichment{Event: event, Processing: normalized})
	if err != nil {
		return nil, 0, err
	}
	enrichment, err := json.Marshal(event.EventEnrichment)
	if err != nil {
		return nil, 0, err
	}
	body := make([]byte, 0, len(enrichment)+8)
	body = append(body, `{"doc":`...)
	body = append(body, enrichment...)
	body = append(body, '}')
	return body, len(source) + len(enrichment) - 1, nil
}
