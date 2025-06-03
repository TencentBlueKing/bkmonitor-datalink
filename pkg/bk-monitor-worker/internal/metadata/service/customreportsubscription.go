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

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
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

func (s CustomReportSubscriptionSvc) RefreshCustomReport2Config(bkBizId *int) error {
	var BkBizIdStr string
	if bkBizId == nil {
		BkBizIdStr = "nil"
	} else {
		BkBizIdStr = strconv.Itoa(*bkBizId)
	}
	// 判定节点管理是否上传支持v2新配置模版的bk-collector版本0.16.1061
	defaultVersion := "0.0.0"
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return err
	}
	var resp nodeman.PluginInfoResp
	_, err = nodemanApi.PluginInfo().SetQueryParams(map[string]string{"name": "bk-collector"}).SetResult(&resp).Request()
	if err != nil {
		return errors.Wrap(err, "get PluginInfo with name [bk-collector] failed")
	}
	var versionStrList []string
	for _, plugin := range resp.Data {
		if plugin.IsReady {
			if plugin.Version == "" {
				versionStrList = append(versionStrList, defaultVersion)
			} else {
				versionStrList = append(versionStrList, plugin.Version)
			}
		}
	}
	maxVersion := getMaxVersion(defaultVersion, versionStrList)

	if compareVersion(maxVersion, models.RecommendedBkCollectorVersion) > 0 {
		if err := NewCustomReportSubscriptionSvc(nil).RefreshCollectorCustomConf(bkBizId, "bk-collector", "add"); err != nil {
			return errors.Wrapf(err, "RefreshCollectorCustomConf with bk_biz_id [%s] plugin_name [bk-collector] op_type [add] failed", BkBizIdStr)
		}
	} else {
		logger.Infof("bk-collector version [%s] lower than supported version %s, stop refresh bk-collector config", maxVersion, models.RecommendedBkCollectorVersion)
	}
	// bkmonitorproxy全量更新
	if err := NewCustomReportSubscriptionSvc(nil).RefreshCollectorCustomConf(nil, "bkmonitorproxy", "add"); err != nil {
		return errors.Wrapf(err, "RefreshCollectorCustomConf with bk_biz_id [nil] plugin_name [bkmonitorproxy] op_type [add] failed")
	}
	return nil
}

