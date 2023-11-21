// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "github.com/spf13/viper"

var (
	// MetadataMetricDimensionMetricKeyPrefix config of metadata.refreshMetric task
	MetadataMetricDimensionMetricKeyPrefix string
	// MetadataMetricDimensionKeyPrefix config of metadata.refreshMetric task
	MetadataMetricDimensionKeyPrefix string
	// MetadataMetricDimensionMaxMetricFetchStep config of metadata.refreshMetric task
	MetadataMetricDimensionMaxMetricFetchStep int
	// MetadataMetricDimensionTimeSeriesMetricExpiredDays config of metadata.refreshMetric task
	MetadataMetricDimensionTimeSeriesMetricExpiredDays int

	// BcsEnableBcsGray  是否启用BCS集群灰度模式
	BcsEnableBcsGray bool
	// BcsGrayClusterIdList BCS集群灰度ID名单
	BcsGrayClusterIdList []string
	// BcsClusterBkEnvLabel BCS集群配置来源标签
	BcsClusterBkEnvLabel string
	// BcsKafkaStorageClusterId BCS kafka 存储集群ID
	BcsKafkaStorageClusterId uint
	// BcsInfluxdbDefaultProxyClusterNameForK8s influxdb proxy给k8s默认使用集群名
	BcsInfluxdbDefaultProxyClusterNameForK8s string
	// BcsCustomEventStorageClusterId 自定义上报存储集群ID
	BcsCustomEventStorageClusterId uint
)

func initMetadataVariables() {
	MetadataMetricDimensionMetricKeyPrefix = GetValue("taskConfig.metadata.metricDimension.metricKeyPrefix", "bkmonitor:metrics_")
	MetadataMetricDimensionKeyPrefix = GetValue("taskConfig.metadata.metricDimension.metricDimensionKeyPrefix", "bkmonitor:metric_dimensions_")
	MetadataMetricDimensionMaxMetricFetchStep = GetValue("taskConfig.metadata.metricDimension.maxMetricsFetchStep", 500)
	MetadataMetricDimensionTimeSeriesMetricExpiredDays = GetValue("taskConfig.metadata.metricDimension.timeSeriesMetricExpiredDays", 30)

	BcsEnableBcsGray = GetValue("taskConfig.metadata.bcs.enableBcsGray", false)
	BcsGrayClusterIdList = GetValue("taskConfig.metadata.bcs.grayClusterIdList", []string{})
	BcsClusterBkEnvLabel = GetValue("taskConfig.metadata.bcs.clusterBkEnvLabel", "")
	BcsKafkaStorageClusterId = GetValue("taskConfig.metadata.bcs.kafkaStorageClusterId", uint(0), viper.GetUint)
	BcsInfluxdbDefaultProxyClusterNameForK8s = GetValue("taskConfig.metadata.bcs.influxdbDefaultProxyClusterNameForK8s", "default")
	BcsCustomEventStorageClusterId = GetValue("taskConfig.metadata.bcs.customEventStorageClusterId", uint(0), viper.GetUint)
}
