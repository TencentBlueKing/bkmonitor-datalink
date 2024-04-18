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
	confPromSliConfigPath = "operator.sli"
)

type Config struct {
	Namespace     string           `yaml:"namespace" mapstructure:"namespace"`
	SecretName    string           `yaml:"secret_name" mapstructure:"secret_name"`
	ConfigMapName string           `yaml:"configmap_name" mapstructure:"configmap_name"`
	Scrape        PrometheusConfig `yaml:"prometheus" mapstructure:"prometheus"`
}

type PrometheusConfig struct {
	Global    map[string]interface{} `yaml:"global" mapstructure:"global"`
	RuleFiles []string               `yaml:"rule_files" mapstructure:"rule_files"`
	Alerting  map[string]interface{} `yaml:"alerting" mapstructure:"alerting"`
}

var ConfConfig = &Config{}

func updateConfig() {
	if viper.IsSet(confPromSliConfigPath) {
		if err := viper.UnmarshalKey(confPromSliConfigPath, &ConfConfig); err != nil {
			logger.Errorf("failed to unmarshal ConfConfig, err: %v", err)
		}
	} else {
		ConfConfig = &Config{}
	}
}

func init() {
	if err := config.EventBus.Subscribe(config.EventConfigPostParse, updateConfig); err != nil {
		logger.Errorf("failed to subscribe event %s, err: %v", config.EventConfigPostParse, err)
	}
}
