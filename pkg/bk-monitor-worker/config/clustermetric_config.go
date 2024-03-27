// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
)

var (
	ClusterMetricStorageKeyPrefix string
	ClusterMetricStorageTTL       int
	ClusterMetricKey              string
	ClusterMetricMetaKey          string
	ClusterMetricSubKeyPattern    string
	ClusterMetricClusterFieldName string
	ClusterMetricFieldName        string
	ClusterMetricHostFieldName    string
	ESClusterMetricTarget         string
)

func initClusterMetricVariables() {
	ClusterMetricStorageKeyPrefix = GetValue("taskConfig.cluster_metrics.storage_key_prefix", "bkmonitor")
	ClusterMetricStorageTTL = GetValue("taskConfig.cluster_metrics.storage_ttl", 300)
	ClusterMetricKey = fmt.Sprintf("%s:cluster_metrics", ClusterMetricStorageKeyPrefix)
	ClusterMetricMetaKey = fmt.Sprintf("%s:cluster_metrics_meta", ClusterMetricStorageKeyPrefix)

	ClusterMetricSubKeyPattern = "{bkm_metric_name}|bkm_cluster={bkm_cluster}"
	ClusterMetricClusterFieldName = "bkm_cluster"
	ClusterMetricFieldName = "bkm_metric_name"
	ClusterMetricHostFieldName = "bkm_hostname"

	ESClusterMetricTarget = "log-search-4"
}
