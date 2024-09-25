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
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/dataflow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BkDataStorageSvc bkdata storage service
type BkDataStorageSvc struct {
	*storage.BkDataStorage
}

func NewBkDataStorageSvc(obj *storage.BkDataStorage) BkDataStorageSvc {
	return BkDataStorageSvc{
		BkDataStorage: obj,
	}
}

func (s BkDataStorageSvc) CreateDatabusClean(rt *resulttable.ResultTable) error {
	if s.BkDataStorage == nil {
		return errors.New("BkDataStorage obj can not be nil")
	}
	db := mysql.GetDBSession().DB
	var kafkaStorage storage.KafkaStorage
	if err := storage.NewKafkaStorageQuerySet(db).TableIDEq(rt.TableId).One(&kafkaStorage); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("result table [%s] data not write into mq", rt.TableId)
		}
		return err
	}
	// 增加接入部署计划
	svc := NewKafkaStorageSvc(&kafkaStorage)
	storageCluster, err := svc.StorageCluster()
	if err != nil {
		return err
	}
	consulConfig, err := NewClusterInfoSvc(storageCluster).ConsulConfig()
	if err != nil {
		return err
	}
	domain := consulConfig.ClusterConfig.DomainName
	port := consulConfig.ClusterConfig.Port
	// kafka broker_url 以实际配置为准，如果没有配置，再使用默认的 broker url
	brokerUrl := config.BkdataKafkaBrokerUrl
	if domain != "" && port != 0 {
		brokerUrl = fmt.Sprintf("%s:%v", domain, port)
	}
	isSasl := consulConfig.ClusterConfig.IsSslVerify
	user := consulConfig.AuthInfo.Username
	passwd := consulConfig.AuthInfo.Password
	// 采用结果表区分消费组
	KafkaConsumerGroupName := GenBkdataRtIdWithoutBizId(rt.TableId)
	// 计算平台要求，raw_data_name不能超过50个字符
	rtId := strings.ReplaceAll(rt.TableId, ".", "__")
	rtId = stringx.LimitLengthSuffix(rtId, 50)
	rawDataName := fmt.Sprintf("%s_%s", config.BkdataRtIdPrefix, rtId)
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return err
	}
	var resp bkdata.AccessDeployPlanResp
	params := bkdata.AccessDeployPlanParams(rawDataName, rt.TableNameZh, brokerUrl, KafkaConsumerGroupName, svc.Topic, user, passwd, svc.Partition, isSasl)
	if config.BypassSuffixPath != "" {
		paramStr, _ := jsonx.MarshalString(params)
		logger.Infof("[db_diff] AccessDeployPlan with params [%s]", paramStr)
		return errors.New("[db_diff] AccessDeployPlan is not really executed because of BypassSuffixPath")
	}
	if _, err := bkdataApi.AccessDeployPlan().SetBody(params).SetResult(&resp).Request(); err != nil {
		return errors.Wrapf(err, "access to bkdata failed, params [%#v]", params)
	}
	s.RawDataID = resp.Data.RawDataId
	if s.RawDataID == 0 {
		return errors.Errorf("access to bkdata failed, %s", resp.Message)
	}
	logger.Infof("access to bkdata, result [%#v]", resp)

	if err := s.Update(db, storage.BkDataStorageDBSchema.RawDataID); err != nil {
		return err
	}
	return nil
}

func (s BkDataStorageSvc) CreateTable(tableId string, isSyncDb bool) error {
	db := mysql.GetDBSession().DB
	var bkDataStorage storage.BkDataStorage
	if err := storage.NewBkDataStorageQuerySet(db).TableIDEq(tableId).One(&bkDataStorage); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			bkDataStorage.TableID = tableId
			if config.BypassSuffixPath != "" {
				logger.Infof("[db_diff] create BkDataStorage with table_id [%s]", tableId)
				return nil
			} else {
				if err := bkDataStorage.Create(db); err != nil {
					return errors.Wrapf(err, "create BkDataStorage with table_id [%s] failed", tableId)
				}
			}
		} else {
			return err
		}
	}
	s.BkDataStorage = &bkDataStorage
	if isSyncDb {
		if err := s.CheckAndAccessBkdata(); err != nil {
			return errors.Wrapf(err, "CheckAndAccessBkdata for table_id [%s] failed", tableId)
		}
	}
	return nil
}

