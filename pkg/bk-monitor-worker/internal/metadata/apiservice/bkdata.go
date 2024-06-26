// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apiservice

import (
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	gormbulk "github.com/t-tiger/gorm-bulk-insert"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var Bkdata BkdataService

type BkdataService struct{}

// DatabusCleans 接入数据清洗
func (BkdataService) DatabusCleans(params bkdata.DatabusCleansParams) (map[string]interface{}, error) {
	params.BkUsername = config.BkdataProjectMaintainer
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}
	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.DataBusCleans().SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "DataBusCleans with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "DataBusCleans with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// StopDatabusCleans 停止清洗配置
func (BkdataService) StopDatabusCleans(resultTableId string, storages []string) (interface{}, error) {
	if len(storages) == 0 {
		storages = []string{"kafka"}
	}
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonResp
	if _, err = bkdataApi.StopDatabusCleans().SetPathParams(map[string]string{"result_table_id": resultTableId}).SetBody(map[string]interface{}{"storages": storages}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "StopDatabusCleans for result_table_id [%s] with storages [%v] failed", resultTableId, storages)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "StopDatabusCleans for result_table_id [%s] with storages [%v] failed", resultTableId, storages)
	}
	return resp.Data, nil
}

// UpdateDatabusCleans 更新数据清洗
func (BkdataService) UpdateDatabusCleans(processingId string, params bkdata.DatabusCleansParams) (interface{}, error) {
	params.BkUsername = config.BkdataProjectMaintainer
	if processingId == "" {
		return nil, errors.New("processing_id can not be empty")
	}
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonResp
	if _, err = bkdataApi.UpdateDatabusCleans().SetPathParams(map[string]string{"processing_id": processingId}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "UpdateDatabusCleans for processing_id [%s] with params [%v] failed", processingId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "UpdateDatabusCleans for processing_id [%s] with params [%v] failed", processingId, paramStr)
	}
	return resp.Data, nil
}

// GetDatabusStatus 查询数据清洗
func (BkdataService) GetDatabusStatus(rawDataId int) ([]map[string]interface{}, error) {
	if rawDataId == 0 {
		return nil, errors.New("rawDataId can not be 0")
	}
	rawDataIdStr := strconv.Itoa(rawDataId)
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonListMapResp
	if _, err = bkdataApi.GetDatabusCleans().SetQueryParams(map[string]string{"raw_data_id": rawDataIdStr}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "GetDatabusStatus with raw_data_id [%s] failed", rawDataIdStr)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "GetDatabusStatus with raw_data_id [%s] failed", rawDataIdStr)
	}
	return resp.Data, nil
}

// StartDatabusStatus 启动清洗配置
func (BkdataService) StartDatabusStatus(resultTableId string, storages []string) (interface{}, error) {
	if resultTableId == "" {
		return nil, errors.New("resultTableId can not be empty")
	}
	if len(storages) == 0 {
		storages = []string{"kafka"}
	}
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]interface{}{
		"result_table_id": resultTableId,
		"storages":        storages,
	}
	var resp bkdata.CommonResp
	if _, err = bkdataApi.StartDatabusCleans().SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "StartDatabusCleans with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "StartDatabusCleans with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// AuthProjectsDataCheck 效验表是否有权限
func (BkdataService) AuthProjectsDataCheck(projectId int, resultTableId, actionId string) (bool, error) {
	if actionId == "" {
		actionId = "result_table.query_data"
	}
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return false, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]string{
		"result_table_id": resultTableId,
	}
	var resp bkdata.CommonResp
	if _, err = bkdataApi.AuthProjectsDataCheck().SetPathParams(map[string]string{"project_id": strconv.Itoa(projectId)}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return false, errors.Wrapf(err, "AuthProjectsDataCheck for project_id [%d] with params [%s] failed", projectId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return false, errors.Wrapf(err, "AuthProjectsDataCheck for project_id [%d] with params [%s] failed", projectId, paramStr)
	}
	result, _ := resp.Data.(bool)
	return result, nil
}

// AuthResultTable 针对结果表授权给项目
func (BkdataService) AuthResultTable(projectId int, objectId string, bkBizId string) (interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]interface{}{
		"object_id": objectId,
		"bk_biz_id": bkBizId,
	}
	var resp bkdata.CommonResp
	if _, err = bkdataApi.AuthResultTable().SetPathParams(map[string]string{"project_id": strconv.Itoa(projectId)}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "AuthResultTable for project_id [%d] with params [%s] failed", projectId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "AuthResultTable for project_id [%d] with params [%s] failed", projectId, paramStr)
	}

	return resp.Data, nil
}