// RefreshCollectorCustomConf 指定业务ID更新，或者更新全量业务
func (s CustomReportSubscriptionSvc) RefreshCollectorCustomConf(bkBizId *int, pluginName, opType string) error {
	if opType == "" {
		opType = "add"
	}
	if pluginName == "" {
		pluginName = "bkmonitorproxy"
	}
	var BkBizIdStr string
	if bkBizId == nil {
		BkBizIdStr = "nil"
	} else {
		BkBizIdStr = strconv.Itoa(*bkBizId)
	}
	logger.Infof("refresh custom report config to proxy on bk_biz_id [%s]", BkBizIdStr)

	customEventConfig, err := s.GetCustomConfig(bkBizId, "event", pluginName)
	if err != nil {
		return errors.Wrapf(err, "GetCustomConfig with bk_biz_id [%s] data_type [event] plugin_name [%s] failed", BkBizIdStr, pluginName)
	}
	logger.Infof("get custom event config success, bk_biz_id [%v], len(config) [%v]", bkBizId, len(customEventConfig))
	customTSConfig, err := s.GetCustomConfig(bkBizId, "time_series", pluginName)
	if err != nil {
		return errors.Wrapf(err, "GetCustomTSConfig with bk_biz_id [%s] data_type [time_series] plugin_name [%s] failed", BkBizIdStr, pluginName)
	}
	logger.Infof("get custom time_series config success, bk_biz_id [%v], len(config) [%v]", bkBizId, len(customTSConfig))

	// 合并配置信息
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
	// todo: tenant
	cmdbApi, err := api.GetCmdbApi(tenant.DefaultTenantId)
	if err != nil {
		return errors.Wrap(err, "GetCmdbApi failed")
	}
	var bizResp cmdb.SearchBusinessResp
	if _, err := cmdbApi.SearchBusiness().SetPathParams(map[string]string{"bk_supplier_account": "0"}).SetResult(&bizResp).Request(); err != nil {
		return errors.Wrap(err, "SearchBusinessResp failed")
	}
	allBizIdSet := mapset.NewSet[int]()
	for _, info := range bizResp.Data.Info {
		allBizIdSet.Add(info.BkBizId)
	}
	var bizIdToProxy = make(map[int][]cmdb.ListBizHostsTopoDataInfo)
	bizIter := allBizIdSet.Iterator()
	defer bizIter.Stop()
	for bizId := range bizIter.C {
		if !isAllBizRefresh && *bkBizId != bizId {
			// 如果仅仅是只刷新一个业务，则跳过其他业务的proxy获取
			continue
		}
		nodemanApi, err := api.GetNodemanApi()
		if err != nil {
			return errors.Wrap(err, "GetNodemanApi failed")
		}
		var proxiesResp nodeman.GetProxiesResp
		_, err = nodemanApi.GetProxiesByBiz().SetQueryParams(map[string]string{"bk_biz_id": strconv.Itoa(bizId)}).SetResult(&proxiesResp).Request()
		if err != nil {
			return errors.Wrapf(err, "GetProxiesByBiz with bk_biz_id [%d] failed", bizId)
		}
		proxyBizIdSet := mapset.NewSet[int]()
		for _, proxy := range proxiesResp.Data {
			proxyBizIdSet.Add(proxy.BkBizId)
		}
		var proxyBizIdList = proxyBizIdSet.ToSlice()
		var proxyHostList []cmdb.ListBizHostsTopoDataInfo
		for _, proxyBizId := range proxyBizIdList {
			var params []apiservice.GetHostByIpParams
			for _, proxy := range proxiesResp.Data {
				if proxy.BkBizId == proxyBizId {
					params = append(params, apiservice.GetHostByIpParams{
						Ip:        proxy.InnerIp,
						BkCloudId: proxy.BkCloudId,
					})
				}
			}
			hostInfos, err := apiservice.CMDB.GetHostByIp(params, proxyBizId)
			if err != nil {
				return err
			}
			if len(hostInfos) != 0 {
				proxyHostList = append(proxyHostList, hostInfos...)
			}
		}
		for _, host := range proxyHostList {
			if ls, ok := bizIdToProxy[bizId]; ok {
				bizIdToProxy[bizId] = append(ls, host)
			} else {
				bizIdToProxy[bizId] = []cmdb.ListBizHostsTopoDataInfo{host}
			}
		}

	}

	for bizId, items := range dictItems {
		if !allBizIdSet.Contains(bizId) && bizId > 0 {
			// 如果cmdb不存在这个业务，那么需要跳过这个业务的下发
			logger.Infof("biz_id [%d] does not exists in cmdb", bizId)
			continue
		}
		if !isAllBizRefresh && *bkBizId != bizId {
			// 如果仅仅是只刷新一个业务，则跳过其他业务的下发
			continue
		}
		// 从节点管理查询到biz_id下的Proxy机器
		hostList := bizIdToProxy[bizId]
		if len(hostList) == 0 {
			logger.Warnf("Update custom report config to biz_id [%d] error, No proxy found", bizId)
			continue
		}
		var bkHostIdList []int
		for _, host := range hostList {
			bkHostIdList = append(bkHostIdList, host.Host.BkHostId)
		}
		// 通过节点管理下发配置
		err := s.CreateSubscription(bizId, items, bkHostIdList, pluginName, opType)
		if err != nil {
			return errors.Wrapf(err, "CreateSubscription with bk_biz_id [%d] items [%v] bk_host_id_list [%v] plugin_name [%s] op_type [%s] failed, %v", bizId, items, bkHostIdList, pluginName, opType, err)
		}
	}
	// 通过节点管理下发直连区域配置，下发全部bk_data_id
	var allItems []map[string]interface{}
	for _, items := range dictItems {
		allItems = append(allItems, items...)
	}
	proxyIps := cfg.GlobalCustomReportDefaultProxyIp
	if len(proxyIps) == 0 {
		logger.Warn("update custom report config to default cloud area failed, The default cloud area is not deployed")
		return nil
	}
	hosts, err := apiservice.CMDB.GetHostWithoutBiz(proxyIps, nil)
	if err != nil {
		return errors.Wrapf(err, "GetHostWithoutBiz with host_ips [%v] failed", proxyIps)
	}
	if len(hosts) == 0 {
		logger.Warn("update custom report config to default cloud area failed, not found host from cmdb")
		return nil
	}
	var proxyHostIds []int
	for _, h := range hosts {
		proxyHostIds = append(proxyHostIds, h.BkHostId)
	}
	if err := s.CreateSubscription(0, allItems, proxyHostIds, pluginName, opType); err != nil {
		return errors.Wrapf(err, "CreateSubscription with bk_biz_id [0] items [%v] bk_host_id [%d] plugin_name [%s] op_type [%s]", allItems, proxyHostIds, pluginName, opType)
	}
	return nil
}

