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
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
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

func (d DataSourceSvc) CreateDataSource(dataName, etcConfig, operator, sourceLabel string, mqCluster uint, typeLabel, transferClusterId, sourceSystem string) (*resulttable.DataSource, error) {
	// 判断两个使用到的标签是否存在
	count, err := resulttable.NewLabelQuerySet(mysql.GetDBSession().DB).LabelIdEq(sourceLabel).LabelTypeEq(models.LabelTypeSource).Count()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New(fmt.Sprintf("user [%s] try to create datasource but use source_type [%s], which is not exists", operator, sourceLabel))
	}
	count, err = resulttable.NewLabelQuerySet(mysql.GetDBSession().DB).LabelIdEq(typeLabel).LabelTypeEq(models.LabelTypeType).Count()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New(fmt.Sprintf("user [%s] try to create datasource but use type_label [%s], which is not exists", operator, typeLabel))
	}
	// 判断参数是否符合预期
	// 数据源名称是否重复
	count, err = resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).DataNameEq(dataName).Count()
	if err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, errors.New(fmt.Sprintf("data_name [%s] is already exists, maybe something go wrong?", dataName))
	}
	// 如果集群信息无提供，则使用默认的MQ集群信息
	var mqClusterObj storage.ClusterInfo
	if mqCluster == 0 {
		if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterTypeEq(models.StorageTypeKafka).
			IsDefaultClusterEq(true).One(&mqClusterObj); err != nil {
			return nil, err
		}
	} else {
		if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(mqCluster).One(&mqClusterObj); err != nil {
			return nil, err
		}
	}
	bkDataId, err := d.ApplyForDataIdFromGse(operator)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("apply for data_id error, %s", err))
	}
	if transferClusterId == "" {
		transferClusterId = "default"
	}
	// 此处启动DB事务，创建默认的信息
	ds := resulttable.DataSource{
		BkDataId:          bkDataId,
		Token:             d.makeToken(),
		DataName:          dataName,
		MqClusterId:       mqClusterObj.ClusterID,
		EtlConfig:         etcConfig,
		Creator:           operator,
		CreateTime:        time.Now(),
		LastModifyUser:    operator,
		LastModifyTime:    time.Now(),
		TypeLabel:         typeLabel,
		SourceLabel:       sourceLabel,
		SourceSystem:      sourceSystem,
		IsEnable:          true,
		TransferClusterId: transferClusterId,
		IsPlatformDataId:  false,
		IsCustomSource:    true,
		SpaceTypeId:       "all",
		SpaceUid:          "",
	}
	if err := ds.Create(mysql.GetDBSession().DB); err != nil {
		return nil, err
	}
	logger.Infof("data_id [%v] data_name [%s] by operator [%s] now is pre-create.", ds.BkDataId, ds.DataName, ds.Creator)
	// 获取这个数据源对应的配置记录model，并创建一个新的配置记录
	mqConfig, err := NewKafkaTopicInfoSvc(nil).CreateInfo(bkDataId, "", 0, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	ds.MqConfigId = mqConfig.Id
	err = ds.Update(mysql.GetDBSession().DB, resulttable.DataSourceDBSchema.MqConfigId)
	if err != nil {
		return nil, err
	}
	logger.Infof("data_id [%v] now is relate to its mq config id [%v]", ds.BkDataId, ds.MqConfigId)

	// 判断是否NS支持的etl配置，如果是，则需要追加option内容
	for _, etl := range NsTimestampEtlConfigList {
		if etcConfig != etl {
			continue
		}
		err := NewDataSourceOptionSvc(nil).CreateOption(ds.BkDataId, models.OptionTimestampUnit, "ms", operator)
		if err != nil {
			return nil, err
		}
		logger.Infof("bk_data_id [%v] etl_config [%s] so is has now has option [%s] with value->[ms]", ds.BkDataId, etcConfig, models.OptionTimestampUnit)
	}

	// 触发consul刷新
	err = NewDataSourceSvc(&ds).RefreshOuterConfig(context.Background())
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

// makeToken
func (d DataSourceSvc) makeToken() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// ConsulPath 获取datasource的consul根路径
func (DataSourceSvc) ConsulPath() string {
	return fmt.Sprintf(models.DataSourceConsulPathTemplate, viper.GetString(consul.ConsulBasePath))
}

// ConsulConfigPath 获取具体data_id的consul配置路径
func (d DataSourceSvc) ConsulConfigPath() string {
	return fmt.Sprintf("%s/%s/data_id/%v", d.ConsulPath(), d.TransferClusterId, d.BkDataId)
}

// MqConfigObj 返回data_id的kafka配置对象
func (d DataSourceSvc) MqConfigObj() (*storage.KafkaTopicInfo, error) {
	var kafkaTopicInfo storage.KafkaTopicInfo
	if err := storage.NewKafkaTopicInfoQuerySet(mysql.GetDBSession().DB).BkDataIdEq(d.BkDataId).One(&kafkaTopicInfo); err != nil {
		return nil, err
	}
	return &kafkaTopicInfo, nil
}

// MqCluster 返回data_id的kafka集群对象
func (d DataSourceSvc) MqCluster() (*storage.ClusterInfo, error) {
	var clusterInfo storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(d.MqClusterId).One(&clusterInfo); err != nil {
		return nil, err
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
	consulConfig := NewClusterInfoSvc(clusterInfo).ConsulConfig()
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

	// 获取ResultTable的配置
	if withRtInfo {
		var resultTableInfoList []interface{}
		var resultTableIdList []string
		var dataSourceRtList []resulttable.DataSourceResultTable
		if err := resulttable.NewDataSourceResultTableQuerySet(mysql.GetDBSession().DB).
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
		if err := resulttable.NewResultTableQuerySet(mysql.GetDBSession().DB).TableIdIn(resultTableIdList...).
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
				skip := false
				for _, clusterType := range storage.IgnoredStorageClusterTypes {
					if consulConfig.ClusterType == clusterType {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				shipperList = append(shipperList, consulConfig)
			}
			var fieldList = make([]interface{}, 0)
			if fields, ok := tableFields[rt.TableId]; ok {
				fieldList = fields
			}
			var options = make(map[string]interface{})
			if ops, ok := rtOptions[rt.TableId]; ok {
				options = ops
			}
			resultTableInfoList = append(resultTableInfoList, map[string]interface{}{
				"bk_biz_id":    rt.BkBizId,
				"result_table": rt.TableId,
				"shipper_list": shipperList,
				"field_list":   fieldList, // 如果是自定义上报的情况，不需要将字段信息写入到consul上
				"schema_type":  rt.SchemaType,
				"option":       options,
			})
		}
		resultConfig["result_table_list"] = resultTableInfoList
	}
	return resultConfig, nil
}

// RefreshGseConfig 刷新GSE配置，同步路由配置到gse
func (d DataSourceSvc) RefreshGseConfig() error {
	mqCluster, err := d.MqCluster()
	if err != nil {
		return err
	}
	if mqCluster.GseStreamToId == -1 {
		return errors.New(fmt.Sprintf("dataid [%v] mq is not inited", d.BkDataId))
	}
	gseApi, err := api.GetGseApi()
	if err != nil {
		return err
	}
	var resp define.APICommonResp
	_, err = gseApi.QueryRoute().SetBody(map[string]interface{}{
		"condition": map[string]interface{}{
			"plat_name": "bkmonitor", "channel_id": d.BkDataId,
		},
		"operation": map[string]interface{}{
			"operator_name": "admin",
		},
	}).SetResult(&resp).Request()
	if err != nil {
		logger.Errorf("data_id [%v] query gse route failed, error: %v", err)
		return err
	}
	if resp.Data == nil {
		logger.Errorf("data_id [%v] can not find route info from gse, %s, please check your datasource config", d.BkDataId, resp.Message)
		err := d.AddBuiltInChannelIdToGse()
		if err != nil {
			logger.Errorf("add builtin channel id [%v] to gse failed, %v", d.BkDataId, err)
			return err
		}
		return nil
	}

	var oldRoute *bkgse.GSERoute
	config, err := d.GseRouteConfig()
	if err != nil {
		return err
	}
	configJSON, err := jsonx.MarshalString(config)
	if err != nil {
		return err
	}
	err = jsonx.UnmarshalString(configJSON, &config)
	if err != nil {
		return err
	}

	dataJSON, err := jsonx.MarshalString(resp.Data)
	if err != nil {
		return err
	}
	var dataList []bkgse.QueryRouteDataResp
	err = jsonx.UnmarshalString(dataJSON, &dataList)
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
		equal = reflect.DeepEqual(*oldRoute, *config)
	}
	if equal {
		logger.Infof("data_id [%v] gse route config has no difference from gse, skip", d.BkDataId)
		return nil
	}
	logger.Infof("data_id [%v] gse route config is different from gse, will refresh it", d.BkDataId)
	var updateResult define.APICommonResp
	_, err = gseApi.UpdateRoute().SetBody(map[string]interface{}{
		"condition": map[string]interface{}{"channel_id": d.BkDataId, "plat_name": "bkmonitor"},
		"operation": map[string]interface{}{"operator_name": "admin"},
		"specification": map[string]interface{}{
			"route": []interface{}{config},
		},
	}).SetResult(&updateResult).Request()
	if err != nil {
		return err
	}
	if updateResult.Code != 0 {
		logger.Errorf("try to update gse route for channel id [%v] failed, %s", d.BkDataId, updateResult.Message)
		return errors.New(updateResult.Message)
	}
	logger.Infof("data_id [%v] success to push route info to gse", d.BkDataId)
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
	params := map[string]interface{}{
		"metadata":  map[string]interface{}{"channel_id": d.BkDataId, "plat_name": "bkmonitor"},
		"operation": map[string]interface{}{"operator_name": "admin"},
		"route":     []interface{}{route},
	}
	gseApi, err := api.GetGseApi()
	if err != nil {
		return err
	}
	var resp define.APICommonResp
	_, err = gseApi.AddRoute().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		logger.Warnf("try to add builtin channel id [%v] to gse, %s", d.BkDataId, resp.Message)
	} else {
		logger.Infof("data_id [%v] success to push route info to gse", d.BkDataId)
	}
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
				"data_set":   mqConfig.Topic[:len(mqConfig.Topic)-1],
				"partition":  mqConfig.Partition,
				"biz_id":     0,
			},
		},
		FilterNameAnd: make([]interface{}, 0),
		FilterNameOr:  make([]interface{}, 0),
	}, nil

}

