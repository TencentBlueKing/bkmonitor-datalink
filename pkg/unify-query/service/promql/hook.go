// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(MaxSamplesConfigPath, 50000000)
	viper.SetDefault(TimeoutConfigPath, "1m")
	viper.SetDefault(LookbackDeltaConfigPath, "5m")
	viper.SetDefault(EnableNegativeOffsetConfigPath, true)
	viper.SetDefault(EnableAtModifierConfigPath, true)
	viper.SetDefault(DefaultStepConfigPath, time.Minute)
	viper.SetDefault(MaxEngineNumConfigPath, 10)
}

// LoadConfig
func LoadConfig() {
	MaxSamples = viper.GetInt(MaxSamplesConfigPath)
	Timeout = viper.GetDuration(TimeoutConfigPath)
	LookbackDelta = viper.GetDuration(LookbackDeltaConfigPath)
	EnableNegativeOffset = viper.GetBool(EnableNegativeOffsetConfigPath)
	EnableAtModifier = viper.GetBool(EnableAtModifierConfigPath)
	DefaultStep = viper.GetDuration(DefaultStepConfigPath)

	log.Debugf(context.TODO(),
		"reload success new config: max_samples:%d,timeout:%s,lookback_delta:%s,enable_negative_offset:%v",
		MaxSamples, Timeout, LookbackDelta, EnableNegativeOffset,
	)
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for default config, maybe http module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for new config, maybe http module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
