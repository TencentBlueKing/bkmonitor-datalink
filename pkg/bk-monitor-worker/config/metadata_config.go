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

	"github.com/spf13/viper"
)

var (
	// MetadataMetricDimensionMetricKeyPrefix config of metadata.refreshMetric task
	MetadataMetricDimensionMetricKeyPrefix string
	// MetadataMetricDimensionKeyPrefix config of metadata.refreshMetric task
	MetadataMetricDimensionKeyPrefix string
	// MetadataMetricDimensionMaxMetricFetchStep config of metadata.refreshMetric task
	MetadataMetricDimensionMaxMetricFetchStep int

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

	// GlobalFetchTimeSeriesMetricIntervalSeconds 获取指标的间隔时间
	GlobalFetchTimeSeriesMetricIntervalSeconds int
	// GlobalTimeSeriesMetricExpiredSeconds 自定义指标过期时间
	GlobalTimeSeriesMetricExpiredSeconds int
	// GlobalIsRestrictDsBelongSpace 是否限制数据源归属具体空间
	GlobalIsRestrictDsBelongSpace bool
	// GlobalDefaultBkdataBizId 接入计算平台使用的业务 ID
	GlobalDefaultBkdataBizId int
	// GlobalDefaultKafkaStorageClusterId 默认 kafka 存储集群ID
	GlobalDefaultKafkaStorageClusterId uint
	// GlobalBkdataKafkaBrokerUrl 与计算平台对接的消息队列BROKER地址
	GlobalBkdataKafkaBrokerUrl string
	// GlobalBkappDeployPlatform 监控平台版本
	GlobalBkappDeployPlatform string
	// GlobalBkdataRtIdPrefix 监控在计算平台的数据表前缀
	GlobalBkdataRtIdPrefix string
	// GlobalBkdataBkBizId 监控在计算平台使用的公共业务ID
	GlobalBkdataBkBizId int
	// GlobalBkdataProjectMaintainer 计算平台项目的维护人员
	GlobalBkdataProjectMaintainer string
	// GlobalAccessDbmRtSpaceUid 访问 dbm 结果表的空间 UID
	GlobalAccessDbmRtSpaceUid []string
	// GlobalTsDataSavedDays 监控采集数据保存天数
	GlobalTsDataSavedDays int

	// SpaceRedisKey redis 中空间的 key
	SpaceRedisKey string
	// DataLabelToResultTableKey 数据标签关联的结果表key
	DataLabelToResultTableKey string
	// DataLabelToResultTableChannel 数据标签关联的结果表channel
	DataLabelToResultTableChannel string
	// ResultTableDetailKey 结果表详情key
	ResultTableDetailKey string
	// ResultTableDetailChannel 结果表详情channel
	ResultTableDetailChannel string
	// SpaceToResultTableKey 空间关联的结果表key
	SpaceToResultTableKey string
	// SpaceToResultTableChannel 空间关联的结果表channel
	SpaceToResultTableChannel string
)

func initMetadataVariables() {
	MetadataMetricDimensionMetricKeyPrefix = GetValue("taskConfig.metadata.metricDimension.metricKeyPrefix", "bkmonitor:metrics_")
	MetadataMetricDimensionKeyPrefix = GetValue("taskConfig.metadata.metricDimension.metricDimensionKeyPrefix", "bkmonitor:metric_dimensions_")
	MetadataMetricDimensionMaxMetricFetchStep = GetValue("taskConfig.metadata.metricDimension.maxMetricsFetchStep", 500)

	BcsEnableBcsGray = GetValue("taskConfig.metadata.bcs.enableBcsGray", false)
	BcsGrayClusterIdList = GetValue("taskConfig.metadata.bcs.grayClusterIdList", []string{})
	BcsClusterBkEnvLabel = GetValue("taskConfig.metadata.bcs.clusterBkEnvLabel", "")
	BcsKafkaStorageClusterId = GetValue("taskConfig.metadata.bcs.kafkaStorageClusterId", uint(0), viper.GetUint)
	BcsInfluxdbDefaultProxyClusterNameForK8s = GetValue("taskConfig.metadata.bcs.influxdbDefaultProxyClusterNameForK8s", "default")
	BcsCustomEventStorageClusterId = GetValue("taskConfig.metadata.bcs.customEventStorageClusterId", uint(0), viper.GetUint)

	GlobalFetchTimeSeriesMetricIntervalSeconds = GetValue("taskConfig.metadata.global.fetchTimeSeriesMetricIntervalSeconds", 7200)
	GlobalTimeSeriesMetricExpiredSeconds = GetValue("taskConfig.metadata.global.timeSeriesMetricExpiredSeconds", 30*24*3600)
	GlobalIsRestrictDsBelongSpace = GetValue("taskConfig.metadata.global.isRestrictDsBelongSpace", true)
	GlobalDefaultBkdataBizId = GetValue("taskConfig.metadata.global.defaultBkdataBizId", 0)
	GlobalDefaultKafkaStorageClusterId = GetValue("taskConfig.metadata.global.defaultKafkaStorageClusterId", uint(0), viper.GetUint)
	GlobalBkappDeployPlatform = GetValue("taskConfig.metadata.global.bkappDeployPlatform", "enterprise")
	GlobalBkdataRtIdPrefix = GetValue("taskConfig.metadata.global.bkdataRtIdPrefix", GlobalBkappDeployPlatform)
	GlobalBkdataBkBizId = GetValue("taskConfig.metadata.global.bkdataBkBizId", 2)
	GlobalBkdataProjectMaintainer = GetValue("taskConfig.metadata.global.bkdataProjectMaintainer", "admin")
	GlobalAccessDbmRtSpaceUid = GetValue("taskConfig.metadata.global.accessDbmRtSpaceUid", []string{})
	GlobalTsDataSavedDays = GetValue("taskConfig.metadata.global.tsDataSavedDays", 30)

	SpaceRedisKey = GetValue("taskConfig.metadata.space.redisKey", fmt.Sprintf("bkmonitorv3:spaces%s", BypassSuffixPath))
	DataLabelToResultTableKey = GetValue("taskConfig.metadata.space.dataLabelToResultTableKey", fmt.Sprintf("%s:data_label_to_result_table", SpaceRedisKey))
	DataLabelToResultTableChannel = GetValue("taskConfig.metadata.space.dataLabelToResultTableChannel", fmt.Sprintf("%s:data_label_to_result_table:channel", SpaceRedisKey))
	ResultTableDetailKey = GetValue("taskConfig.metadata.space.resultTableDetailKey", fmt.Sprintf("%s:result_table_detail", SpaceRedisKey))
	ResultTableDetailChannel = GetValue("taskConfig.metadata.space.resultTableDetailChannel", fmt.Sprintf("%s:result_table_detail:channel", SpaceRedisKey))
	SpaceToResultTableKey = GetValue("taskConfig.metadata.space.spaceToResultTableKey", fmt.Sprintf("%s:space_to_result_table", SpaceRedisKey))
	SpaceToResultTableChannel = GetValue("taskConfig.metadata.space.spaceToResultTableChannel", fmt.Sprintf("%s:space_to_result_table:channel", SpaceRedisKey))
}
