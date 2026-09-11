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

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

var _ enrich.CWStrategyReader = (*MockCWStrategyClient)(nil)

const sampleMonitorTemplateID int64 = 1

// MockCWStrategyClient 提供开发期固定鲸眼声明式策略。
type MockCWStrategyClient struct{}

// GetByBKStrategyID 返回匹配平台策略 ID 的固定鲸眼策略。
func (*MockCWStrategyClient) GetByBKStrategyID(ctx context.Context, tenantID string, bkStrategyID int64) (models.CWStrategy, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.CWStrategy{}, false, err
	}
	if tenantID != SampleTenantID || bkStrategyID != SampleStrategyID {
		return models.CWStrategy{}, false, nil
	}
	return sampleCWStrategy(), true, nil
}

func sampleCWStrategy() models.CWStrategy {
	tenantID := SampleTenantID
	bizID := SampleBizID
	monitorTemplateID := sampleMonitorTemplateID
	configID := "strategy-config-1"
	objectModelCode := "cw-Host"
	isDefault := true
	strategyItem := &models.CWStrategyItem{
		AggregateMethod: "avg", AggregatePeriod: json.RawMessage(`60`), Functions: json.RawMessage(`[]`),
		QueryConfigs: []models.StrategyQueryConfig{{
			Unit: "percent", Alias: "A", AggregateMethod: "avg", AggregatePeriod: 60,
			MetricField: "usage", AggregateFilter: json.RawMessage(`[]`), AggregateBy: []string{},
			ResultTableID: "system.cpu", DataSourceLabel: "bk_monitor", DataTypeLabel: "time_series",
		}},
	}
	return models.CWStrategy{
		Active: true, Kind: models.CWStrategyKindStrategy, APIVersion: "v1alpha1", Name: "CPU 使用率",
		BKTenantID: &tenantID,
		BKBizID:    &bizID,
		Namespace:  "default", UID: "strategy-config-1",
		IsDefault:         &isDefault,
		MonitorTemplateID: &monitorTemplateID,
		ConfigID:          &configID,
		ObjectModelCode:   &objectModelCode,
		Spec: models.CWStrategySpec{
			Name: "CPU 使用率", ItemName: "CPU 使用率", AlarmAlias: "CPU 使用率过高", DataSource: "system",
			StrategyItem: strategyItem,
		},
		Status: models.CWStrategyStatus{BKStrategyID: SampleStrategyID},
	}
}
