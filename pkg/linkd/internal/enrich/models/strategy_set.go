// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import "linkd/internal/domain"

// StrategySetConfig 是 SplitRecord.strategy_config 中的检测配置项，ID 对应 config_uid。
type StrategySetConfig struct {
	ID                  string              `json:"id"`
	Enable              bool                `json:"enable"`
	Algorithms          domain.JSONObject   `json:"algorithms"`
	InnerStrategyConfig CWStrategySpec      `json:"inner_strategy_config"`
	InnerStrategyItem   CWStrategyItem      `json:"inner_strategy_item"`
	InnerMetricInfo     []domain.JSONObject `json:"inner_metric_info"`
}
