// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// IgnoreConsulSyncDataIdList 忽略同步consul的data_id
var IgnoreConsulSyncDataIdList = []uint{1002, 1003, 1004, 1005, 1006}

// NsTimestampEtlConfigList 需要指定是纳秒级别的清洗配置内容
var NsTimestampEtlConfigList = []string{"bk_standard_v2_event", "bk_standard_v2_time_series"}

type DataSourceSvc struct {
	*resulttable.DataSource
}

func NewDataSourceSvc(obj *resulttable.DataSource) DataSourceSvc {
	return DataSourceSvc{
		DataSource: obj,
	}
}

// ConsulPath 获取datasource的consul根路径
func (DataSourceSvc) ConsulPath() string {
	return fmt.Sprintf(models.DataSourceConsulPathTemplate, cfg.StorageConsulPathPrefix)
}

// ConsulConfigPath 获取具体data_id的consul配置路径
func (d DataSourceSvc) ConsulConfigPath() string {
	return fmt.Sprintf("%s/%s/data_id/%v", d.ConsulPath(), d.TransferClusterId, d.BkDataId)
}

// MqConfigObj 返回data_id的kafka配置对象
func (d DataSourceSvc) MqConfigObj() (*storage.KafkaTopicInfo, error) {
	var kafkaTopicInfo storage.KafkaTopicInfo
	if err := storage.NewKafkaTopicInfoQuerySet(mysql.GetDBSession().DB).BkDataIdEq(d.BkDataId).One(&kafkaTopicInfo); err != nil {
		return nil, errors.Wrapf(err, "query KafkaTopicInfo failed")
	}
	return &kafkaTopicInfo, nil
}

// MqCluster 返回data_id的kafka集群对象
func (d DataSourceSvc) MqCluster() (*storage.ClusterInfo, error) {
	var clusterInfo storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(d.MqClusterId).One(&clusterInfo); err != nil {
		return nil, errors.Wrap(err, "query mq cluster failed")
	}
	return &clusterInfo, nil
}