func (s BkDataStorageSvc) CheckAndAccessBkdata() error {
	/*
		   	 1. 先看是否已经接入，没有接入则继续
			  第一步：
				  - 按kafka逻辑，接入到100147业务下
				  - 走access/deploy_plan接口配置kafka接入
				  - 走databus/cleans接口配置清洗规则
				  - 走databus/tasks接口启动清洗

			  第二步：
				  - 走auth/tickets接口将100147业务的表授权给某个项目
				  - 走dataflow/flow/flows接口创建出一个画布
				  - 走dataflow/flow/flows/{flow_id}/nodes/接口创建画布上的实时数据源、统计节点、存储节点
				  - 走dataflow/flow/flows/{flow_id}/start/接口启动任务

		  	2. 已经接入，则走更新逻辑
			  - 判断字段是否有变更，无变更则退出，有变更则继续
			  - 走access/deploy_plan/{raw_data_id}/接口更新接入计划
			  - 走databus/cleans/{processing_id}/接口更新清洗配置
			  - 走dataflow/flow/flows/{fid}/nodes/{nid}/接口更新计算节点 & 存储节点
			  - 走dataflow/flow/flows/{flow_id}/restart/接口重启任务
	*/
	db := mysql.GetDBSession().DB
	var rt resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).TableIdEq(s.TableID).One(&rt); err != nil {
		return errors.Wrapf(err, "query ResultTable with table_id [%s] failed", s.TableID)
	}

	if s.RawDataID == -1 {
		if err := s.CreateDatabusClean(&rt); err != nil {
			return errors.Wrapf(err, "CreateDatabusClean for table_id [%s] failed", s.TableID)
		}
	}

	// 增加或修改清洗配置任务
	etlConfig, fields, err := s.generateBkDataEtlConfig()
	if err != nil {
		return errors.Wrapf(err, "generateBkDataEtlConfig for table_id [%s] failed", s.TableID)
	}
	etlConfigJson, err := jsonx.MarshalString(etlConfig)
	if err != nil {
		return errors.Wrapf(err, "marshal etl config  [%v] failed", etlConfigJson)
	}

	bkDataRtIdWithoutBizId := GenBkdataRtIdWithoutBizId(s.TableID)
	resultTableId := fmt.Sprintf("%v_%s", config.BkdataBkBizId, bkDataRtIdWithoutBizId)
	params := bkdata.DatabusCleansParams{
		RawDataId:            s.RawDataID,
		JsonConfig:           etlConfigJson,
		PEConfig:             "",
		BkBizId:              config.BkdataBkBizId,
		Description:          fmt.Sprintf("清洗配置 (%s)", rt.TableNameZh),
		CleanConfigName:      fmt.Sprintf("清洗配置 (%s)", rt.TableNameZh),
		ResultTableName:      bkDataRtIdWithoutBizId,
		ResultTableNameAlias: rt.TableNameZh,
		Fields:               fields,
	}
	if len(s.EtlJSONConfig) == 0 {
		// 执行创建操作
		if config.BypassSuffixPath != "" {
			paramStr, _ := jsonx.MarshalString(params)
			logger.Infof("[db_diff] create DatabusCleans with params [%s]", paramStr)
			return nil
		}
		result, err := apiservice.Bkdata.DatabusCleans(params)
		if err != nil {
			return errors.Wrap(err, "add databus clean to bkdata failed")
		}
		logger.Infof("add databus clean to bkdata, result [%v]", result)
	} else {
		if equal, _ := jsonx.CompareJson(s.EtlJSONConfig, etlConfigJson); !equal {
			// 执行更新操作
			if config.BypassSuffixPath != "" {
				logger.Infof("[db_diff] update DatabusCleans beacuse etl_config different [%s] and [%s]", s.EtlJSONConfig, etlConfigJson)
			} else {
				result, err := apiservice.Bkdata.StopDatabusCleans(resultTableId, []string{"kafka"})
				if err != nil {
					return errors.Wrap(err, "stop databus clean failed")
				}
				logger.Infof("stop databus clean, result [%v]", result)
				result, err = apiservice.Bkdata.UpdateDatabusCleans(resultTableId, params)
				if err != nil {
					return errors.Wrap(err, "update databus clean failed")
				}
				resultStr, _ := jsonx.MarshalString(result)
				logger.Infof("update databus clean, result [%s]", resultStr)
			}
		}
	}
	// 获取对应的etl任务状态，如果不是running则start三次，如果还不行，则报错
	etlStatus := s.getEtlStatus(resultTableId)
	if etlStatus != models.DatabusStatusRunning {
		if config.BypassSuffixPath != "" {
			logger.Infof("[db_diff] start bkdata databus clean and update db EtlJSONConfig [%s] and BkDataResultTableID [%s]", etlConfigJson, resultTableId)
			return nil
		}
		var done bool
		for i := 0; i < 3; i++ {
			// 启动清洗任务
			resp, err := apiservice.Bkdata.StartDatabusStatus(resultTableId, []string{"kafka"})
			if err != nil {
				return errors.Wrap(err, "start bkdata databus clean failed")
			}
			// 轮训状态，查看是否启动成功
			for j := 0; j < 10; j++ {
				status := s.getEtlStatus(resultTableId)
				if status == models.DatabusStatusRunning {
					logger.Infof("start bkdata databus clean success, result [%v]", resp)
					done = true
					break
				} else if status == models.DatabusStatusStarting {
					time.Sleep(time.Second)
				} else {
					return errors.Errorf("start bkdata databus clean failed, params [%v]", params)
				}
			}
			if done {
				break
			}
		}
		if !done {
			// 启动清洗任务不成功，则报错
			return errors.Errorf("start bkdata databus clean failed, param [%v]", params)
		}
		s.EtlJSONConfig = etlConfigJson
		s.BkDataResultTableID = resultTableId
		if err := s.Update(db, storage.BkDataStorageDBSchema.EtlJSONConfig, storage.BkDataStorageDBSchema.BkDataResultTableID); err != nil {
			return errors.Wrapf(err, "update BkDataStorage [%s] with etl_json_config [%s] bk_data_result_table_id [%v] failed", s.TableID, s.EtlJSONConfig, s.BkDataResultTableID)
		}
		// 提前做一次授权，授权给某个项目
		auth := NewDataFlowSvc()
		auth.EnsureHasPermissionWithRtId(resultTableId, config.BkdataProjectId)
	}
	// 过滤掉未来时间后再入库
	if s.FilterUnknownTimeWithRt() {
		if err := s.FullCMDBNodeInfoToResultTable(); err != nil {
			return errors.Wrapf(err, "FullCmdbNodeInfoToResultTable for result_table [%s] failed", s.TableID)
		}
	}
	return nil
}

