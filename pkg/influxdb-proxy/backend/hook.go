// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/event"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/utils"
)

func initConfig(c common.Configuration) {
	// init config for kafka service
	c.SetDefault(common.ConfigKeyKafkaAddress, "0.0.0.0")
	c.SetDefault(common.ConfigKeyKafkaPort, 0)
	c.SetDefault(common.ConfigKeyKafkaTopicPrefix, nil)
	c.SetDefault(common.ConfigKeyKafkaVersion, "0.0.0.0")
	// 默认将认证关闭
	c.SetDefault(common.ConfigKeyKafkaIsAuth, false)
	c.SetDefault(common.ConfigKeyKafkaUsername, "")
	c.SetDefault(common.ConfigKeyKafkaPassword, "")
	c.SetDefault(common.ConfigKeyKafkaMechanism, "")
	c.SetDefault(common.ConfigKeyKafkaRetention, "336h")

	c.SetDefault(common.ConfigKeyBackendForceBackup, true)
	c.SetDefault(common.ConfigKeyBackendTimeout, "30s")
	c.SetDefault(common.ConfigKeyBackendIgnoreKafka, false)

	// 缓冲区相关配置
	c.SetDefault(common.ConfigKeyBatchSize, 1500)
	c.SetDefault(common.ConfigKeyFlushTime, "5s")
	c.SetDefault(common.ConfigKeyMaxFlushConcurrency, 100)
}

func init() {
	// subscribe for config pre parse, in order for avoid empty config
	utils.CheckError(eventbus.Subscribe(event.EvSysConfigPreParse, initConfig))
}
