// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package confengine

import (
	"path/filepath"

	"github.com/elastic/go-ucfg"
	"github.com/elastic/go-ucfg/yaml"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metacache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	loadConfigSuccessTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "engine_load_config_success_total",
			Help:      "Engine load config successfully total",
		},
	)

	loadConfigFailedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "engine_load_config_failed_total",
			Help:      "Engine load config failed total",
		},
	)
)

var loadedPlatformConfig bool

// LoadedPlatformConfig 返回是否已经加载过 platform 配置
//
// 作为判断服务是否就绪的一种方案
func LoadedPlatformConfig() bool {
	return loadedPlatformConfig
}

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncLoadConfigSuccessCounter() {
	loadConfigSuccessTotal.Inc()
}

func (m *metricMonitor) IncLoadConfigFailedCounter() {
	loadConfigFailedTotal.Inc()
}

func SelectConfigFromType(configs []*Config, typ string) *Config {
	type T struct {
		Type string `config:"type"`
	}

	for _, c := range configs {
		var subConf T
		if err := c.Unpack(&subConf); err != nil {
			logger.Errorf("failed to unpack config, err: %v", err)
			continue
		}
		if subConf.Type == typ {
			return c
		}
	}
	return nil
}

func LoadConfigPatterns(patterns []string) []*Config {
	var configs []*Config
	for _, p := range patterns {
		c, err := LoadConfigPattern(p)
		if err != nil {
			logger.Errorf("failed to load subconfig, path: %s, err: %v", p, err)
			continue
		}
		configs = append(configs, c...)
	}
	return configs
}

func LoadConfigPattern(pattern string) ([]*Config, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		logger.Warnf("no config file found for pattern %s", pattern)
		return nil, err
	}

	multiConfig := make([]*Config, 0)
	for _, path := range matches {
		config, err := LoadConfigPath(path)
		if err != nil {
			logger.Errorf("load config failed: %v, path: %s", err, path)
			continue
		}
		multiConfig = append(multiConfig, config)
	}
	return multiConfig, nil
}

func LoadConfigPath(path string) (*Config, error) {
	config, err := yaml.NewConfigWithFile(path, ucfg.PathSep("."))
	if err != nil {
		DefaultMetricMonitor.IncLoadConfigFailedCounter()
		return nil, err
	}

	var token define.Token
	if err := config.Unpack(&token); err != nil {
		logger.Warnf("failed to parse config (%s), err: %v", path, err)
	}
	if token.Original != "" {
		logger.Debugf("metacache set token: %+v", token)
		metacache.Set(token.Original, token)
	}
	if token.Type == define.ConfigTypePlatform {
		loadedPlatformConfig = true
	}

	logger.Debugf("load config file '%v'", path)
	DefaultMetricMonitor.IncLoadConfigSuccessCounter()
	return New((*beat.Config)(config)), err
}

func LoadConfigContent(content string) (*Config, error) {
	config, err := yaml.NewConfig([]byte(content))
	if err != nil {
		return nil, err
	}
	return New((*beat.Config)(config)), err
}

func MustLoadConfigContent(content string) *Config {
	config, err := LoadConfigContent(content)
	if err != nil {
		panic(err)
	}
	return config
}