func (s BkDataStorageSvc) generateBkDataEtlConfig() (map[string]interface{}, []map[string]interface{}, error) {
	db := mysql.GetDBSession().DB
	var rtfList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).TableIDEq(s.TableID).All(&rtfList); err != nil {
		return nil, nil, errors.Wrapf(err, "query ResultTableField with [%s] failed", s.TableID)
	}
	fields := make([]map[string]interface{}, 0)
	var etlDimensionAssign []map[string]string
	var etlMetricAssign []map[string]string
	var etlTimeAssign []map[string]string
	timeFieldName := "time"
	i := 1
	for _, field := range rtfList {
		fieldAlias := field.Description
		if fieldAlias == "" {
			fieldAlias = field.FieldName
		}
		if field.Tag == models.ResultTableFieldTagDimension || field.Tag == models.ResultTableFieldTagGroup {
			fields = append(fields, map[string]interface{}{
				"field_name":   field.FieldName,
				"field_type":   "string",
				"field_alias":  fieldAlias,
				"is_dimension": true,
				"field_index":  i},
			)
			etlDimensionAssign = append(etlDimensionAssign, map[string]string{"type": "string", "key": field.FieldName, "assign_to": field.FieldName})
		} else if field.Tag == models.ResultTableFieldTagMetric {
			// 计算平台没有float类型，这里使用double做一层转换
			// 监控的int类型转成计算平台的long类型
			fieldType := field.FieldType
			if field.FieldType == models.ResultTableFieldTypeFloat {
				fieldType = "double"
			} else if field.FieldType == models.ResultTableFieldTypeInt {
				fieldType = "long"
			}
			fields = append(fields, map[string]interface{}{
				"field_name":   field.FieldName,
				"field_type":   fieldType,
				"field_alias":  fieldAlias,
				"is_dimension": false,
				"field_index":  i},
			)
			etlMetricAssign = append(etlMetricAssign, map[string]string{"type": fieldType, "key": field.FieldName, "assign_to": field.FieldName})
		} else if field.Tag == models.ResultTableFieldTagTimestamp {
			timeFieldName = field.FieldName
			fields = append(fields, map[string]interface{}{
				"field_name":   field.FieldName,
				"field_type":   "string",
				"field_alias":  fieldAlias,
				"is_dimension": false,
				"field_index":  i},
			)
			etlTimeAssign = append(etlTimeAssign, map[string]string{"type": "string", "key": field.FieldName, "assign_to": field.FieldName})
		} else {
			continue
		}
		i += 1
	}
	etlConfig := map[string]interface{}{
		"extract": map[string]interface{}{
			"args":   []interface{}{},
			"type":   "fun",
			"label":  "label6356db",
			"result": "json",
			"next": map[string]interface{}{
				"type":  "branch",
				"name":  "",
				"label": nil,
				"next": []map[string]interface{}{
					{
						"type":   "access",
						"label":  "label5a9c45",
						"result": "dimensions",
						"next": map[string]interface{}{
							"type":    "assign",
							"label":   "labelb2c1cb",
							"subtype": "assign_obj",
							"assign":  etlDimensionAssign,
							"next":    nil,
						},
						"key":     "dimensions",
						"subtype": "access_obj",
					},
					{
						"type":   "access",
						"label":  "label65f2f1",
						"result": "metrics",
						"next": map[string]interface{}{
							"type":    "assign",
							"label":   "labela6b250",
							"subtype": "assign_obj",
							"assign":  etlMetricAssign,
							"next":    nil,
						},
						"key":     "metrics",
						"subtype": "access_obj",
					},
					{
						"type":    "assign",
						"label":   "labelecd758",
						"subtype": "assign_obj",
						"assign":  etlTimeAssign,
						"next":    nil,
					},
				},
			},
			"method": "from_json",
		},
		"conf": map[string]interface{}{
			"timezone":          8,
			"output_field_name": "timestamp",
			"time_format":       "Unix Time Stamp(seconds)",
			"time_field_name":   timeFieldName,
			"timestamp_len":     10,
			"encoding":          "UTF-8",
		}}
	return etlConfig, fields, nil
}

