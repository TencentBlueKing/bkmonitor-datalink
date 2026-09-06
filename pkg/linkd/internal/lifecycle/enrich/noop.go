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

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

// NoopEnricher 把已知来源的空处理链编码为成功的固定协议。
type NoopEnricher struct{}

// Enrich 返回 succeeded 和空 processors 列表。
func (NoopEnricher) Enrich(ctx context.Context, _ lifecycle.EnrichInput) (lifecycle.EnrichResult, error) {
	if err := ctx.Err(); err != nil {
		return lifecycle.EnrichResult{}, err
	}
	payload := Payload{Status: domain.EnrichStatusSucceeded, Processors: []ProcessorEntry{}}
	data, err := payload.JSONObject()
	if err != nil {
		return lifecycle.EnrichResult{}, err
	}
	return lifecycle.EnrichResult{Status: payload.Status, Data: data}, nil
}
