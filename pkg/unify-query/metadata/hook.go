// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"fmt"
	"time"

	cache "github.com/patrickmn/go-cache"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

var (
	md *metaData
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(DefaultExpirationPath, time.Minute*1)
	viper.SetDefault(CleanupIntervalPath, time.Minute*5)
}

// initMetadata
func initMetadata() {
	md = &metaData{
		c: cache.New(
			viper.GetDuration(DefaultExpirationPath),
			viper.GetDuration(CleanupIntervalPath),
		),
	}
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for log module for default config, maybe log module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, initMetadata); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for log module for default config, maybe log module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}
}
