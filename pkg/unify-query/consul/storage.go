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

var storagePath = "storage"

// Storage
type Storage struct {
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
	Type     string `json:"type"`
}

// FormatESStorageInfo :
func FormatStorageInfo(kvPairs api.KVPairs) (map[string]*Storage, error) {
	result := make(map[string]*Storage)
	for _, kvPair := range kvPairs {
		var data *Storage
		err := json.Unmarshal(kvPair.Value, &data)
		if err != nil {
			return nil, err
		}
		prefix := fmt.Sprintf("%s/%s/%s/", basePath, dataPath, storagePath)
		key := strings.ReplaceAll(string(kvPair.Key), prefix, "")
		result[key] = data
	}
	return result, nil
}

// GetStorageInfo
func GetStorageInfo() (map[string]*Storage, error) {
	path := fmt.Sprintf("%s/%s/%s", basePath, dataPath, storagePath)
	pairs, err := GetDataWithPrefix(path)
	if err != nil {
		return nil, err
	}
	return FormatStorageInfo(pairs)
}

// WatchStorageInfo
func WatchStorageInfo(ctx context.Context) (<-chan any, error) {
	path := fmt.Sprintf("%s/%s/%s", basePath, versionPath, storagePath)
	return WatchChange(ctx, path)
}
