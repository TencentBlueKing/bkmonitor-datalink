// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package target

import (
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	confMaxTimeoutPath          = "discover.scrape.max_timeout"
	confMinPeriodPath           = "discover.scrape.min_period"
	confDefaultPeriodPath       = "discover.scrape.default_period"
	confEventScrapeMaxSpanPath  = "operator.event.max_span"
	confEventScrapeIntervalPath = "operator.event.scrape_interval"
	confEventScrapeFilesPath    = "operator.event.scrape_path"
	confBuiltInLabelsPath       = "operator.builtin_labels"
	confServiceNamePath         = "operator.service_name"
)

var (
	ConfMaxTimeout          string
	ConfMinPeriod           string
	ConfDefaultPeriod       string
	ConfEventScrapeInterval string
	ConfEventScrapeFiles    []string
	ConfEventMaxSpan        string
	ConfBuiltinLabels       []string
	ConfServiceName         string
)

func initConfig() {
	viper.SetDefault(confMaxTimeoutPath, "100s")
	viper.SetDefault(confMinPeriodPath, "3s")
	viper.SetDefault(confDefaultPeriodPath, "60s")
	viper.SetDefault(confEventScrapeMaxSpanPath, "2h")
	viper.SetDefault(confEventScrapeIntervalPath, "60s")
	viper.SetDefault(confEventScrapeFilesPath, []string{"/var/log/gse/events.log"})
	viper.SetDefault(confBuiltInLabelsPath, []string{"instance", "job"})
	viper.SetDefault(confServiceNamePath, "bkmonitor-operator-stack-operator")
}

func updateConfig() {
	ConfMaxTimeout = viper.GetString(confMaxTimeoutPath)
	ConfMinPeriod = viper.GetString(confMinPeriodPath)
	ConfDefaultPeriod = viper.GetString(confDefaultPeriodPath)
	ConfEventMaxSpan = viper.GetString(confEventScrapeMaxSpanPath)
	ConfEventScrapeInterval = viper.GetString(confEventScrapeIntervalPath)
	ConfEventScrapeFiles = viper.GetStringSlice(confEventScrapeFilesPath)
	ConfBuiltinLabels = viper.GetStringSlice(confBuiltInLabelsPath)
	ConfServiceName = viper.GetString(confServiceNamePath)
}

func init() {
	if err := config.EventBus.Subscribe(config.EventConfigPreParse, initConfig); err != nil {
		logger.Errorf("failed to subscribe event %s, err: %v", config.EventConfigPreParse, err)
	}

	if err := config.EventBus.Subscribe(config.EventConfigPostParse, updateConfig); err != nil {
		logger.Errorf("failed to subscribe event %s, err: %v", config.EventConfigPostParse, err)
	}
}
