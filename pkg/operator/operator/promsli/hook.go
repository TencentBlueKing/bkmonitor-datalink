// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promsli

import (
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	confPromScrapeConfigPath = "operator.prometheus_scrape"
)

type ScrapeConfig struct {
	Namespace string                 `yaml:"namespace" mapstructure:"namespace"`
	Global    map[string]interface{} `yaml:"global" mapstructure:"global"`
	RuleFiles []string               `yaml:"rule_files" mapstructure:"rule_files"`
}

var ConfScrapeConfig = ScrapeConfig{}

func updateConfig() {
	if viper.IsSet(confPromScrapeConfigPath) {
		if err := viper.UnmarshalKey(confPromScrapeConfigPath, &ConfScrapeConfig); err != nil {
			logger.Errorf("failed to unmarshal ConfScrapeConfig, err: %v", err)
		}
	} else {
		ConfScrapeConfig = ScrapeConfig{}
	}
}

func init() {
	if err := config.EventBus.Subscribe(config.EventConfigPostParse, updateConfig); err != nil {
		logger.Errorf("failed to subscribe event %s, err: %v", config.EventConfigPostParse, err)
	}
}