// UpdateDataFlowNode 更新dataflow node
func (s BkdataService) UpdateDataFlowNode(flowId int, params bkdata.UpdateDataFlowNodeParams) (interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}
	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.UpdateDataFlowNode().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "UpdateDataFlowNode with flow_id [%d] params [%s] failed", flowId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "UpdateDataFlowNode with flow_id [%d] params [%s] failed", flowId, paramStr)
	}
	return resp.Data, nil
}

// AddDataFlowNode 新增dataflow node
func (s BkdataService) AddDataFlowNode(flowId int, params bkdata.DataFlowNodeParams) (map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.AddDataFlowNode().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "AddDataFlowNode with flow_id [%d] params [%s] failed", flowId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "AddDataFlowNode with flow_id [%d] params [%s] failed", flowId, paramStr)
	}
	return resp.Data, nil
}

// GetLatestDeployDataFlow 获取指定dataflow最后一次部署的信息
func (s BkdataService) GetLatestDeployDataFlow(flowId int) (map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.GetLatestDeployDataFlow().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "GetLatestDeployDataFlow with flow_id [%d] failed", flowId)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "GetLatestDeployDataFlow with flow_id [%d] failed", flowId)
	}
	return resp.Data, nil
}

// GetDataFlow 获取dataflow信息
func (s BkdataService) GetDataFlow(flowId int) (map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.GetDataFlow().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "GetDataFlow with flow_id [%d] failed", flowId)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "GetDataFlow with flow_id [%d] failed", flowId)
	}
	return resp.Data, nil
}

// GetDataFlowGraph 获取DataFlow里的画布信息
func (s BkdataService) GetDataFlowGraph(flowId int) (*bkdata.GetDataFlowGraphRespData, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.GetDataFlowGraphResp
	if _, err = bkdataApi.GetDataFlowGraph().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "GetDataFlowGraph with flow_id [%d] failed", flowId)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "GetDataFlowGraph with flow_id [%d] failed", flowId)
	}
	return resp.Data, nil
}

// GetDataFlowList 获取项目下的dataflow列表
func (s BkdataService) GetDataFlowList(projectId int) ([]map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonListMapResp
	if _, err = bkdataApi.GetDataFlowList().SetQueryParams(map[string]string{"project_id": strconv.Itoa(projectId)}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "GetDataFlowList with project_id [%d] failed", projectId)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "GetDataFlowList with project_id [%d] failed", projectId)
	}
	return resp.Data, nil
}

// CreateDataFlow 创建DataFlow
func (s BkdataService) CreateDataFlow(flowName string, projectId int, nodes []map[string]interface{}) (map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]interface{}{
		"flow_name":  flowName,
		"project_id": projectId,
	}
	if len(nodes) != 0 {
		params["nodes"] = nodes
	}
	var resp bkdata.CommonMapResp
	if _, err = bkdataApi.CreateDataFlow().SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "CreateDataFlow with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "StartDataFlow with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// StopDataFlow 停止dataflow
func (s BkdataService) StopDataFlow(flowId int) (interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonResp
	if _, err = bkdataApi.StopDataFlow().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "StopDataFlow for flow_id [%d] failed", flowId)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "StopDataFlow for flow_id [%d] failed", flowId)
	}
	return resp.Data, nil
}

// StartDataFlow 启动dataflow
func (s BkdataService) StartDataFlow(flowId int, consumingMode, clusterGroup string) (interface{}, error) {
	if consumingMode == "" {
		consumingMode = "continue"
	}
	if clusterGroup == "" {
		clusterGroup = "default"
	}

	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]interface{}{
		"consuming_mode": consumingMode,
		"cluster_group":  clusterGroup,
	}
	var resp bkdata.CommonResp
	if _, err = bkdataApi.StartDataFlow().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "StartDataFlow for flow_id [%d] with params [%s] failed", flowId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "StartDataFlow for flow_id [%d] with params [%s] failed", flowId, paramStr)
	}
	return resp.Data, nil
}