// RefreshConsulConfig 更新consul配置，告知ETL等其他依赖模块配置有所更新
func (d DataSourceSvc) RefreshConsulConfig(ctx context.Context) error {
	// 如果数据源没有启用，则不用刷新 consul 配置
	if !d.IsEnable {
		return nil
	}

	// transfer不处理data_id 1002--1006的数据，忽略推送到consul
	for _, dataId := range IgnoreConsulSyncDataIdList {
		if d.BkDataId == dataId {
			return nil
		}
	}
	consulClient, err := consul.GetInstance(ctx)
	if err != nil {
		return err
	}
	val, err := d.ToJson(true, true)
	if err != nil {
		return err
	}
	valStr, err := jsonx.MarshalString(val)
	if err != nil {
		return err
	}
	err = consulClient.Put(d.ConsulConfigPath(), valStr, 0)
	if err != nil {
		logger.Errorf("data_id [%v] put [%s] failed, %v", d.BkDataId, d.ConsulConfigPath(), err)
		return err
	}
	logger.Infof("data_id [%v] has update config to [%v] success", d.BkDataId, d.ConsulConfigPath())
	return nil
}

func (d DataSourceSvc) RefreshOuterConfig(ctx context.Context) error {
	if !d.IsEnable {
		logger.Infof("data_id [%s] is not enable, nothing will refresh to outer systems.", d.BkDataId)
		return nil
	}
	err := d.RefreshGseConfig()
	if err != nil {
		logger.Errorf("data_id [%v] refresh gse config failed, %v", d.BkDataId, err)
		return err
	}

	err = d.RefreshConsulConfig(ctx)
	if err != nil {
		logger.Errorf("data_id [%v] refresh consul config failed, %v", d.BkDataId, err)
		return err
	}

	return nil
}

// ApplyForDataIdFromGse 从gse请求生成data id
func (d DataSourceSvc) ApplyForDataIdFromGse(operator string) (uint, error) {
	gseApi, err := api.GetGseApi()
	if err != nil {
		return 0, nil
	}
	var resp define.APICommonResp
	_, err = gseApi.AddRoute().SetBody(map[string]interface{}{
		"metadata": map[string]interface{}{
			"plat_name": "bkmonitor",
		},
		"operation": map[string]interface{}{
			"operator_name": operator,
		},
	}).SetResult(&resp).Request()
	if err != nil {
		return 0, err
	}
	if resp.Code != 0 {
		return 0, errors.New(resp.Message)
	}
	data := resp.Data.(map[string]interface{})
	channelIdInterface := data["channel_id"]
	channelId := channelIdInterface.(float64)
	return uint(channelId), nil
}
