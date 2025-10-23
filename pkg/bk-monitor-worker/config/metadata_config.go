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
	"time"

	"github.com/spf13/viper"
)

var (
	// MetadataMetricDimensionMetricKeyPrefix config of metadata.refreshMetric task
	MetadataMetricDimensionMetricKeyPrefix string
	// MetadataMetricDimensionKeyPrefix config of metadata.refreshMetric task
	MetadataMetricDimensionKeyPrefix string
	// MetadataMetricDimensionMaxMetricFetchStep config of metadata.refreshMetric task
	MetadataMetricDimensionMaxMetricFetchStep int
	// MetadataMetricDimensionByBkData refresh metric dimension by bkdata
	MetadataMetricDimensionByBkData bool
	// MetadataTableIdListForBkDataTsMetrics refresh metadata table_id dimension by bkdata
	BkDataTableIdListRedisPath string

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
	// BkciSpaceAccessPlugins 允许被项目空间访问业务数据的RT列表
	BkciSpaceAccessPlugins []string

	// QueryDbTableIdBatchSize 查询DB的table_id批量大小
	QueryDbTableIdBatchSize int
	// QueryDbBatchSize 查询DB的批量大小
	QueryDbBatchSize int
	// QueryDbBatchDelay 查询DB的批量延迟时间
	QueryDbBatchDelay time.Duration

	// SpecialRtRouterAliasResultTableList 特殊的路由别名结果表列表
	SpecialRtRouterAliasResultTableList []string

	// GlobalFetchTimeSeriesMetricIntervalSeconds 获取指标的间隔时间
	GlobalFetchTimeSeriesMetricIntervalSeconds int
	// GlobalTimeSeriesMetricExpiredSeconds 自定义指标过期时间
	GlobalTimeSeriesMetricExpiredSeconds int
	// GlobalIsRestrictDsBelongSpace 是否限制数据源归属具体空间
	GlobalIsRestrictDsBelongSpace bool
	// GlobalDefaultKafkaStorageClusterId 默认 kafka 存储集群ID
	GlobalDefaultKafkaStorageClusterId uint
	// GlobalBkappDeployPlatform 监控平台版本
	GlobalBkappDeployPlatform string
	// GlobalAccessDbmRtSpaceUid 访问 dbm 结果表的空间 UID
	GlobalAccessDbmRtSpaceUid []string
	// GlobalTsDataSavedDays 监控采集数据保存天数
	GlobalTsDataSavedDays int
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
	// BuildInResultTableDetailKey 空间关联内置上报rt详情
	BuildInResultTableDetailKey string
	// BkAppToSpaceKey redis 中 bkApp 的 key
	BkAppToSpaceKey string
	// BkAppToSpaceChannelKey bkAppCode 关联 space 的 channel
	BkAppToSpaceChannelKey string

	// BkdataDefaultBizId 接入计算平台使用的业务 ID
	BkdataDefaultBizId int
	// BkdataProjectId 监控在计算平台使用的公共项目ID
	BkdataProjectId int
	// BkdataRealtimeNodeWaitTime 计算平台实时节点等待时间
	BkdataRealtimeNodeWaitTime int
	// BkdataDataExpiresDays 计算平台中结果表(MYSQL)默认保存天数
	BkdataDataExpiresDays int
	// BkdataKafkaBrokerUrl 与计算平台对接的消息队列BROKER地址
	BkdataKafkaBrokerUrl string
	// BkdataRtIdPrefix 监控在计算平台的数据表前缀
	BkdataRtIdPrefix string
	// BkdataBkBizId 监控在计算平台使用的公共业务ID
	BkdataBkBizId int
	// BkdataRawTableSuffix 数据接入前缀
	BkdataRawTableSuffix string
	// BkdataCMDBFullTableSuffix 补充cmdb节点信息后的表后缀
	BkdataCMDBFullTableSuffix string
	// BkdataCMDBSplitTableSuffix 补充表拆分后的表后缀
	BkdataCMDBSplitTableSuffix string
	// BkdataDruidStorageClusterName 监控专属druid存储集群名称
	BkdataDruidStorageClusterName string
	// BkdataMysqlStorageClusterName 监控专属tspider存储集群名称
	BkdataMysqlStorageClusterName string
	// BkdataMysqlStorageClusterType 计算平台 SQL 类存储集群类型
	BkdataMysqlStorageClusterType string
	// BkdataFlowClusterGroup 计算平台 dataflow 计算集群组
	BkdataFlowClusterGroup string
	// BkdataProjectMaintainer 计算平台项目的维护人员
	BkdataProjectMaintainer string
	// BkdataIsAllowAllCmdbLevel 是否允许所有数据源配置CMDB聚合
	BkdataIsAllowAllCmdbLevel bool
	// 跳过写入influxdb的结果表列表
	SkipInfluxdbTableIds []string
	// 是否可以删除 consul 路径
	CanDeleteConsulPath bool

	// SloPushGatewayToken slo数据上报Token
	SloPushGatewayToken string
	// SloPushGatewayEndpoint slo数据上报端点
	SloPushGatewayEndpoint string

	// InitialMaxWaitTime 初始最大等待时间
	InitialMaxWaitTime string
)

