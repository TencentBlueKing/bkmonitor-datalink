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
	"strconv"

	"github.com/jinzhu/gorm"
	"github.com/nsf/jsondiff"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// CustomReportSubscriptionSvc custom report subscription service
type CustomReportSubscriptionSvc struct {
	*customreport.CustomReportSubscription
}

func NewCustomReportSubscriptionSvc(obj *customreport.CustomReportSubscription) CustomReportSubscriptionSvc {
	return CustomReportSubscriptionSvc{
		CustomReportSubscription: obj,
	}
}

// RefreshCollectorCustomConf 指定业务ID更新，或者更新全量业务
func (s CustomReportSubscriptionSvc) RefreshCollectorCustomConf(bkBizId *int, pluginName, opType string) error {
	var BkBizIdStr string
	if bkBizId == nil {
		BkBizIdStr = "nil"
	} else {
		BkBizIdStr = strconv.Itoa(*bkBizId)
	}
	logger.Infof("refresh custom report config to proxy on bk_biz_id [%s]", BkBizIdStr)
	if opType == "" {
		opType = "add"
	}
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return err
	}
	var bizResp cmdb.SearchBusinessResp
	if _, err := cmdbApi.SearchBusiness().SetResult(&bizResp).Request(); err != nil {
		return err
	}

	customEventConfig, err := s.GetCustomEventConfig(bkBizId, pluginName)
	if err != nil {
		return err
	}
	customTSConfig, err := s.GetCustomTSConfig(bkBizId, pluginName)
	if err != nil {
		return err
	}
	dictItems := make(map[int][]map[string]interface{})
	for k, v := range customEventConfig {
		dictItems[k] = v
	}
	for k, v := range customTSConfig {
		if configs, ok := dictItems[k]; ok {
			dictItems[k] = append(configs, v...)
		} else {
			dictItems[k] = v
		}
	}

	var isAllBizRefresh bool
	if bkBizId == nil {
		isAllBizRefresh = true
	}
	var bizIdToProxy = make(map[int][]cmdb.ListBizHostsTopoDataInfo)
	for _, bizInfo := range bizResp.Data.Info {
		if !isAllBizRefresh && *bkBizId != bizInfo.BkBizId {
			// 如果仅仅是只刷新一个业务，则跳过其他业务的proxy获取
			continue
		}
		nodemanApi, err := api.GetNodemanApi()
		if err != nil {
			return err
		}
		var proxiesResp nodeman.GetProxiesResp
		_, err = nodemanApi.GetProxiesByBiz().SetQueryParams(map[string]string{"bk_biz_id": strconv.Itoa(bizInfo.BkBizId)}).SetResult(&proxiesResp).Request()
		if err != nil {
			return err
		}
		var proxyBizIdList []int
		for _, proxy := range proxiesResp.Data {
			proxyBizIdList = append(proxyBizIdList, proxy.BkBizId)
		}
		var proxyHostList []cmdb.ListBizHostsTopoDataInfo
		for _, proxyBizId := range proxyBizIdList {
			var params []GetHostByIpParams
			for _, proxy := range proxiesResp.Data {
				if proxy.BkBizId == proxyBizId {
					params = append(params, GetHostByIpParams{
						Ip:        proxy.InnerIp,
						BkCloudId: proxy.BkCloudId,
					})
				}
			}
			hostInfos, err := NewBcsClusterInfoSvc(nil).getHostByIp(params, proxyBizId)
			if err != nil {
				return err
			}
			if len(hostInfos) != 0 {
				proxyHostList = append(proxyHostList, hostInfos...)
			}
		}
		for _, host := range proxyHostList {
			if ls, ok := bizIdToProxy[bizInfo.BkBizId]; ok {
				bizIdToProxy[bizInfo.BkBizId] = append(ls, host)
			} else {
				bizIdToProxy[bizInfo.BkBizId] = []cmdb.ListBizHostsTopoDataInfo{host}
			}
		}

	}

	for bizId, items := range dictItems {
		if bizId > 0 {
			var exist bool
			for _, bizInfo := range bizResp.Data.Info {
				if bizId == bizInfo.BkBizId {
					exist = true
					break
				}
			}
			// 如果cmdb不存在这个业务，那么需要跳过这个业务的下发
			if !exist {
				logger.Infof("biz_id [%v] does not exists in cmdb", bizId)
				continue
			}
		}
		if !isAllBizRefresh && *bkBizId != bizId {
			// 如果仅仅是只刷新一个业务，则跳过其他业务的下发
			continue
		}
		// 从节点管理查询到biz_id下的Proxy机器
		hostList := bizIdToProxy[bizId]
		if len(hostList) == 0 {
			logger.Warnf("Update custom report config to biz_id [%v] error, No proxy found", bizId)
			continue
		}
		var bkHostIdList []int
		for _, host := range hostList {
			bkHostIdList = append(bkHostIdList, host.Host.BkHostId)
		}
		// 通过节点管理下发配置
		err := s.CreateSubscription(bizId, items, bkHostIdList, pluginName, opType)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetCustomEventConfig 获取自定义上报event配置的数据
func (CustomReportSubscriptionSvc) GetCustomEventConfig(bkBizId *int, pluginName string) (map[int][]map[string]interface{}, error) {
	var BkBizIdStr string
	if bkBizId == nil {
		BkBizIdStr = "nil"
	} else {
		BkBizIdStr = strconv.Itoa(*bkBizId)
	}
	logger.Infof("get custom event config, bk_biz_id [%v]", BkBizIdStr)
	db := mysql.GetDBSession().DB
	eventGroupQS := customreport.NewEventGroupQuerySet(db).IsEnableEq(true).IsDeleteEq(false)
	if bkBizId != nil {
		eventGroupQS = eventGroupQS.BkBizIDEq(*bkBizId)
	}
	var eventGroupList []customreport.EventGroup
	if err := eventGroupQS.All(&eventGroupList); err != nil {
		return nil, err
	}
	if len(eventGroupList) == 0 {
		return nil, nil
	}
	var bkDataIdList []uint
	for _, eg := range eventGroupList {
		bkDataIdList = append(bkDataIdList, eg.BkDataID)
	}

	// 从数据库查询到bk_biz_id到自定义上报配置的数据
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).BkDataIdIn(bkDataIdList...).All(&dsList); err != nil {
		return nil, err
	}
	var dsMap = make(map[uint]resulttable.DataSource)
	for _, ds := range dsList {
		dsMap[ds.BkDataId] = ds
	}
	var result = make(map[int][]map[string]interface{})
	for _, eg := range eventGroupList {
		ds, ok := dsMap[eg.BkDataID]
		if !ok {
			continue
		}
		maxRate := eg.MaxRate
		if maxRate < 0 {
			maxRate = models.MaxDataIdThroughPut
		}
		var dataIdConfig = map[string]interface{}{
			"dataid":                 eg.BkDataID,
			"datatype":               "event",
			"version":                "v2",
			"access_token":           ds.Token,
			"max_rate":               maxRate,
			"max_future_time_offset": models.MaxFutureTimeOffset,
		}
		if pluginName == "bk-collector" {
			dataIdConfig["bk_data_token"] = ds.Token
			dataIdConfig["bk_data_id"] = eg.BkDataID
			dataIdConfig["token_config"] = map[string]interface{}{
				"name":         "token_checker/proxy",
				"proxy_dataid": eg.BkDataID,
				"proxy_token":  ds.Token,
			}
			dataIdConfig["qps_config"] = map[string]interface{}{
				"name": "rate_limiter/token_bucket",
				"type": "token_bucket",
				"qps":  maxRate,
			}
			dataIdConfig["validator_config"] = map[string]interface{}{
				"name":                   "proxy_validator/common",
				"type":                   "event",
				"version":                "v2",
				"max_future_time_offset": models.MaxFutureTimeOffset,
			}
		}
		if configs, ok := result[eg.BkBizID]; ok {
			result[eg.BkBizID] = append(configs, dataIdConfig)
		} else {
			result[eg.BkBizID] = []map[string]interface{}{dataIdConfig}
		}
	}
	return result, nil
}

