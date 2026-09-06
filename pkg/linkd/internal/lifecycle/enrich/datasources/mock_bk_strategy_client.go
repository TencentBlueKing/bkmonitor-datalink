// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"encoding/json"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

var _ enrich.BKStrategyReader = (*MockBKStrategyClient)(nil)

// MockBKStrategyClient 提供开发期固定平台策略与历史快照。
type MockBKStrategyClient struct{}

// GetStrategyHistory 返回匹配平台策略历史的固定数据。
func (*MockBKStrategyClient) GetStrategyHistory(ctx context.Context, strategyID, historyID int64) (models.BkStrategyHistory, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.BkStrategyHistory{}, false, err
	}
	if strategyID != SampleStrategyID || historyID != SampleHistoryID {
		return models.BkStrategyHistory{}, false, nil
	}
	return models.BkStrategyHistory{
		ID:         SampleHistoryID,
		StrategyID: SampleStrategyID,
		Content: domain.JSONObject{
			"id":         json.RawMessage(`123`),
			"bk_biz_id":  json.RawMessage(`2`),
			"name":       json.RawMessage(`"CPU 使用率"`),
			"source":     json.RawMessage(`"monitor"`),
			"scenario":   json.RawMessage(`"os"`),
			"type":       json.RawMessage(`"monitor"`),
			"is_enabled": json.RawMessage(`true`),
			"is_invalid": json.RawMessage(`false`),
			"items":      json.RawMessage(`[{"id":1,"name":"CPU 使用率","query_configs":[{"id":1,"unit":"percent","alias":"A","metric_id":"bk_monitor.system.cpu.usage","agg_method":"avg","agg_interval":60,"metric_field":"usage","agg_condition":[],"agg_dimension":[],"result_table_id":"system.cpu","data_source_label":"bk_monitor","data_type_label":"time_series"}]}]`),
		},
	}, true, nil
}

// GetStrategy 返回匹配平台当前策略的固定数据。
func (*MockBKStrategyClient) GetStrategy(ctx context.Context, strategyID int64) (models.BkStrategy, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.BkStrategy{}, false, err
	}
	if strategyID != SampleStrategyID {
		return models.BkStrategy{}, false, nil
	}
	return models.BkStrategy{
		ID: SampleStrategyID, Name: "CPU 使用率", BKBizID: SampleBizID,
		Source: "monitor", Scenario: "os", Type: "monitor", IsEnabled: true,
	}, true, nil
}