// ToJson 获取当前data_id的配置
func (d DataSourceSvc) ToJson(isConsulConfig, withRtInfo bool) (map[string]interface{}, error) {
	// 集群配置信息
	kafkaTopicInfo, err := d.MqConfigObj()
	if err != nil {
		return nil, err
	}
	mqConfig := map[string]interface{}{
		"storage_config": map[string]interface{}{
			"topic":     kafkaTopicInfo.Topic,
			"partition": kafkaTopicInfo.Partition,
		},
		"batch_size":     kafkaTopicInfo.BatchSize,
		"flush_interval": kafkaTopicInfo.FlushInterval,
		"consume_rate":   kafkaTopicInfo.ConsumeRate,
	}
	// 添加集群信息
	clusterInfo, err := d.MqCluster()
	if err != nil {
		return nil, err
	}
	consulConfig, err := NewClusterInfoSvc(clusterInfo).ConsulConfig()
	if err != nil {
		return nil, err
	}
	mqConfig["cluster_config"] = consulConfig.ClusterConfig
	mqConfig["cluster_type"] = consulConfig.ClusterType
	mqConfig["auth_info"] = consulConfig.AuthInfo

	// 获取datasource的配置项
	optionData, err := NewDataSourceOptionSvc(nil).GetOptions(d.BkDataId)
	if err != nil {
		return nil, err
	}
	resultConfig := map[string]interface{}{
		"bk_data_id":          d.BkDataId,
		"data_id":             d.BkDataId,
		"mq_config":           mqConfig,
		"etl_config":          d.EtlConfig,
		"option":              optionData,
		"type_label":          d.TypeLabel,
		"source_label":        d.SourceLabel,
		"token":               d.Token,
		"transfer_cluster_id": d.TransferClusterId,
		"data_name":           d.DataName,
		"is_platform_data_id": d.IsPlatformDataId,
		"space_type_id":       d.SpaceTypeId,
		"space_uid":           d.SpaceUid,
	}
	db := mysql.GetDBSession().DB
	// 获取ResultTable的配置
	if withRtInfo {
		resultTableInfoList := make([]interface{}, 0)
		resultConfig["result_table_list"] = resultTableInfoList
		var resultTableIdList []string
		var dataSourceRtList []resulttable.DataSourceResultTable
		if err := resulttable.NewDataSourceResultTableQuerySet(db).
			BkDataIdEq(d.BkDataId).All(&dataSourceRtList); err != nil {
			return nil, err
		}
		if len(dataSourceRtList) == 0 {
			return resultConfig, nil
		}
		for _, t := range dataSourceRtList {
			resultTableIdList = append(resultTableIdList, t.TableId)
		}
		var resultTableList []resulttable.ResultTable
		if err := resulttable.NewResultTableQuerySet(db).TableIdIn(resultTableIdList...).
			IsDeletedEq(false).IsEnableEq(true).All(&resultTableList); err != nil {
			return nil, err
		}
		if len(resultTableList) == 0 {
			return resultConfig, nil
		}

		var filterResultTableIdList []string
		for _, table := range resultTableList {
			filterResultTableIdList = append(filterResultTableIdList, table.TableId)
		}
		// 批量获取结果表级别选项
		rtOptions, err := NewResultTableOptionSvc(nil).BathResultTableOption(filterResultTableIdList)
		if err != nil {
			return nil, err
		}
		// 获取字段信息
		tableFields, err := NewResultTableFieldSvc(nil).BatchGetFields(filterResultTableIdList, isConsulConfig)
		if err != nil {
			return nil, err
		}
		// 判断需要未删除，而且在启用状态的结果表
		for _, rt := range resultTableList {
			storageList, err := NewResultTableSvc(&rt).RealStorageList()
			if err != nil {
				return nil, err
			}
			var shipperList []*StorageConsulConfig
			for _, s := range storageList {
				consulConfig, err := s.ConsulConfig()
				if err != nil {
					return nil, err
				}
				// 当集群类型在白名单或者rt在白名单中时，填过记录
				if slicex.IsExistItem(storage.IgnoredStorageClusterTypes, consulConfig.ClusterType) || (consulConfig.ClusterType == models.StorageTypeInfluxdb && slicex.IsExistItem(config.SkipInfluxdbTableIds, rt.TableId)) {
					continue
				}
				shipperList = append(shipperList, consulConfig)
			}
			var fieldList = make([]interface{}, 0)
			// 如果是自定义上报的情况，不需要将字段信息写入到consul上
			if !d.isCustomTimeSeriesReport() {
				if fields, ok := tableFields[rt.TableId]; ok {
					fieldList = fields
				}
			}
			var options = make(map[string]interface{})
			if ops, ok := rtOptions[rt.TableId]; ok {
				options = ops
			}
			if len(shipperList) == 0 {
				shipperList = make([]*StorageConsulConfig, 0)
			}
			resultTableInfoList = append(resultTableInfoList, map[string]interface{}{
				"bk_biz_id":    rt.BkBizId,
				"result_table": rt.TableId,
				"shipper_list": shipperList,
				"field_list":   fieldList,
				"schema_type":  rt.SchemaType,
				"option":       options,
			})
		}
		resultConfig["result_table_list"] = resultTableInfoList
	}
	return resultConfig, nil
}

// 是否自定义上报的数据源
func (d DataSourceSvc) isCustomTimeSeriesReport() bool {
	return slicex.IsExistItem([]string{models.ETLConfigTypeBkStandardV2TimeSeries}, d.EtlConfig)
}

// CanRefreshConfig 判断是否可以刷新GSE和consul配置
func (d DataSourceSvc) CanRefreshConfig() bool {
	if d.IsEnable && d.CreatedFrom == common.DataIdFromBkGse {
		return true
	}
	return false
}

