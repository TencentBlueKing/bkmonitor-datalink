// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"fmt"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
)

// Data 备份数据结构
type Data struct {
	Header    http.Header          `json:"header"`
	Query     string               `json:"query"`
	URLParams *backend.WriteParams `json:"url_params"`
	FlowID    uint64               `json:"flow_id"`
}

// KafkaConfig kafka配置项
type KafkaConfig struct {
	Address     string
	Port        int
	TopicPrefix string
}

// MakeKafkaConfig : 创建并返回一个kafka配置对象，直接读取配置文件的信息
func MakeKafkaConfig() *KafkaConfig {
	c := common.Config

	return &KafkaConfig{
		Address:     c.GetString(common.ConfigKeyKafkaAddress),
		Port:        c.GetInt(common.ConfigKeyKafkaPort),
		TopicPrefix: c.GetString(common.ConfigKeyKafkaTopicPrefix),
	}
}

// GetBrokerAddress 获取Broker地址
func GetBrokerAddress() string {
	c := common.Config

	address := c.GetString(common.ConfigKeyKafkaAddress)
	port := c.GetString(common.ConfigKeyKafkaPort)

	return fmt.Sprintf("%s:%s", address, port)
}