// RestartDataFlow 重启dataflow
func (s BkdataService) RestartDataFlow(flowId int, consumingMode, clusterGroup string) (interface{}, error) {
	if consumingMode == "" {
		consumingMode = "continue"
	}
	if clusterGroup == "" {
		clusterGroup = "default"
	}

	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	params := map[string]interface{}{
		"consuming_mode": consumingMode,
		"cluster_group":  clusterGroup,
	}
	var resp bkdata.CommonResp
	if _, err = bkdataApi.RestartDataFlow().SetPathParams(map[string]string{"flow_id": strconv.Itoa(flowId)}).SetBody(params).SetResult(&resp).Request(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "RestartDataFlow for flow_id [%d] with params [%s] failed", flowId, paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "RestartDataFlow for flow_id [%d] with params [%s] failed", flowId, paramStr)
	}
	return resp.Data, nil
}

// QueryMetrics 查询指标数据
func (s BkdataService) QueryMetrics(storage string, rt string) (*map[string]float64, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonListResp
	if _, err = bkdataApi.QueryMetrics().SetQueryParams(map[string]string{"storage": storage, "result_table_id": rt}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "query metrics error by bkdata: %s, table_id: %s", storage, rt)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "query metrics error by bkdata: %s, table_id: %s", storage, rt)
	}
	// parse metrics
	metrics := make(map[string]float64)
	for _, data := range resp.Data {
		metricInfo, ok := data.([]interface{})
		if !ok {
			logger.Errorf("parse metrics data error, metric_info: %v", metricInfo)
			continue
		}
		metric, ok := metricInfo[0].(string)
		if !ok {
			logger.Errorf("parse metrics data error, metric: %v", metric)
			continue
		}
		// NOTE: 如果时间戳不符合预期，则忽略该指标
		timestamp, ok := metricInfo[1].(float64)
		if !ok {
			logger.Errorf("parse metrics data error, timestamp: %v", timestamp)
			continue
		}
		metrics[metric] = timestamp
	}
	return &metrics, nil
}

// QueryDimension 查询维度数据
func (s BkdataService) QueryDimension(storage string, rt string, metric string) (*[]map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}

	var resp bkdata.CommonListResp
	if _, err = bkdataApi.QueryDimension().SetQueryParams(map[string]string{"storage": storage, "result_table_id": rt, "metric": metric}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "query dimension error by bkdata: %s, table_id: %s", storage, rt)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "query dimension error by bkdata: %s, table_id: %s", storage, rt)
	}
	// parse dimension
	var dimensions []map[string]interface{}
	for _, data := range resp.Data {
		dimensionInfo, ok := data.([]interface{})
		if !ok {
			logger.Errorf("parse dimension data error, dimension_info: %v", dimensionInfo)
			continue
		}
		dimension, ok := dimensionInfo[0].(string)
		if !ok {
			logger.Errorf("parse dimension data error, dimension: %v", dimension)
			continue
		}
		// NOTE: 如果时间戳不符合预期，则忽略该指标
		timestamp, ok := dimensionInfo[1].(float64)
		if !ok {
			logger.Errorf("parse dimension data error, timestamp: %v", timestamp)
			continue
		}
		dimensions = append(dimensions, map[string]interface{}{dimension: map[string]interface{}{"last_update_time": timestamp}})
	}
	return &dimensions, nil
}

// QueryMetricAndDimension 查询指标和维度数据
func (s BkdataService) QueryMetricAndDimension(storage string, rt string) ([]map[string]interface{}, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get bkdata api failed")
	}
	var resp bkdata.CommonMapResp
	// NOTE: 设置no_value=true，不需要返回维度对应的 value
	params := map[string]string{"storage": storage, "result_table_id": rt, "no_value": "true"}
	if _, err = bkdataApi.QueryMetricAndDimension().SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "query metrics and dimension error by bkdata: %s, table_id: %s", storage, rt)
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrapf(err, "query metrics and dimension error by bkdata: %s, table_id: %s", storage, rt)
	}

	metrics := resp.Data["metrics"]
	metricInfo, ok := metrics.([]interface{})
	if !ok || len(metricInfo) == 0 {
		logger.Errorf("query bkdata metrics error, params: %v, metrics: %v", params, metricInfo)
		return nil, errors.New("query metrics error, no data")
	}

	// parse metrics and dimensions
	var MetricsDimension []map[string]interface{}
	for _, dataInfo := range metricInfo {
		data, ok := dataInfo.(map[string]interface{})
		if !ok {
			logger.Errorf("metric data not map[string]interface{}, data: %v", params, metricInfo)
			continue
		}
		lastModifyTime := data["update_time"].(float64)
		dimensions := data["dimensions"].([]interface{})
		tagValueList := make(map[string]interface{})
		for _, dimInfo := range dimensions {
			dim, ok := dimInfo.(map[string]interface{})
			if !ok {
				logger.Errorf("dimension data not map[string]interface{}, dimInfo: %v", dimInfo)
				continue
			}
			// 判断值为 string
			tag_name, ok := dim["name"].(string)
			if !ok {
				logger.Errorf("dimension: %s is not string", dim["name"])
				continue
			}
			// 判断值为 float64
			tagUpdateTime, ok := dim["update_time"].(float64)
			if !ok {
				logger.Errorf("dimension: %s is not string", dim["name"])
				continue
			}
			tagValueList[tag_name] = map[string]interface{}{"last_update_time": tagUpdateTime / 1000}
		}

		item := map[string]interface{}{
			"field_name":       data["name"],
			"last_modify_time": lastModifyTime / 1000,
			"tag_value_list":   tagValueList,
		}
		MetricsDimension = append(MetricsDimension, item)
	}
	return MetricsDimension, nil
}