// 根据对应的processing_id获取该清洗任务的状态
func (s BkDataStorageSvc) getEtlStatus(processingId string) string {
	resp, err := apiservice.Bkdata.GetDatabusStatus(s.RawDataID)
	if err != nil {
		logger.Errorf("GetDatabusStatus with raw_data_id [%v] failed, %v", s.RawDataID, err)
		return ""
	}
	for _, etlTemplate := range resp {
		pId, ok := etlTemplate["processing_id"].(string)
		if ok && pId == processingId {
			status, _ := etlTemplate["status"].(string)
			return status
		}
	}
	return ""
}

// FilterUnknownTimeWithRt 通过dataflow过滤掉未来时间, 同时过滤过去时间后，再进行入库
func (s BkDataStorageSvc) FilterUnknownTimeWithRt() bool {
	db := mysql.GetDBSession().DB
	var rtfList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).TableIDEq(s.TableID).All(&rtfList); err != nil {
		logger.Errorf("query ResultTableField with table_id [%s] failed, %v", s.TableID, err)
		return false
	}
	if len(rtfList) == 0 {
		return false
	}
	var metricFields, dimensionFields []string
	for _, field := range rtfList {
		if field.Tag == models.ResultTableFieldTagDimension || field.Tag == models.ResultTableFieldTagGroup {
			dimensionFields = append(dimensionFields, field.FieldName)
		} else if field.Tag == models.ResultTableFieldTagMetric || field.Tag == models.ResultTableFieldTagTimestamp {
			metricFields = append(metricFields, field.FieldName)
		}
	}
	task := dataflow.NewFilterUnknownTimeTask(s.BkDataResultTableID, metricFields, dimensionFields)
	if task == nil {
		logger.Errorf("NewFilterUnknownTimeTask for rt [%s] failed", s.BkDataResultTableID)
		return false
	}
	if config.BypassSuffixPath != "" {
		for i, n := range task.NodeList {
			nodeConfig, _ := jsonx.MarshalString(n.Config())
			logger.Infof("[db_diff] rt [%s] create and start data flow with Node %d [%s] config [%s]", s.BkDataResultTableID, i+1, n.Name(), nodeConfig)
		}
		return false
	}

	if err := task.CreateFlow(false, 0); err != nil {
		logger.Errorf("create flow [%s] failed, result_id [%s], reason [%v]", task.FlowName(), s.BkDataResultTableID, err)
	}
	if err := task.StartFlow(""); err != nil {
		logger.Errorf("start flow [%s] failed, result_id [%s], reason [%v]", task.FlowName(), s.BkDataResultTableID, err)
	}
	// 通过轮训去查看该flow的启动状态，如果60s内启动成功则正常返回，如果不成功则报错
	for i := 0; i < 60; i++ {
		resp, err := apiservice.Bkdata.GetLatestDeployDataFlow(task.DataFlow.FlowId)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		status, _ := resp["status"].(string)
		if status == "success" {
			logger.Infof("create flow [%s] successfully, result_id [%s]", task.FlowName(), s.BkDataResultTableID)
			return true
		}
		time.Sleep(time.Second)
		continue
	}
	return false
}