// GetCustomTSConfig 获取自定义上报TS配置的数据
func (CustomReportSubscriptionSvc) GetCustomTSConfig(bkBizId *int, pluginName string) (map[int][]map[string]interface{}, error) {
	var BkBizIdStr string
	if bkBizId == nil {
		BkBizIdStr = "nil"
	} else {
		BkBizIdStr = strconv.Itoa(*bkBizId)
	}
	logger.Infof("get custom ts config, bk_biz_id [%v]", BkBizIdStr)
	db := mysql.GetDBSession().DB
	tsGroupQS := customreport.NewTimeSeriesGroupQuerySet(db).IsEnableEq(true).IsDeleteEq(false)
	if bkBizId != nil {
		tsGroupQS = tsGroupQS.BkBizIDEq(*bkBizId)
	}
	var tsGroupList []customreport.TimeSeriesGroup
	if err := tsGroupQS.All(&tsGroupList); err != nil {
		return nil, err
	}
	if len(tsGroupList) == 0 {
		return nil, nil
	}
	var bkDataIdList []uint
	for _, ts := range tsGroupList {
		bkDataIdList = append(bkDataIdList, ts.BkDataID)
	}

	// 从数据库查询到bk_biz_id到自定义上报配置的数据
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).BkDataIdIn(bkDataIdList...).All(&dsList); err != nil {
		return nil, err
	}
	var dsMap = make(map[uint]resulttable.DataSource)
	for _, ds := range dsList {
		dsMap[ds.BkDataId] = ds
	}
	var result = make(map[int][]map[string]interface{})
	for _, ts := range tsGroupList {
		ds, ok := dsMap[ts.BkDataID]
		if !ok {
			continue
		}
		maxRate := ts.MaxRate
		if maxRate < 0 {
			maxRate = models.MaxDataIdThroughPut
		}
		var dataIdConfig = map[string]interface{}{
			"dataid":                 ts.BkDataID,
			"datatype":               "time_series",
			"version":                "v2",
			"access_token":           ds.Token,
			"max_rate":               maxRate,
			"max_future_time_offset": models.MaxFutureTimeOffset,
		}
		if pluginName == "bk-collector" {
			dataIdConfig["bk_data_token"] = ds.Token
			dataIdConfig["bk_data_id"] = ts.BkDataID
			dataIdConfig["token_config"] = map[string]interface{}{
				"name":         "token_checker/proxy",
				"proxy_dataid": ts.BkDataID,
				"proxy_token":  ds.Token,
			}
			dataIdConfig["qps_config"] = map[string]interface{}{
				"name": "rate_limiter/token_bucket",
				"type": "token_bucket",
				"qps":  maxRate,
			}
			dataIdConfig["validator_config"] = map[string]interface{}{
				"name":                   "proxy_validator/common",
				"type":                   "time_series",
				"version":                "v2",
				"max_future_time_offset": models.MaxFutureTimeOffset,
			}
		}
		if configs, ok := result[ts.BkBizID]; ok {
			result[ts.BkBizID] = append(configs, dataIdConfig)
		} else {
			result[ts.BkBizID] = []map[string]interface{}{dataIdConfig}
		}
	}
	return result, nil
}

