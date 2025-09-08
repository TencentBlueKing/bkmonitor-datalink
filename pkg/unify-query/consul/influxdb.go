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
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

const (
	influxdbInfoPath = "influxdb/info"
	influxdbStorage  = "influxdb"
)

// InfluxdbTableInfo
type InfluxdbTableInfo struct {
	// 存储类型，如果为free则需要特殊处理
	PivotTable bool `json:"pivot_table"`

	// 分段查询开关
	SegmentedQueryEnable bool `json:"segmented_query_enable"`

	// table_id对应的influxdb集群版本号，用于决定ast逻辑的版本
	InfluxdbVersion string `json:"influxdb_version"`
}

// FormatESTableInfo :
func FormatInfluxdbTableInfo(kvPairs api.KVPairs) (map[string]*InfluxdbTableInfo, error) {
	result := make(map[string]*InfluxdbTableInfo)
	for _, kvPair := range kvPairs {
		var data *InfluxdbTableInfo
		err := json.Unmarshal(kvPair.Value, &data)
		if err != nil {
			return nil, err
		}
		prefix := fmt.Sprintf("%s/%s/%s/", basePath, dataPath, influxdbInfoPath)
		key := strings.ReplaceAll(string(kvPair.Key), prefix, "")
		result[key] = data
	}
	return result, nil
}

// WatchInfluxdbTableInfo
func WatchInfluxdbTableInfo(ctx context.Context) (<-chan any, error) {
	path := fmt.Sprintf("%s/%s/%s", basePath, versionPath, influxdbInfoPath)
	return WatchChange(ctx, path)
}

// GetInfluxdbTableInfo
func GetInfluxdbTableInfo() (map[string]*InfluxdbTableInfo, error) {
	path := fmt.Sprintf("%s/%s/%s", basePath, dataPath, influxdbInfoPath)
	pairs, err := GetDataWithPrefix(path)
	if err != nil {
		return nil, err
	}
	return FormatInfluxdbTableInfo(pairs)
}

// GetInfluxdbStorageInfo 获取 influxdb 存储实例
func GetInfluxdbStorageInfo() (map[string]*Storage, error) {
	infos, err := GetStorageInfo()
	if err != nil {
		return nil, err
	}
	influxdbInfos := make(map[string]*Storage)
	for key, info := range infos {
		if info.Type != influxdbStorage {
			continue
		}
		influxdbInfos[key] = info
	}
	return influxdbInfos, nil
}
