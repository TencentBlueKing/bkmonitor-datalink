// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"encoding/json"
	"fmt"
	"strings"

	consul "github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

func ValidateDataSource(payload []byte) (*define.DataSource, error) {
	var data interface{}
	var err error
	err = json.Unmarshal(payload, &data)
	if err != nil {
		return nil, fmt.Errorf("json unmarshal error->(%v), origin data->(%s)", err, payload)
	}

	dataSource := &define.DataSource{}
	err = utils.ConvertByJSON(data, dataSource)
	if err != nil {
		return nil, fmt.Errorf("datasource watch event convert error: %+v, data: %+v", err, data)
	}
	option, err := dataSource.GetPluginOption()
	if err != nil {
		return nil, fmt.Errorf("DataSource(%d) get plugin info err: %+v", dataSource.DataID, err)
	}

	if option.GetRunMode() == define.PluginRunModeUnknown {
		return nil, fmt.Errorf("DataSource(%d) plugin type invalid", dataSource.DataID)
	}

	return dataSource, nil
}

func ListDataSources(consulPath string) (consul.KVPairs, error) {
	if !strings.HasSuffix(consulPath, "/") {
		consulPath = consulPath + "/"
	}
	client, err := consul.NewClient(NewConfig())
	if err != nil {
		return nil, err
	}
	kvPairs, _, err := client.KV().List(consulPath, nil)
	if err != nil {
		return nil, err
	}

	return kvPairs, nil
}

// ConvertShadowKVPair: consul kv对的镜像，其value是对原有KVPair进行序列化得到的，解析时需要再做一次反序列化
func ConvertShadowKVPair(kvPair *consul.KVPair) (*consul.KVPair, error) {
	// 需要对value进行额外一次反序列化，才能拿到原有的KV对
	actualKVPair := &consul.KVPair{}
	err := json.Unmarshal(kvPair.Value, actualKVPair)
	if err != nil {
		return nil, err
	}
	// 还是沿用原来的Key
	actualKVPair.Key = kvPair.Key
	return actualKVPair, nil
}

// ParseDataSourceFromKVPair: 从标准kv对获取DataSource对象
func ParseDataSourceFromKVPair(kvPair *consul.KVPair) (*define.DataSourceKVPair, error) {
	dataSource, err := ValidateDataSource(kvPair.Value)
	if err != nil {
		return nil, err
	}
	return &define.DataSourceKVPair{
		Pair:       kvPair,
		DataSource: dataSource,
	}, nil
}

// ParseDataSourceFromShadowKVPair: 从影子kv对获取DataSource对象
func ParseDataSourceFromShadowKVPair(kvPair *consul.KVPair) (*define.DataSourceKVPair, error) {
	actualKVPair, err := ConvertShadowKVPair(kvPair)
	if err != nil {
		return nil, err
	}
	return ParseDataSourceFromKVPair(actualKVPair)
}

func GetServiceNameFromShadow(kvPair *consul.KVPair) string {
	paths := strings.Split(strings.Trim(kvPair.Key, "/"), "/")
	if len(paths) < 2 {
		return ""
	}
	// 路径的倒数第二个是服务ID
	service := paths[len(paths)-2]
	return service
}
