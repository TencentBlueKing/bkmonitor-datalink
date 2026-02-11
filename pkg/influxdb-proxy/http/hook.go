// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/event"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/eventbus"
)

func initConfig(c common.Configuration) {
	// init config for http service
	c.SetDefault(common.ConfigHTTPPort, 10201)
	c.SetDefault(common.ConfigHTTPAddress, "0.0.0.0")

	// 设置http批次处理大小的默认值
	c.SetDefault(common.ConfigHTTPBatchsize, 5000)
	c.SetDefault(common.ConfigKeyConsulHealthPeriod, "30s")
	c.SetDefault(common.ConfigKeyConsulAddress, "127.0.0.1:8500")
	c.SetDefault(common.ConfigKeyConsulPrefix, "bkmonitor_enterprise_production/metadata/influxdb_info")
	c.SetDefault(common.ConfigKeyConsulACLToken, "")
}

func init() {
	// subscribe for config pre parse, in order for avoid empty config
	_ = eventbus.Subscribe(event.EvSysConfigPreParse, initConfig)
}
