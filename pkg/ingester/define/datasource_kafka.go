// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type kafkaAuthInfo struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type kafkaClusterConfig struct {
	ClusterName string `json:"cluster_name"`
	DomainName  string `json:"domain_name"`
	Port        int    `json:"port"`
}

type kafkaStorageConfig struct {
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
}

type KafkaMetaClusterInfo struct {
	ClusterType   string             `json:"cluster_type"`
	ClusterConfig kafkaClusterConfig `json:"cluster_config"`
	StorageConfig kafkaStorageConfig `json:"storage_config"`
	AuthInfo      kafkaAuthInfo      `json:"auth_info"`
}

type KafkaDataSource struct {
	DataSource
	MQConfig KafkaMetaClusterInfo `json:"mq_config"`
}

func (ds *KafkaDataSource) GetAddress() string {
	return fmt.Sprintf("%s:%d", ds.MQConfig.ClusterConfig.DomainName, ds.MQConfig.ClusterConfig.Port)
}

func NewKafkaDataSource(d *DataSource) (*KafkaDataSource, error) {
	k := &KafkaDataSource{}
	err := utils.ConvertByJSON(d, k)
	return k, err
}
