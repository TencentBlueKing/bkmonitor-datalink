// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

const (
	BkSqlStorageType           = "bk_sql"
	VictoriaMetricsStorageType = "victoria_metrics"
	InfluxDBStorageType        = "influxdb"
	PrometheusStorageType      = "prometheus"
	OfflineDataArchive         = "offline_data_archive"
	RedisStorageType           = "redis"
)

var typeList = []string{VictoriaMetricsStorageType, InfluxDBStorageType}

// GetTsDBStorageInfo 获取 tsDB 存储实例
func GetTsDBStorageInfo() (map[string]*Storage, error) {
	infos, err := GetStorageInfo()
	if err != nil {
		return nil, err
	}
	influxdbInfos := make(map[string]*Storage)
	for key, info := range infos {
		for _, t := range typeList {
			if info.Type == t {
				influxdbInfos[key] = info
			}
		}
	}
	return influxdbInfos, nil
}