// GetCustomConfig 获取自定义上报配置的数据
func (s CustomReportSubscriptionSvc) GetCustomConfig(bkBizId *int, dataType, pluginName string) (map[int][]map[string]interface{}, error) {
	if dataType == "" {
		dataType = "event"
	}
	var BkBizIdStr string
	if bkBizId == nil {
		BkBizIdStr = "nil"
	} else {
		BkBizIdStr = strconv.Itoa(*bkBizId)
	}
	logger.Infof("get custom event config, bk_biz_id [%s]", BkBizIdStr)
	// 从数据库查询到bk_biz_id到自定义上报配置的数据
	db := mysql.GetDBSession().DB
	var bkDataIdList []uint
	var groups []customreport.CustomGroupBase
	if dataType == "event" {
		eventGroupQS := customreport.NewEventGroupQuerySet(db).IsEnableEq(true).IsDeleteEq(false)
		if bkBizId != nil {
			eventGroupQS = eventGroupQS.BkBizIDEq(*bkBizId)
		}
		var eventGroupList []customreport.EventGroup
		if err := eventGroupQS.All(&eventGroupList); err != nil {
			return nil, errors.Wrapf(err, "query EventGroup failed")
		}
		if len(eventGroupList) == 0 {
			return nil, nil
		}
		for _, eg := range eventGroupList {
			bkDataIdList = append(bkDataIdList, eg.BkDataID)
			groups = append(groups, eg.CustomGroupBase)
		}
	} else {
		tsGroupQS := customreport.NewTimeSeriesGroupQuerySet(db).IsEnableEq(true).IsDeleteEq(false)
		if bkBizId != nil {
			tsGroupQS = tsGroupQS.BkBizIDEq(*bkBizId)
		}
		var tsGroupList []customreport.TimeSeriesGroup
		if err := tsGroupQS.All(&tsGroupList); err != nil {
			return nil, errors.Wrapf(err, "query TimeSeriesGroup failed")
		}
		if len(tsGroupList) == 0 {
			return nil, nil
		}
		for _, ts := range tsGroupList {
			bkDataIdList = append(bkDataIdList, ts.BkDataID)
			groups = append(groups, ts.CustomGroupBase)
		}
	}
	// 查询对应datasource信息
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).BkDataIdIn(bkDataIdList...).All(&dsList); err != nil {
		return nil, errors.Wrapf(err, "query DataSource with bk_data_id [%v] failed", bkDataIdList)
	}
	if len(dsList) == 0 {
		logger.Info("no event report config in database")
		return nil, nil
	}
	var dsMap = make(map[uint]*resulttable.DataSource)
	for _, ds := range dsList {
		dsMap[ds.BkDataId] = &ds
	}

	subConfigMap := map[string]string{
		"json":       "bk-collector-report-v2.conf",
		"prometheus": "bk-collector-application.conf",
	}
	var result = make(map[int][]map[string]interface{})
	for _, gp := range groups {
		ds, ok := dsMap[gp.BkDataID]
		if !ok {
			continue
		}
		maxRate := gp.MaxRate
		if maxRate < 0 {
			maxRate = models.MaxDataIdThroughPut
		}
		var dataIdConfig = map[string]interface{}{
			"dataid":                 gp.BkDataID,
			"datatype":               dataType,
			"version":                "v2",
			"access_token":           ds.Token,
			"max_rate":               maxRate,
			"max_future_time_offset": models.MaxFutureTimeOffset,
		}

		if pluginName == "bk-collector" {
			dataIdConfig = map[string]interface{}{}
			protocol, err := s.getProtocol(gp.BkDataID)
			if err != nil {
				logger.Errorf("getProtocol with bk_data_id [%d] failed, %v", gp.BkDataID, err)
				continue
			}
			subConfigName := subConfigMap[protocol]
			dataIdConfig["sub_config_name"] = subConfigName
			// 根据格式决定使用那种配置
			if protocol == "json" {
				// json格式: bk-collector-report-v2.conf
				dataIdConfig["bk_data_token"] = ds.Token
				dataIdConfig["bk_data_id"] = gp.BkDataID
				dataIdConfig["token_config"] = map[string]interface{}{
					"name":         "token_checker/proxy",
					"proxy_dataid": gp.BkDataID,
					"proxy_token":  ds.Token,
				}
				dataIdConfig["qps_config"] = map[string]interface{}{
					"name": "rate_limiter/token_bucket",
					"type": "token_bucket",
					"qps":  maxRate,
				}
				dataIdConfig["validator_config"] = map[string]interface{}{
					"name":                   "proxy_validator/common",
					"type":                   dataType,
					"version":                "v2",
					"max_future_time_offset": models.MaxFutureTimeOffset,
				}
			} else {
				// prometheus格式: bk-collector-application.conf
				dataIdConfig["bk_data_token"] = cipher.TransformDataidToToken(int(gp.BkDataID), -1, -1, -1, "")
				dataIdConfig["bk_biz_id"] = gp.BkBizID
				dataIdConfig["bk_data_id"] = gp.BkDataID
				dataIdConfig["bk_app_name"] = "prometheus_report"
				dataIdConfig["qps_config"] = map[string]interface{}{
					"name": "rate_limiter/token_bucket",
					"type": "token_bucket",
					"qps":  maxRate,
				}
			}

		}
		if configs, ok := result[gp.BkBizID]; ok {
			result[gp.BkBizID] = append(configs, dataIdConfig)
		} else {
			result[gp.BkBizID] = []map[string]interface{}{dataIdConfig}
		}
	}
	return result, nil
}