// RefreshGseConfig 刷新GSE配置，同步路由配置到gse
func (d DataSourceSvc) RefreshGseConfig() error {
	if !d.CanRefreshConfig() {
		logger.Infof("data_id [%d] can not refresh gse config, skip", d.BkDataId)
		return nil
	}
	mqCluster, err := d.MqCluster()
	if err != nil {
		return err
	}
	if mqCluster.GseStreamToId == -1 {
		return errors.Errorf("dataid [%v] mq is not inited", d.BkDataId)
	}
	params := bkgse.QueryRouteParams{}
	params.Condition.ChannelId = d.BkDataId
	params.Condition.PlatName = "bkmonitor"
	params.Operation.OperatorName = "admin"
	data, err := apiservice.Gse.QueryRoute(d.BkTenantId, params)
	if err != nil {
		return errors.Wrapf(err, "data_id [%v] query gse route failed", d.BkDataId)
	}
	if data == nil {
		logger.Errorf("data_id [%d] can not find route info from gse, please check your datasource config", d.BkDataId)
		if err := d.AddBuiltInChannelIdToGse(); err != nil {
			return errors.Wrapf(err, "add builtin channel id [%d] to gse failed", d.BkDataId)
		}
		return nil
	}

	dataJSON, err := jsonx.MarshalString(data)
	if err != nil {
		return err
	}
	var dataList []bkgse.QueryRouteDataResp
	err = jsonx.UnmarshalString(dataJSON, &dataList)
	if err != nil {
		return err
	}

	var oldRoute *bkgse.GSERoute
	config, err := d.GseRouteConfig()
	if err != nil {
		return err
	}

	for _, routeInfo := range dataList {
		if oldRoute != nil {
			break
		}
		if len(routeInfo.Route) == 0 {
			continue
		}
		for _, route := range routeInfo.Route {

			if route.Name != config.Name {
				continue
			}
			oldRoute = &bkgse.GSERoute{
				Name:          route.Name,
				StreamTo:      route.StreamTo,
				FilterNameAnd: make([]interface{}, 0),
				FilterNameOr:  make([]interface{}, 0),
			}
			break
		}
	}
	var equal bool
	if oldRoute == nil {
		equal = false
	} else {
		equal, err = jsonx.CompareObjects(*oldRoute, *config)
		if err != nil {
			return errors.Wrapf(err, "CompareObjects [%#v] and [%#v] failed", *oldRoute, *config)
		}
	}
	if equal {
		logger.Infof("data_id [%d] gse route config has no difference from gse, skip", d.BkDataId)
		return nil
	}
	logger.Infof("data_id [%d] gse route config [%#v] is different from gse [%#v], will refresh it", d.BkDataId, config, oldRoute)
	metrics.GSEUpdateCount(d.BkDataId)

	updateParam := bkgse.UpdateRouteParams{
		Condition: bkgse.RouteMetadata{
			ChannelId: d.BkDataId,
			PlatName:  common.AccessGseApiPlatName,
		},
		Specification: map[string]interface{}{"route": []interface{}{config}},
		Operation:     bkgse.Operation{OperatorName: "admin"},
	}

	if _, err = apiservice.Gse.UpdateRoute(d.BkTenantId, updateParam); err != nil {
		return errors.Wrapf(err, "UpdateRoute for data_id [%d] failed", d.BkDataId)
	}
	logger.Infof("data_id [%d] success to push route info to gse", d.BkDataId)

	return nil
}

// AddBuiltInChannelIdToGse add register built_in channel_id to gse
func (d DataSourceSvc) AddBuiltInChannelIdToGse() error {
	if !models.IsBuildInDataId(d.BkDataId) {
		return nil
	}
	logger.Warnf("try to add register built_in channel_id [%v] to gse", d.BkDataId)
	route, err := d.GseRouteConfig()
	if err != nil {
		logger.Errorf("make gse route config error, %v", err)
		return err
	}
	metrics.GSEUpdateCount(d.BkDataId)
	params := bkgse.AddRouteParams{
		Metadata: bkgse.RouteMetadata{
			ChannelId: d.BkDataId,
			PlatName:  "bkmonitor",
		},
		Route:     []interface{}{route},
		Operation: bkgse.Operation{OperatorName: "admin"},
	}

	data, err := apiservice.Gse.AddRoute(d.BkTenantId, params)
	if err != nil {
		return err
	}
	logger.Infof("data_id [%v] success to push route info to gse, [%v]", d.BkDataId, data)
	return nil
}

