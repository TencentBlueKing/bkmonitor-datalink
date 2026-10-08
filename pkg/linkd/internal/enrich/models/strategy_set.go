// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"linkd/internal/domain"
)

// StrategySet 保存当前声明式监控模板的配置集合，与派生的 CWStrategy 分开读取。
type StrategySet struct {
	UID               string
	BKTenantID        string
	MonitorTemplateID int64
	Spec              StrategySetSpec
}

// StrategySetSpec 对齐 core_v1alpha1_strategyset.spec 中的内容生成依赖。
type StrategySetSpec struct {
	MonitorTemplateID int64               `json:"monitor_template_id"`
	ObjectModelCode   string              `json:"object_model_code"`
	StrategyConfigs   []StrategySetConfig `json:"strategy_configs"`
}

// StrategySetConfig 是 Set 内一项检测配置，ID 对应 CWStrategy.ConfigID。
type StrategySetConfig struct {
	ID                  string              `json:"id"`
	Enable              bool                `json:"enable"`
	Algorithms          domain.JSONObject   `json:"algorithms"`
	InnerStrategyConfig CWStrategySpec      `json:"inner_strategy_config"`
	InnerStrategyItem   CWStrategyItem      `json:"inner_strategy_item"`
	InnerMetricInfo     []domain.JSONObject `json:"inner_metric_info"`
}

// Config 精确选择关联配置；不按数组位置、默认配置或名称回退。
// 缺失返回 found=false；重复身份属于无效数据，禁止静默选择其中一项。
func (s StrategySet) Config(configID string) (StrategySetConfig, bool, error) {
	id, err := uuid.Parse(configID)
	if err != nil || id == uuid.Nil {
		return StrategySetConfig{}, false, fmt.Errorf("strategy set config ID is invalid")
	}
	var result StrategySetConfig
	found := false
	for _, config := range s.Spec.StrategyConfigs {
		candidate, err := uuid.Parse(config.ID)
		if err != nil || candidate == uuid.Nil {
			return StrategySetConfig{}, false, fmt.Errorf("strategy set contains invalid config ID")
		}
		if candidate == id {
			if found {
				return StrategySetConfig{}, false, fmt.Errorf("strategy set contains duplicate config ID")
			}
			result, found = config, true
		}
	}
	return result, found, nil
}

// ValidIdentity 验证外部读取结果的租户和模板身份，排除缺失与空白身份。
func (s StrategySet) ValidIdentity(tenant string, templateID int64) bool {
	return tenant != "" && strings.TrimSpace(tenant) == tenant && templateID > 0 && s.BKTenantID == tenant && s.MonitorTemplateID == templateID && s.Spec.MonitorTemplateID == templateID
}