func (CustomReportSubscriptionSvc) getProtocol(bkDataId uint) (string, error) {
	db := mysql.GetDBSession().DB
	var ts customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(db).BkDataIDEq(bkDataId).One(&ts); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return "json", nil
		}
		return "", errors.Wrapf(err, "query TimeSeriesGroup with bk_data_id [%d] failed", bkDataId)
	}

	tsDetail, err := apiservice.Metadata.CustomTimeSeriesDetail(ts.BkBizID, ts.TimeSeriesGroupID, true)
	if err != nil {
		return "", errors.Wrapf(err, "get CustomTimeSeriesDetail with bk_biz_id [%d] ts_group_id [%d] failed", ts.BkBizID, ts.TimeSeriesGroupID)
	}
	if tsDetail.Protocol != "" {
		return tsDetail.Protocol, nil
	}
	return "json", nil
}

// CreateSubscription 创建订阅
func (s CustomReportSubscriptionSvc) CreateSubscription(bkBizId int, items []map[string]interface{}, bkHostIds []int, pluginName, opType string) error {
	if opType == "" {
		opType = "add"
	}
	logger.Infof("update or create subscription task, bk_biz_id [%d], target_hosts [%v], plugin [%s]", bkBizId, bkHostIds, pluginName)
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
		for _, i := range items {
			delete(i, "sub_config_name")
		}
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
		if err := s.CreateOrUpdateConfig(subscriptionParams, bkBizId, "bkmonitorproxy", 0); err != nil {
			logger.Errorf("CreateOrUpdateConfig with subscription_params [%v] bk_biz_id [%d] plugin_name [bkmonitorproxy] bk_data_id [0] failed, %v", subscriptionParams, bkBizId, err)
			return nil
		}
	}
	for _, item := range items {
		//从item中取出subConfigName
		subConfigName, ok := item["sub_config_name"].(string)
		if !ok {
			// bk-collector 默认自定义事件，和json的自定义指标使用bk-collector-report-v2.conf
			subConfigName = "bk-collector-report-v2.conf"
		}
		delete(item, "sub_config_name")

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
							map[string]interface{}{"name": subConfigName, "version": "latest"},
						},
					},
					"params": map[string]interface{}{
						"context": context,
					},
				},
			},
		}
		bkDataId, ok := item["bk_data_id"].(uint)
		if !ok {
			return errors.New("get bk_data_id from item failed")
		}
		if err := s.CreateOrUpdateConfig(subscriptionParams, bkBizId, pluginName, bkDataId); err != nil {
			logger.Errorf("CreateOrUpdateConfig with subscription_params [%v] bk_biz_id [%d] plugin_name [%s] bk_data_id [%d] failed, %v", subscriptionParams, bkBizId, pluginName, bkDataId, err)
			continue
		}
	}
	return nil
}