func (d DataSourceSvc) GseRouteConfig() (*bkgse.GSERoute, error) {
	mqConfig, err := d.MqConfigObj()
	if err != nil {
		return nil, err
	}
	mqCluster, err := d.MqCluster()
	if err != nil {
		return nil, err
	}
	routeName := fmt.Sprintf("stream_to_bkmonitor_kafka_%s", mqConfig.Topic)

	return &bkgse.GSERoute{
		Name: routeName,
		StreamTo: map[string]interface{}{
			"stream_to_id": mqCluster.GseStreamToId,
			"kafka": map[string]interface{}{
				"topic_name": mqConfig.Topic,
			},
		},
		FilterNameAnd: make([]interface{}, 0),
		FilterNameOr:  make([]interface{}, 0),
	}, nil

}

// RefreshConsulConfig 更新consul配置，告知ETL等其他依赖模块配置有所更新
func (d DataSourceSvc) RefreshConsulConfig(ctx context.Context, modifyIndex uint64, oldValueBytes []byte) error {
	logger.Infof("RefreshConsulConfig:data_id [%d] started to refresh consul config", d.BkDataId)
	// 如果数据源没有启用，则不用刷新 consul 配置
	if !d.CanRefreshConfig() {
		logger.Infof("RefreshConsulConfig:data_id [%d] can not refresh consul config, skip", d.BkDataId)
		return nil
	}

	// transfer不处理data_id 1002--1006的数据，忽略推送到consul
	for _, dataId := range IgnoreConsulSyncDataIdList {
		if d.BkDataId == dataId {
			return nil
		}
	}

	// 获取Consul句柄
	consulClient, err := consul.GetInstance()
	if err != nil {
		logger.Errorf("RefreshConsulConfig:data_id [%d] get consul client failed, %v", d.BkDataId, err)
		return err
	}

	val, err := d.ToJson(true, true)
	if err != nil {
		return errors.Wrap(err, "RefreshConsulConfig:datasource to_json failed")
	}
	valStr, err := jsonx.MarshalString(val)
	if err != nil {
		return err
	}
	err = hashconsul.PutCas(consulClient, d.ConsulConfigPath(), valStr, modifyIndex, oldValueBytes)
	if err != nil {
		logger.Errorf("RefreshConsulConfig:data_id [%v] put [%s] to [%s] failed, %v", d.BkDataId, valStr, d.ConsulConfigPath(), err)
		return err
	}
	logger.Infof("RefreshConsulConfig:data_id [%v] has update config [%s] to [%v] success", d.BkDataId, valStr, d.ConsulConfigPath())
	return nil
}

func (d DataSourceSvc) RefreshOuterConfig(ctx context.Context, modifyIndex uint64, oldValueBytes []byte) error {
	if !d.IsEnable {
		logger.Infof("data_id [%d] is not enable, nothing will refresh to outer systems.", d.BkDataId)
		return nil
	}

	// NOTE: 当刷新 gse 异常时，仅记录日志
	err := d.RefreshGseConfig()
	if err != nil {
		logger.Errorf("data_id [%d] refresh gse config failed, %v", d.BkDataId, err)
	}

	err = d.RefreshConsulConfig(ctx, modifyIndex, oldValueBytes)
	if err != nil {
		logger.Errorf("data_id [%d] refresh consul config failed, %v", d.BkDataId, err)
	}

	return err
}

// CleanConsulPath clean datasource consul path, when not enable or from bkdata
func CleanConsulPath(consulClient *consul.Instance, dataIdPaths *[]string, consulPaths *[]string) error {
	// 获取需要删除的路径
	_, needDeletePaths := lo.Difference(*dataIdPaths, *consulPaths)
	// 直接删除即可
	if len(needDeletePaths) == 0 {
		logger.Info("no need to delete consul path")
		return nil
	}
	if cfg.CanDeleteConsulPath {
		for _, path := range needDeletePaths {
			if err := consulClient.Delete(path); err != nil {
				logger.Error("delete dataid consul path failed, path: %s, error: %s", path, err)
			}
		}
	} else {
		logger.Infof("different path for datasource and consul_key, path: %v", needDeletePaths)
	}

	return nil
}
