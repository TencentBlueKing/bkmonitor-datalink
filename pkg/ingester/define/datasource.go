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
	consul "github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

const (
	EtlConfigName = "bk_fta_event"
)

type MetaClusterInfo struct {
	ClusterConfig map[string]interface{} `json:"cluster_config"`
	StorageConfig map[string]interface{} `json:"storage_config"`
	AuthInfo      map[string]interface{} `json:"auth_info"`
	ClusterType   string                 `json:"cluster_type"`
}

type DataSource struct {
	DataID    int             `json:"data_id"`
	MQConfig  MetaClusterInfo `json:"mq_config"`
	ETLConfig string          `json:"etl_config"`
	Option    interface{}     `json:"option"`
	Token     string          `json:"token"`
	plugin    *Plugin
}

func (d *DataSource) GetPluginOption() (*Plugin, error) {
	if d.plugin == nil {
		plugin := &Plugin{}
		err := utils.ConvertByJSON(d.Option, plugin)
		if err != nil {
			return nil, err
		}
		d.plugin = plugin
	}
	return d.plugin, nil
}

func (d *DataSource) MustGetPluginOption() *Plugin {
	plugin, err := d.GetPluginOption()
	if err != nil {
		panic(err)
	}
	return plugin
}

type DataSourceKVPair struct {
	Pair       *consul.KVPair
	DataSource *DataSource
}