// CreateOrUpdateConfig 创建或更新订阅
func (s CustomReportSubscriptionSvc) CreateOrUpdateConfig(params map[string]interface{}, bkBizId int, pluginName string, bkDataId uint) error {
	db := mysql.GetDBSession().DB
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return errors.Wrapf(err, "GetNodemanApi failed")
	}
	// 若订阅存在则判定是否更新，若不存在则创建
	// 使用proxy下发bk_data_id为默认值0，一个业务下的多个data_id对应一个订阅
	var subscripList []customreport.CustomReportSubscription
	qs := customreport.NewCustomReportSubscriptionQuerySet(db).BkBizIdEq(bkBizId).BkDataIDEq(bkDataId)
	if err := qs.All(&subscripList); err != nil {
		return errors.Wrapf(err, "query CustomReportSubscription with bk_biz_id [%d] bk_data_id [%d] failed", bkBizId, bkDataId)
	}
	// 存在则更新
	if len(subscripList) != 0 {
		logger.Infof("subscription task already exists")
		subscrip := subscripList[0]
		params["subscription_id"] = subscrip.SubscriptionId
		params["run_immediately"] = true
		var subscripConfig map[string]interface{}
		if err := jsonx.UnmarshalString(subscrip.Config, &subscripConfig); err != nil {
			return err
		}
		scope, ok := subscripConfig["scope"].(map[string]interface{})
		if !ok {
			return errors.New("subscription config parse error")
		}
		var oldNodes []interface{}
		oldNodes, ok = scope["nodes"].([]interface{})
		if !ok {
			oldNodes = nil
		}
		// bkmonitorproxy原订阅scope配置为空则不再管理该订阅
		if pluginName == "bkmonitorproxy" && len(oldNodes) == 0 {
			logger.Infof("[bkmonitorproxy] target_hosts is None, don't need to update subscription task.")
			return nil
		}
		newConfig, err := jsonx.MarshalString(params)
		if err != nil {
			return errors.Wrapf(err, "marshal newConfig [%v] failed", params)
		}
		// 对比新老配置
		equal, _ := jsonx.CompareJson(subscrip.Config, newConfig)
		if !equal {
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_custom_report_2_node_man") {
				paramStr, _ := jsonx.MarshalString(params)
				logger.Info(diffutil.BuildLogStr("refresh_custom_report_2_node_man", diffutil.OperatorTypeAPIPost, diffutil.NewStringBody(paramStr), ""))

				var ids []uint
				for _, s := range subscripList {
					ids = append(ids, s.ID)
				}
				metrics.MysqlCount(subscrip.TableName(), "CreateOrUpdateConfig_update_config", float64(len(subscripList)))
				logger.Info(diffutil.BuildLogStr("refresh_custom_report_2_node_man", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(subscrip.TableName(), map[string]interface{}{
					customreport.CustomReportSubscriptionDBSchema.SubscriptionId.String(): ids,
					customreport.CustomReportSubscriptionDBSchema.Config.String():         newConfig,
				}), ""))
				return nil
			}
			logger.Infof("subscription task config has changed, update it")
			var resp define.APICommonResp
			_, err = nodemanApi.UpdateSubscription().SetBody(params).SetResult(&resp).Request()
			if err != nil {
				return errors.Wrapf(err, "UpdateSubscription with body [%v] failed", params)
			}
			logger.Infof("update subscription successful, result [%s]", resp.Data)

			if err := qs.GetUpdater().SetConfig(newConfig).Update(); err != nil {
				return errors.Wrapf(err, "update subscrips bk_biz_id [%d] bk_data_id [%d] with config [%s] failed", subscrip.BkBizId, bkDataId, newConfig)
			}
		}
		return nil
	}
	// 不存在则创建订阅
	logger.Info("subscription task not exists, create it")
	newConfig, err := jsonx.MarshalString(params)
	if err != nil {
		return err
	}
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_custom_report_2_node_man") {
		paramStr, _ := jsonx.MarshalString(params)
		logger.Info(diffutil.BuildLogStr("refresh_custom_report_2_node_man", diffutil.OperatorTypeAPIPost, diffutil.NewStringBody(paramStr), ""))
		metrics.MysqlCount(customreport.CustomReportSubscription{}.TableName(), "CreateOrUpdateConfig_create", 1)
		logger.Info(diffutil.BuildLogStr("refresh_custom_report_2_node_man", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(customreport.CustomReportSubscription{}.TableName(), map[string]interface{}{
			customreport.CustomReportSubscriptionDBSchema.BkBizId.String():        bkBizId,
			customreport.CustomReportSubscriptionDBSchema.BkDataID.String():       bkDataId,
			customreport.CustomReportSubscriptionDBSchema.Config.String():         newConfig,
			customreport.CustomReportSubscriptionDBSchema.SubscriptionId.String(): 0,
		}), ""))
		return nil
	}
	var resp define.APICommonMapResp
	_, err = nodemanApi.CreateSubscription().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return errors.Wrapf(err, "CreateSubscription with body [%v] failed", params)
	}
	logger.Infof("create subscription successful, result: %v", resp.Data)
	// 创建订阅成功后，优先存储下来，不然因为其他报错会导致订阅ID丢失
	subscripId, ok := resp.Data["subscription_id"].(float64)
	if !ok {
		return errors.New("parse api response subscription_id error")
	}
	newSub := customreport.CustomReportSubscription{
		BkBizId:        bkBizId,
		SubscriptionId: int(subscripId),
		BkDataID:       bkDataId,
		Config:         newConfig,
	}
	if err := newSub.Create(db); err != nil {
		return errors.Wrapf(err, "create CustomReportSubscription with bk_biz_id [%d] subscription_id [%v] bk_data_id [%d] config [%s] failed", bkBizId, subscripId, bkDataId, newConfig)
	}
	var installResp define.APICommonResp
	_, err = nodemanApi.RunSubscription().SetBody(map[string]interface{}{
		"subscription_id": subscripId, "actions": map[string]interface{}{pluginName: "INSTALL"},
	}).SetResult(&installResp).Request()
	if err != nil {
		return errors.Wrapf(err, "RunSubscription with subscription_id [%v] actions [{%s: %s}] failed", subscripId, pluginName, "INSTALL")
	}
	logger.Infof("run subscription result [%v]", installResp.Data)
	return nil
}