func initMetadataVariables() {
	MetadataMetricDimensionMetricKeyPrefix = GetValue("taskConfig.metadata.metricDimension.metricKeyPrefix", "bkmonitor:metrics_")
	MetadataMetricDimensionKeyPrefix = GetValue("taskConfig.metadata.metricDimension.metricDimensionKeyPrefix", "bkmonitor:metric_dimensions_")
	MetadataMetricDimensionMaxMetricFetchStep = GetValue("taskConfig.metadata.metricDimension.maxMetricsFetchStep", 500)
	MetadataMetricDimensionByBkData = GetValue("taskConfig.metadata.metricDimension.metadataMetricDimensionByBkData", false)
	BkDataTableIdListRedisPath = GetValue("taskConfig.metadata.metricDimension.BkDataTableIdListRedisPath", "metadata:query_metric:table_id_list")

	BcsEnableBcsGray = GetValue("taskConfig.metadata.bcs.enableBcsGray", false)
	BcsGrayClusterIdList = GetValue("taskConfig.metadata.bcs.grayClusterIdList", []string{})
	BcsClusterBkEnvLabel = GetValue("taskConfig.metadata.bcs.clusterBkEnvLabel", "")
	BcsKafkaStorageClusterId = GetValue("taskConfig.metadata.bcs.kafkaStorageClusterId", uint(0), viper.GetUint)
	BcsInfluxdbDefaultProxyClusterNameForK8s = GetValue("taskConfig.metadata.bcs.influxdbDefaultProxyClusterNameForK8s", "default")
	BcsCustomEventStorageClusterId = GetValue("taskConfig.metadata.bcs.customEventStorageClusterId", uint(0), viper.GetUint)
	GlobalFetchTimeSeriesMetricIntervalSeconds = GetValue("taskConfig.metadata.global.fetchTimeSeriesMetricIntervalSeconds", 7200)
	GlobalTimeSeriesMetricExpiredSeconds = GetValue("taskConfig.metadata.global.timeSeriesMetricExpiredSeconds", 30*24*3600)
	GlobalIsRestrictDsBelongSpace = GetValue("taskConfig.metadata.global.isRestrictDsBelongSpace", true)
	GlobalDefaultKafkaStorageClusterId = GetValue("taskConfig.metadata.global.defaultKafkaStorageClusterId", uint(0), viper.GetUint)
	GlobalBkappDeployPlatform = GetValue("taskConfig.metadata.global.bkappDeployPlatform", "enterprise")
	GlobalAccessDbmRtSpaceUid = GetValue("taskConfig.metadata.global.accessDbmRtSpaceUid", []string{})
	GlobalTsDataSavedDays = GetValue("taskConfig.metadata.global.tsDataSavedDays", 30)
	GlobalCustomReportDefaultProxyIp = GetValue("taskConfig.metadata.global.customReportDefaultProxyIp", []string{})
	GlobalIsAutoDeployCustomReportServer = GetValue("taskConfig.metadata.global.isAutoDeployCustomReportServer", true)
	GlobalIPV6SupportBizList = GetValue("taskConfig.metadata.global.ipv6SupportBizList", []int{})
	GlobalHostDisableMonitorStates = GetValue("taskConfig.metadata.global.hostDisableMonitorStates", []string{"备用机", "测试中", "故障中"})
	BkciSpaceAccessPlugins = GetValue("taskConfig.metadata.bcs.bkciSpaceAccessPlugins", []string{})

	QueryDbTableIdBatchSize = GetValue("taskConfig.metadata.bcs.queryDbTableIdBatchSize", 200)
	QueryDbBatchSize = GetValue("taskConfig.metadata.bcs.queryDbBatchSize", 10000)
	// 优先使用毫秒配置，如果没有配置则使用默认值
	queryDbBatchDelayMs := GetValue("taskConfig.metadata.bcs.queryDbBatchDelayMs", 20)
	QueryDbBatchDelay = time.Duration(queryDbBatchDelayMs) * time.Millisecond

	SpecialRtRouterAliasResultTableList = GetValue("taskConfig.metadata.bcs.specialRtRouterAliasResultTableList", []string{})

	PingServerEnablePingAlarm = GetValue("taskConfig.metadata.pingserver.enablePingAlarm", true)
	PingServerEnableDirectAreaPingCollect = GetValue("taskConfig.metadata.pingserver.enableDirectAreaPingCollect", true)
	PingServerDataid = GetValue("taskConfig.metadata.pingserver.dataid", uint(1100005), viper.GetUint)

	SpaceRedisKey = GetValue("taskConfig.metadata.space.redisKey", "bkmonitorv3:spaces")
	DataLabelToResultTableKey = GetValue("taskConfig.metadata.space.dataLabelToResultTableKey", fmt.Sprintf("%s:data_label_to_result_table", SpaceRedisKey))
	DataLabelToResultTableChannel = GetValue("taskConfig.metadata.space.dataLabelToResultTableChannel", fmt.Sprintf("%s:data_label_to_result_table:channel", SpaceRedisKey))
	ResultTableDetailKey = GetValue("taskConfig.metadata.space.resultTableDetailKey", fmt.Sprintf("%s:result_table_detail", SpaceRedisKey))
	ResultTableDetailChannel = GetValue("taskConfig.metadata.space.resultTableDetailChannel", fmt.Sprintf("%s:result_table_detail:channel", SpaceRedisKey))
	SpaceToResultTableKey = GetValue("taskConfig.metadata.space.spaceToResultTableKey", fmt.Sprintf("%s:space_to_result_table", SpaceRedisKey))
	SpaceToResultTableChannel = GetValue("taskConfig.metadata.space.spaceToResultTableChannel", fmt.Sprintf("%s:space_to_result_table:channel", SpaceRedisKey))
	BuildInResultTableDetailKey = GetValue("taskConfig.metadata.space.buildInResultTableDetailKey", fmt.Sprintf("%s:built_in_result_table_detail", SpaceRedisKey))
	BkAppToSpaceKey = GetValue("taskConfig.metadata.space.bkAppSpace", fmt.Sprintf("%s:bk_app_to_space", SpaceRedisKey))
	BkAppToSpaceChannelKey = GetValue("taskConfig.metadata.space.bkAppSpaceChannel", fmt.Sprintf("%s:bk_app_to_space:channel", SpaceRedisKey))

	BkdataDefaultBizId = GetValue("taskConfig.metadata.bkdata.defaultBizId", 0)
	BkdataProjectId = GetValue("taskConfig.metadata.bkdata.projectId", 1)
	BkdataRealtimeNodeWaitTime = GetValue("taskConfig.metadata.bkdata.realtimeNodeWaitTime", 10)
	BkdataDataExpiresDays = GetValue("taskConfig.metadata.bkdata.dataExpiresDays", 30)
	BkdataKafkaBrokerUrl = GetValue("taskConfig.metadata.bkdata.kafkaBrokerUrl", "")
	BkdataRtIdPrefix = GetValue("taskConfig.metadata.bkdata.rtIdPrefix", GlobalBkappDeployPlatform)
	BkdataBkBizId = GetValue("taskConfig.metadata.bkdata.bkBizId", 2)
	BkdataRawTableSuffix = GetValue("taskConfig.metadata.bkdata.rawTableSuffix", "raw")
	BkdataCMDBFullTableSuffix = GetValue("taskConfig.metadata.bkdata.CMDBFullTableSuffix", "full")
	BkdataCMDBSplitTableSuffix = GetValue("taskConfig.metadata.bkdata.CMDBFSplitTableSuffix", "cmdb")
	BkdataDruidStorageClusterName = GetValue("taskConfig.metadata.bkdata.druidStorageClusterName", "")
	BkdataMysqlStorageClusterName = GetValue("taskConfig.metadata.bkdata.mysqlStorageClusterName", "jungle_alert")
	BkdataMysqlStorageClusterType = GetValue("taskConfig.metadata.bkdata.mysqlStorageClusterType", "mysql_storage")
	BkdataFlowClusterGroup = GetValue("taskConfig.metadata.bkdata.flowClusterGroup", "default_inland")
	BkdataProjectMaintainer = GetValue("taskConfig.metadata.bkdata.projectMaintainer", "admin")
	BkdataIsAllowAllCmdbLevel = GetValue("taskConfig.metadata.bkdata.isAllowAllCmdbLevel", false)
	SkipInfluxdbTableIds = GetValue("taskConfig.metadata.global.skipInfluxdbTableIds", []string{})
	CanDeleteConsulPath = GetValue("taskConfig.metadata.global.CanDeleteConsulPath", false)

	SloPushGatewayToken = GetValue("taskConfig.metadata.slo.sloPushGatewayToken", "")
	SloPushGatewayEndpoint = GetValue("taskConfig.metadata.slo.sloPushGatewayEndpoint", "")

	InitialMaxWaitTime = GetValue("taskConfig.metadata.initialMaxWaitTime", "10m")
}
