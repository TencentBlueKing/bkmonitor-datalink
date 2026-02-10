// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var typeList = []string{metadata.InfluxDBStorageType, metadata.ElasticsearchStorageType, metadata.BkSqlStorageType, metadata.VictoriaMetricsStorageType}

// GetTsDBStorageInfo 获取 tsDB 存储实例
func GetTsDBStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	infos, err := GetStorageInfo(ctx)
	if err != nil {
		return nil, err
	}
	tsdbInfos := make(map[string]*Storage)
	for key, info := range infos {
		for _, t := range typeList {
			if info.Type == t {
				tsdbInfos[key] = info
			}
		}
	}
	return tsdbInfos, nil
}
