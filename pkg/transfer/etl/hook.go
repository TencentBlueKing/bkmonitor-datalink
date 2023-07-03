// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfKeyTime        = "etl.time"
	ConfKeyTimeFormats = "etl.time.formats"
)

// InitConfiguration :
func initConfiguration(c define.Configuration) {
	c.SetDefault(ConfKeyTimeFormats, []string{})
}

func readConfiguration(c define.Configuration) {
	var conf struct {
		Formats []struct {
			Name   string `mapstructure:"name"`
			Layout string `mapstructure:"layout"`
		} `mapstructure:"formats"`
	}

	utils.CheckError(c.UnmarshalKey(ConfKeyTime, &conf))
	for _, item := range conf.Formats {
		define.RegisterTimeLayout(item.Name, item.Layout)
	}
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, readConfiguration))
}
