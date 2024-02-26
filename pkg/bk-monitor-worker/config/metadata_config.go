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
	// GlobalBkdataProjectId 监控在计算平台使用的公共项目ID
	GlobalBkdataProjectId int
	// GlobalBkdataRealtimeNodeWaitTime 计算平台实时节点等待时间
	GlobalBkdataRealtimeNodeWaitTime int
	// GlobalBkdataDataExpiresDays 计算平台中结果表(MYSQL)默认保存天数
	GlobalBkdataDataExpiresDays int
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
	// GlobalBkdataRawTableSuffix 数据接入前缀
	GlobalBkdataRawTableSuffix string
	// GlobalBkdataCMDBFullTableSuffix 补充cmdb节点信息后的表后缀
	GlobalBkdataCMDBFullTableSuffix string
	// GlobalBkdataCMDBSplitTableSuffix 补充表拆分后的表后缀
	GlobalBkdataCMDBSplitTableSuffix string
	// GlobalBkdataDruidStorageClusterName 监控专属druid存储集群名称
	GlobalBkdataDruidStorageClusterName string
	// GlobalBkdataMysqlStorageClusterName 监控专属tspider存储集群名称
	GlobalBkdataMysqlStorageClusterName string
	// GlobalBkdataFlowClusterGroup 计算平台 dataflow 计算集群组
	GlobalBkdataFlowClusterGroup string
	// GlobalBkdataProjectMaintainer 计算平台项目的维护人员
	GlobalBkdataProjectMaintainer string
	// GlobalAccessDbmRtSpaceUid 访问 dbm 结果表的空间 UID
	GlobalAccessDbmRtSpaceUid []string
	// GlobalTsDataSavedDays 监控采集数据保存天数
	GlobalTsDataSavedDays int
	// GlobalIsAllowAllCmdbLevel 是否允许所有数据源配置CMDB聚合
	GlobalIsAllowAllCmdbLevel bool
	// GlobalCustomReportDefaultProxyIp 自定义上报默认服务器
	GlobalCustomReportDefaultProxyIp []string
	// GlobalIsAutoDeployCustomReportServer 是否自动部署自定义上报服务
	GlobalIsAutoDeployCustomReportServer bool
	// GlobalIPV6SupportBizList 支持ipv6的业务列表
	GlobalIPV6SupportBizList []int
	// GlobalHostDisableMonitorStates 主机不监控字段列表
	GlobalHostDisableMonitorStates []string

	// PingServerEnablePingAlarm 全局 Ping 告警开关
	PingServerEnablePingAlarm bool
	// PingServerEnableDirectAreaPingCollect 是否开启直连区域的PING采集
	PingServerEnableDirectAreaPingCollect bool
	// PingServerDataid ping server dataid
	PingServerDataid uint

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
	GlobalBkdataProjectId = GetValue("taskConfig.metadata.global.bkdataProjectId", 1)
	GlobalBkdataRealtimeNodeWaitTime = GetValue("taskConfig.metadata.global.bkdataRealtimeNodeWaitTime", 10)
	GlobalBkdataDataExpiresDays = GetValue("taskConfig.metadata.global.bkdataDataExpiresDays", 30)
	GlobalDefaultKafkaStorageClusterId = GetValue("taskConfig.metadata.global.defaultKafkaStorageClusterId", uint(0), viper.GetUint)
	GlobalBkappDeployPlatform = GetValue("taskConfig.metadata.global.bkappDeployPlatform", "enterprise")
	GlobalBkdataRtIdPrefix = GetValue("taskConfig.metadata.global.bkdataRtIdPrefix", GlobalBkappDeployPlatform)
	GlobalBkdataBkBizId = GetValue("taskConfig.metadata.global.bkdataBkBizId", 2)
	GlobalBkdataRawTableSuffix = GetValue("taskConfig.metadata.global.bkdataRawTableSuffix", "raw")
	GlobalBkdataCMDBFullTableSuffix = GetValue("taskConfig.metadata.global.bkdataCMDBFullTableSuffix", "full")
	GlobalBkdataCMDBSplitTableSuffix = GetValue("taskConfig.metadata.global.bkdataCMDBFSplitTableSuffix", "cmdb")
	GlobalBkdataDruidStorageClusterName = GetValue("taskConfig.metadata.global.bkdataDruidStorageClusterName", "monitor")
	GlobalBkdataMysqlStorageClusterName = GetValue("taskConfig.metadata.global.bkdataMysqlStorageClusterName", "jungle_alert")
	GlobalBkdataFlowClusterGroup = GetValue("taskConfig.metadata.global.bkdataFlowClusterGroup", "default_inland")
	GlobalBkdataProjectMaintainer = GetValue("taskConfig.metadata.global.bkdataProjectMaintainer", "admin")
	GlobalAccessDbmRtSpaceUid = GetValue("taskConfig.metadata.global.accessDbmRtSpaceUid", []string{})
	GlobalTsDataSavedDays = GetValue("taskConfig.metadata.global.tsDataSavedDays", 30)
	GlobalIsAllowAllCmdbLevel = GetValue("taskConfig.metadata.global.isAllowAllCmdbLevel", false)
	GlobalCustomReportDefaultProxyIp = GetValue("taskConfig.metadata.global.customReportDefaultProxyIp", []string{})
	GlobalIsAutoDeployCustomReportServer = GetValue("taskConfig.metadata.global.isAutoDeployCustomReportServer", true)
	GlobalIPV6SupportBizList = GetValue("taskConfig.metadata.global.ipv6SupportBizList", []int{})
	GlobalHostDisableMonitorStates = GetValue("taskConfig.metadata.global.hostDisableMonitorStates", []string{"备用机", "测试中", "故障中"})

	PingServerEnablePingAlarm = GetValue("taskConfig.metadata.pingserver.enablePingAlarm", true)
	PingServerEnableDirectAreaPingCollect = GetValue("taskConfig.metadata.pingserver.enableDirectAreaPingCollect", true)
	PingServerDataid = GetValue("taskConfig.metadata.pingserver.dataid", uint(1100005), viper.GetUint)

	SpaceRedisKey = GetValue("taskConfig.metadata.space.redisKey", fmt.Sprintf("bkmonitorv3:spaces%s", BypassSuffixPath))
	DataLabelToResultTableKey = GetValue("taskConfig.metadata.space.dataLabelToResultTableKey", fmt.Sprintf("%s:data_label_to_result_table", SpaceRedisKey))
	DataLabelToResultTableChannel = GetValue("taskConfig.metadata.space.dataLabelToResultTableChannel", fmt.Sprintf("%s:data_label_to_result_table:channel", SpaceRedisKey))
	ResultTableDetailKey = GetValue("taskConfig.metadata.space.resultTableDetailKey", fmt.Sprintf("%s:result_table_detail", SpaceRedisKey))
	ResultTableDetailChannel = GetValue("taskConfig.metadata.space.resultTableDetailChannel", fmt.Sprintf("%s:result_table_detail:channel", SpaceRedisKey))
	SpaceToResultTableKey = GetValue("taskConfig.metadata.space.spaceToResultTableKey", fmt.Sprintf("%s:space_to_result_table", SpaceRedisKey))
	SpaceToResultTableChannel = GetValue("taskConfig.metadata.space.spaceToResultTableChannel", fmt.Sprintf("%s:space_to_result_table:channel", SpaceRedisKey))
}