func (s CustomReportSubscriptionSvc) CreateSubscription(bkBizId int, items []map[string]interface{}, bkHostIds []int, pluginName, opType string) error {
	logger.Infof("update or create subscription task, bk_biz_id [%v], target_hosts [%v], plugin [%s]", bkBizId, bkHostIds, pluginName)
	nodes := make([]map[string]interface{}, 0)
	for _, hostId := range bkHostIds {
		nodes = append(nodes, map[string]interface{}{"bk_host_id": hostId})
	}
	scope := map[string]interface{}{
		"object_type": "HOST",
		"node_type":   "INSTANCE",
		"nodes":       nodes,
	}
	if pluginName == "bkmonitorproxy" {
		subscriptionParams := map[string]interface{}{
			"scope": scope,
			"steps": []interface{}{
				map[string]interface{}{
					"id":   "bkmonitorproxy",
					"type": "PLUGIN",
					"config": map[string]interface{}{
						"plugin_name":    pluginName,
						"plugin_version": "latest",
						"config_templates": []interface{}{
							map[string]interface{}{"name": "bkmonitorproxy_report.conf", "version": "latest"},
						},
					},
					"params": map[string]interface{}{
						"context": map[string]interface{}{
							"listen_ip":      "{{ cmdb_instance.host.bk_host_innerip }}",
							"listen_port":    models.BkMonitorProxyListenPort,
							"max_length":     models.MaxReqLength,
							"max_throughput": models.MaxReqThroughPut,
							"items":          items,
						},
					},
				},
			},
		}
		return s.CreateOrUpdateConfig(subscriptionParams, bkBizId, "bkmonitorproxy", 0)
	}
	for _, item := range items {
		context := map[string]interface{}{
			"bk_biz_id": bkBizId,
		}
		for k, v := range item {
			context[k] = v
		}
		subscriptionParams := map[string]interface{}{
			"scope": scope,
			"steps": []interface{}{
				map[string]interface{}{
					"id":   pluginName,
					"type": "PLUGIN",
					"config": map[string]interface{}{
						"plugin_name":    pluginName,
						"plugin_version": "latest",
						"config_templates": []interface{}{
							map[string]interface{}{"name": "bk-collector-report-v2.conf", "version": "latest"},
						},
					},
					"params": map[string]interface{}{
						"context": context,
					},
				},
			},
		}
		bkDataIdInterface, ok := item["bk_data_id"]
		if !ok {
			return errors.New("config lack of bk_data_id")
		}
		bkDataId, ok := bkDataIdInterface.(uint)
		if !ok {
			return errors.New("bk_data_id asset error")
		}
		if err := s.CreateOrUpdateConfig(subscriptionParams, bkBizId, pluginName, bkDataId); err != nil {
			return err
		}

	}
	return nil
}