// FullCMDBNodeInfoToResultTable  接入cmdb节点
func (s BkDataStorageSvc) FullCMDBNodeInfoToResultTable() error {
	if !config.BkdataIsAllowAllCmdbLevel {
		return nil
	}
	db := mysql.GetDBSession().DB
	var rtfList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).TableIDEq(s.TableID).All(&rtfList); err != nil {
		logger.Errorf("query ResultTableField with table_id [%s] failed, %v", s.TableID, err)
		return nil
	}
	if len(rtfList) == 0 {
		return nil
	}
	var metricFields, dimensionFields []string
	for _, field := range rtfList {
		if field.Tag == models.ResultTableFieldTagDimension || field.Tag == models.ResultTableFieldTagGroup {
			dimensionFields = append(dimensionFields, field.FieldName)
		} else if field.Tag == models.ResultTableFieldTagMetric {
			metricFields = append(metricFields, field.FieldName)
		}
	}
	task := dataflow.NewCMDBPrepareAggregateTask(
		ToBkdataRtId(s.TableID, config.BkdataRawTableSuffix),
		0,
		"",
		metricFields,
		dimensionFields,
	)
	if task == nil {
		return errors.Errorf("NewCMDBPrepareAggregateTask for rt [%s] failed", s.TableID)
	}
	if config.BypassSuffixPath != "" {
		for i, n := range task.NodeList {
			nodeConfig, _ := jsonx.MarshalString(n.Config())
			logger.Infof("[db_diff] rt [%s] create and start data flow with Node %d [%s] config [%s]", s.BkDataResultTableID, i+1, n.Name(), nodeConfig)
		}
		return nil
	}
	if err := task.CreateFlow(false, 0); err != nil {
		logger.Errorf("create flow [%s] failed, result_id [%s], reason [%v]", task.FlowName(), s.BkDataResultTableID, err)
	}
	if err := task.StartFlow(""); err != nil {
		logger.Errorf("start flow [%s] failed, result_id [%s], reason [%v]", task.FlowName(), s.BkDataResultTableID, err)
	}
	return nil
}

func ToBkdataRtId(tableId, suffix string) string {
	if tableId == "" {
		return ""
	}
	prefixList := []string{strconv.Itoa(config.BkdataBkBizId), GenBkdataRtIdWithoutBizId(tableId)}
	if suffix != "" {
		prefixList = append(prefixList, suffix)
	}
	return strings.Join(prefixList, "_")
}

// GenBkdataRtIdWithoutBizId 生成bkdata result id
func GenBkdataRtIdWithoutBizId(tableId string) string {
	rawDataName := fmt.Sprintf("%s_%s", config.BkdataRtIdPrefix, strings.ReplaceAll(tableId, ".", "_"))
	rawDataName = stringx.LimitLengthSuffix(rawDataName, 32)
	rtId := strings.TrimLeft(strings.ToLower(rawDataName), "_")
	return rtId
}