// SyncBkBaseResultTables 同步bkbase结果表
func (s BkdataService) SyncBkBaseResultTables(bkBizId, page, pageSize *int) error {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return errors.Wrap(err, "get bkdata api failed")
	}

	// 查询条件
	params := make(map[string]string)
	if bkBizId != nil {
		params["bk_biz_id"] = strconv.Itoa(*bkBizId)
	}
	if page != nil {
		params["page"] = strconv.Itoa(*page)
	}
	if pageSize != nil {
		params["page_size"] = strconv.Itoa(*pageSize)
	}
	params["genereage_type"] = "user"
	params["related"] = "fields"
	params["is_query"] = strconv.Itoa(1)

	// 访问bkbase接口获取数据
	bkApiAppCodeSecret, _ := json.Marshal(map[string]string{"bk_app_code": config.BkApiAppCode, "bk_app_secret": config.BkApiAppSecret})
	var resp bkdata.CommonListResp
	if _, err = bkdataApi.QueryResultTables().SetHeaders(map[string]string{"X-Bkapi-Authorization": string(bkApiAppCodeSecret)}).
		SetQueryParams(params).SetResult(&resp).Request(); err != nil {
		return errors.Wrapf(err, "query bkbase resulttables error by bkbizid: %v", bkBizId)
	}
	if err := resp.Err(); err != nil {
		return errors.Wrapf(err, "query bkbase resulttables error by bkbizid: %v", bkBizId)
	}

	var (
		resultTables       = make([]interface{}, 0)
		resultTableFields  = make([]interface{}, 0)
		mysqlStorages      = make([]interface{}, 0)
		hdfsStorages       = make([]interface{}, 0)
		tspiderStorages    = make([]interface{}, 0)
		postgresqlStorages = make([]interface{}, 0)
		redisStorages      = make([]interface{}, 0)
		oracleStorages     = make([]interface{}, 0)
		dorisStorages      = make([]interface{}, 0)
	)
	for _, data := range resp.Data {
		tableInfo, ok := data.(map[string]interface{})
		if !ok {
			logger.Errorf("parse result table data error, result_table_info: %v", tableInfo)
			continue
		}

		// 获取结果表内容
		tableId := fmt.Sprintf("bkbase_%s.__default__", tableInfo["result_table_id"].(string))
		rt := s.GetBkBaseResultTables(tableId, tableInfo)
		resultTables = append(resultTables, rt)

		// 获取结果表字段内容
		fields := tableInfo["fields"].([]interface{})
		rtf := s.GetBkBaseResultTableFields(tableId, fields)
		for _, field := range rtf {
			resultTableFields = append(resultTableFields, field)
		}

		// 获取每个tableId的存储类型存放于存储类型表
		storages := tableInfo["query_storages_arr"].([]interface{})
		for _, ste := range storages {
			switch ste.(string) {
			case models.StorageTypeMySQL:
				mysqlStorages = append(mysqlStorages, storage.MysqlStorage{TableID: tableId, StorageClusterID: models.MySQLStorageClusterId})
			case models.StorageTypeHdfs:
				hdfsStorages = append(hdfsStorages, storage.HdfsStorage{TableID: tableId, StorageClusterID: models.HdfsStorageClusterId})
			case models.StorageTypeTspider:
				tspiderStorages = append(tspiderStorages, storage.TsPiDerStorage{TableID: tableId, StorageClusterID: models.TspiderStorageClusterId})
			case models.StorageTypePostgresql:
				postgresqlStorages = append(postgresqlStorages, storage.PostgresqlStorage{TableID: tableId, StorageClusterID: models.PostgresqlStorageClusterId})
			case models.StorageTypeRedis:
				redisStorages = append(redisStorages, storage.RedisStorage{TableID: tableId, StorageClusterID: models.RedisStorageClusterId})
			case models.StorageTypeOracle:
				oracleStorages = append(oracleStorages, storage.OracleStorage{TableID: tableId, StorageClusterID: models.OracleStorageClusterId})
			case models.StorageTypeDoris:
				dorisStorages = append(dorisStorages, storage.DorisStorage{TableID: tableId, StorageClusterID: models.DorisStorageClusterId})
			}
		}
	}

	// 将结果表, 结果字段表, 存储关联表插入db
	db := mysql.GetDBSession().DB
	tx := db.Begin()
	if err := gormbulk.BulkInsert(tx, resultTables, len(resultTables)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, resultTableFields, len(resultTableFields)); err != nil {
		tx.Rollback()
		logger.Errorf("insert result table field  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, mysqlStorages, len(mysqlStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert mysql storages table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, tspiderStorages, len(tspiderStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert tspider storages table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, postgresqlStorages, len(postgresqlStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert postgresql storages table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, redisStorages, len(redisStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert redis storages table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, oracleStorages, len(oracleStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert oracle storages table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, dorisStorages, len(dorisStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert doris storages table  error, error: %s", err)
	}
	if err := gormbulk.BulkInsert(tx, hdfsStorages, len(hdfsStorages)); err != nil {
		tx.Rollback()
		logger.Errorf("insert hdfs storages table  error, error: %s", err)
	}
	tx.Commit()
	return nil
}