func (s CustomReportSubscriptionSvc) CreateOrUpdateConfig(params map[string]interface{}, bkBizId int, pluginName string, bkDataId uint) error {
	db := mysql.GetDBSession().DB
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return err
	}
	// 若订阅存在则判定是否更新，若不存在则创建
	// 使用proxy下发bk_data_id为默认值0，一个业务下的多个data_id对应一个订阅
	var subscrip customreport.CustomReportSubscription
	if err := customreport.NewCustomReportSubscriptionQuerySet(db).BkBizIdEq(bkBizId).BkDataIDEq(bkDataId).One(&subscrip); err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return err
		}
	}

	if subscrip.ID != 0 {
		logger.Infof("subscription task already exists")
		params["subscription_id"] = subscrip.SubscriptionId
		params["run_immediately"] = true
		// bkmonitorproxy原订阅scope配置为空则不再管理该订阅
		var subscripConfig map[string]interface{}

		if err := jsonx.UnmarshalString(subscrip.Config, &subscripConfig); err != nil {
			return err
		}
		scopeInterface, ok := subscripConfig["scope"]
		if !ok {
			return errors.New("subscription config parse error")
		}
		scope, ok := scopeInterface.(map[string]interface{})
		if !ok {
			return errors.New("subscription config parse error")
		}
		var oldNodes []interface{}
		oldNodesInterface, ok := scope["nodes"]
		if !ok {
			oldNodes = nil
		} else {
			oldNodes, ok = oldNodesInterface.([]interface{})
			if !ok {
				return errors.New("subscription config parse error")
			}
		}
		if pluginName == "bkmonitorproxy" && len(oldNodes) == 0 {
			logger.Infof("[bkmonitorproxy] target_hosts is None, don't need to update subscription task.")
			return nil
		}
		oldConfigBytes := []byte(subscrip.Config)
		newConfigBytes, err := jsonx.Marshal(params)
		if err != nil {
			return err
		}
		options := jsondiff.DefaultJSONOptions()
		compared, _ := jsondiff.Compare(newConfigBytes, oldConfigBytes, &options)
		if compared != jsondiff.FullMatch {
			logger.Infof("subscription task config has changed, update it")
			var resp define.APICommonResp
			_, err = nodemanApi.UpdateSubscription().SetBody(params).SetResult(&resp).Request()
			if err != nil {
				return err
			}
			logger.Infof("update subscription successful, result [%s]", resp.Message)

			subscrip.Config = string(newConfigBytes)
			if err := subscrip.Update(db, customreport.CustomReportSubscriptionDBSchema.Config); err != nil {
				return err
			}
		}
	}
	logger.Infof("subscription task not exists, create it")
	var resp define.APICommonResp
	_, err = nodemanApi.CreateSubscription().SetBody(params).SetResult(&resp).Request()
	logger.Infof("create subscription successful, result: %s", resp.Message)
	// 创建订阅成功后，优先存储下来，不然因为其他报错会导致订阅ID丢失
	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		return errors.New("parse api response error")
	}
	subscripIdInterface, ok := dataMap["subscription_id"]
	if !ok {
		return errors.New("parse api response error")
	}
	subscripId, ok := subscripIdInterface.(int64)
	if !ok {
		return errors.New("parse api response error")
	}
	newConfig, err := jsonx.MarshalString(params)
	if err != nil {
		return err
	}
	newSub := customreport.CustomReportSubscription{
		BkBizId:        bkBizId,
		SubscriptionId: int(subscripId),
		BkDataID:       bkDataId,
		Config:         newConfig,
	}
	if err := newSub.Create(db); err != nil {
		return err
	}
	var installResp define.APICommonResp
	_, err = nodemanApi.RunSubscription().SetBody(map[string]interface{}{
		"subscription_id": subscripId, "actions": map[string]interface{}{pluginName: "INSTALL"},
	}).SetResult(&installResp).Request()
	if err != nil {
		return err
	}
	logger.Infof("run subscription result [%s]", installResp.Message)
	return nil
}
