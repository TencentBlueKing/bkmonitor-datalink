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

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

var _ enrich.CWStrategyReader = (*MockCWStrategyClient)(nil)

const sampleMonitorTemplateID int64 = 1

// MockCWStrategyClient 提供开发期固定鲸眼声明式策略。
type MockCWStrategyClient struct{}

// GetByBKStrategyID 返回匹配平台策略 ID 的固定鲸眼策略。
func (*MockCWStrategyClient) GetByBKStrategyID(ctx context.Context, bkStrategyID int64) (models.CWStrategy, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.CWStrategy{}, false, err
	}
	if bkStrategyID != SampleStrategyID {
		return models.CWStrategy{}, false, nil
	}
	return sampleCWStrategy(), true, nil
}

// GetByMonitorTemplateID 返回匹配监控模板 ID 的固定鲸眼策略。
func (*MockCWStrategyClient) GetByMonitorTemplateID(ctx context.Context, monitorTemplateID int64) (models.CWStrategy, bool, error) {
	if err := ctx.Err(); err != nil {
		return models.CWStrategy{}, false, err
	}
	if monitorTemplateID != sampleMonitorTemplateID {
		return models.CWStrategy{}, false, nil
	}
	return sampleCWStrategy(), true, nil
}

func sampleCWStrategy() models.CWStrategy {
	monitorTemplateID := sampleMonitorTemplateID
	configID := "strategy-config-1"
	objectModelCode := "cw-Host"
	isDefault := true
	return models.CWStrategy{
		Active: true, Kind: models.CWStrategyKindStrategy, APIVersion: "v1alpha1", Name: "CPU 使用率",
		Namespace: "default", UID: "strategy-config-1",
		IsDefault:         &isDefault,
		MonitorTemplateID: &monitorTemplateID,
		ConfigID:          &configID,
		ObjectModelCode:   &objectModelCode,
		Spec: models.CWStrategySpec{
			Name: "CPU 使用率", AlarmAlias: "CPU 使用率过高", DataSource: "system",
		},
		Status: models.CWStrategyStatus{BKStrategyID: SampleStrategyID},
	}
}