func (s BkdataService) GetBkBaseResultTables(tableId string, tableInfo map[string]interface{}) resulttable.ResultTable {
	tableNameZh := tableInfo["result_table_name"].(string)
	operator := tableInfo["created_by"].(string)
	bkBizId := tableInfo["bk_biz_id"].(float64)
	storages := tableInfo["query_storages_arr"].([]interface{})
	storageList := make([]string, 0)
	// 保存数据存储类型
	for _, sto := range storages {
		switch sto {
		case models.StorageTypeMySQL:
			storageList = append(storageList, models.StorageTypeMySQL)
		case models.StorageTypeHdfs:
			storageList = append(storageList, models.StorageTypeHdfs)
		case models.StorageTypePostgresql:
			storageList = append(storageList, models.StorageTypePostgresql)
		case models.StorageTypeTspider:
			storageList = append(storageList, models.StorageTypeTspider)
		case models.StorageTypeRedis:
			storageList = append(storageList, models.StorageTypeRedis)
		case models.StorageTypeOracle:
			storageList = append(storageList, models.StorageTypeOracle)
		case models.StorageTypeDoris:
			storageList = append(storageList, models.StorageTypeDoris)
		}
	}
	storageStr := strings.Join(storageList, ",")
	defaultStorage := storages[0].(string)
	rt := resulttable.ResultTable{
		TableId:            tableId,
		TableNameZh:        tableNameZh,
		IsCustomTable:      false,
		SchemaType:         models.ResultTableSchemaTypeFree,
		DefaultStorage:     defaultStorage,
		Creator:            operator,
		CreateTime:         time.Now(),
		LastModifyUser:     operator,
		LastModifyTime:     time.Now(),
		BkBizId:            int(bkBizId),
		IsDeleted:          false,
		Label:              models.StorageTypeBkbase,
		IsEnable:           true,
		DataLabel:          nil,
		StorageClusterType: &storageStr,
	}
	return rt
}

func (s BkdataService) GetBkBaseResultTableFields(tableId string, fields []interface{}) []resulttable.ResultTableField {
	// 创建逻辑结果表字段内容
	var resultTableFields []resulttable.ResultTableField
	for _, data := range fields {
		field, ok := data.(map[string]interface{})
		if !ok {
			logger.Errorf("parse result table field data error, field_info: %v", field)
			continue
		}
		description, _ := field["description"].(string)
		aliasName, _ := field["field_alias"].(string)
		rtf := resulttable.ResultTableField{
			TableID:        tableId,
			FieldName:      field["field_name"].(string),
			FieldType:      field["field_type"].(string),
			Description:    description,
			Creator:        field["created_by"].(string),
			CreateTime:     time.Now(),
			LastModifyUser: field["updated_by"].(string),
			LastModifyTime: time.Now(),
			AliasName:      aliasName,
			IsDisabled:     false,
		}
		resultTableFields = append(resultTableFields, rtf)
	}
	return resultTableFields
}
