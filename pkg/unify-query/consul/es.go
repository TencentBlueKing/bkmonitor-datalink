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
	esInfoPath = "es/info"
)

// ESTableInfo
type ESTableInfo struct {
	// 存储id，对应一个es实例(或集群)
	StorageID int `json:"storage_id"`
	// 别名生成格式
	AliasFormat string `json:"alias_format"`
	// 日期生成格式,会被time.Format使用
	DateFormat string `json:"date_format"`
	// 日期步长,单位: h
	DateStep int `json:"date_step"`
}

// FormatESTableInfo :
func FormatESTableInfo(kvPairs api.KVPairs) (map[string]*ESTableInfo, error) {
	result := make(map[string]*ESTableInfo)
	for _, kvPair := range kvPairs {
		var data *ESTableInfo
		err := json.Unmarshal(kvPair.Value, &data)
		if err != nil {
			return nil, err
		}
		prefix := fmt.Sprintf("%s/%s/%s/", basePath, dataPath, esInfoPath)
		key := strings.ReplaceAll(string(kvPair.Key), prefix, "")
		result[key] = data
	}
	return result, nil
}

// WatchESTableInfo
func WatchESTableInfo(ctx context.Context) (<-chan any, error) {
	path := fmt.Sprintf("%s/%s/%s", basePath, versionPath, esInfoPath)
	return WatchChange(ctx, path)
}

// GetESTableInfo
func GetESTableInfo() (map[string]*ESTableInfo, error) {
	path := fmt.Sprintf("%s/%s/%s", basePath, dataPath, esInfoPath)
	pairs, err := GetDataWithPrefix(path)
	if err != nil {
		return nil, err
	}
	return FormatESTableInfo(pairs)
}

// GetESStorageInfo
func GetESStorageInfo() (map[string]*Storage, error) {
	infos, err := GetStorageInfo()
	if err != nil {
		return nil, err
	}
	esInfos := make(map[string]*Storage)
	for key, info := range infos {
		if info.Type != "elasticsearch" {
			continue
		}
		esInfos[key] = info
	}
	return esInfos, nil
}
