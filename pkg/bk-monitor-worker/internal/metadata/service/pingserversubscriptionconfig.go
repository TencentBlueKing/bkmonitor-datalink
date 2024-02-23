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
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/pingserver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashring"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// PingServerSubscriptionConfigSvc ping server subscription config service
type PingServerSubscriptionConfigSvc struct {
	*pingserver.PingServerSubscriptionConfig
}

func NewPingServerSubscriptionConfigSvc(obj *pingserver.PingServerSubscriptionConfig) PingServerSubscriptionConfigSvc {
	return PingServerSubscriptionConfigSvc{
		PingServerSubscriptionConfig: obj,
	}
}

type hostInfo struct {
	IP           string
	IPv6         string
	BkCloudId    int
	BkBizId      int
	BkHostId     int
	BkSupplierId int
}

func (h hostInfo) IsIPV6Biz() bool {
	return slicex.IsExistItem(cfg.GlobalIPV6SupportBizList, h.BkBizId)
}

// RefreshPingConf 刷新Ping Server的ip列表配置
func (s PingServerSubscriptionConfigSvc) RefreshPingConf(pluginName string) error {
	if pluginName == "" {
		pluginName = "bkmonitorproxy"
	}
	// 1. 获取CMDB下的所有主机ip
	hosts, err := apiservice.CMDB.GetAllHost()
	if err != nil {
		return errors.Wrap(err, "GetAllHost failed")
	}
	cloudToHostsMap := make(map[int][]hostInfo)
	for _, h := range hosts {
		var ip string
		if h.IsIPV6Biz() {
			ip = h.BkHostInneripV6
		} else {
			ip = h.BkHostInnerip
		}
		if h.IgnoreMonitorByStatus() || ip == "" {
			continue
		}
		mapx.AddSliceItems(cloudToHostsMap, h.BkCloudId, hostInfo{
			IP:        ip,
			BkCloudId: h.BkCloudId,
			BkBizId:   h.BkBizId,
			BkHostId:  h.BkHostId,
		})
	}

	// 2. 获取云区域下的所有ProxyIP
	for bkCloudId, targetIps := range cloudToHostsMap {
		var targetHosts []hostInfo
		var proxiesHostIds []int
		if bkCloudId == 0 {
			if len(cfg.GlobalCustomReportDefaultProxyIp) == 0 {
				logger.Warn("custom report default proxy ip is empty")
				continue
			}
			hosts, err := apiservice.CMDB.GetHostWithoutBiz(cfg.GlobalCustomReportDefaultProxyIp, nil)
			if err != nil {
				logger.Errorf("GetHostWithoutBiz with ips GlobalCustomReportDefaultProxyIp [%v] failed, %v", cfg.GlobalCustomReportDefaultProxyIp, err)
				continue
			}
			var bkHostIds []int
			for _, h := range hosts {
				bkHostIds = append(bkHostIds, h.BkHostId)
				proxiesHostIds = append(proxiesHostIds, h.BkHostId)
			}
			relationMap, err := apiservice.CMDB.FindHostBizRelationMap(bkHostIds)
			if err != nil {
				logger.Errorf("FindHostBizRelationMap with ips bk_hos_id [%v] failed, %v", bkHostIds, err)
				continue
			}
			for _, host := range hosts {
				targetHosts = []hostInfo{{
					IP:           host.BkHostInnerip,
					BkCloudId:    0,
					BkHostId:     host.BkHostId,
					BkBizId:      relationMap[host.BkHostId],
					IPv6:         host.BkHostInneripV6,
					BkSupplierId: 0,
				}}
			}
		} else {
			proxyList, err := apiservice.Nodeman.GetProxies(bkCloudId)
			if err != nil {
				logger.Errorf("GetProxies with bk_cloud_id [%v] failed, %v", bkCloudId, err)
				continue
			}
			for _, p := range proxyList {
				if p.Status != "RUNNING" {
					logger.Warnf("proxy [%s] can not be use with pingserver, it's not running", p.InnerIp)
				} else {
					proxiesHostIds = append(proxiesHostIds, p.BkHostId)
					targetHosts = append(targetHosts, hostInfo{
						IP:           p.InnerIp,
						IPv6:         p.InnerIpv6,
						BkHostId:     p.BkHostId,
						BkCloudId:    p.BkCloudId,
						BkSupplierId: 0,
						BkBizId:      p.BkBizId,
					})
				}
			}
		}

		// 3. 根据Hash环，将同一云区域下的ip分配到不同的Proxy
		if pluginName == "bkmonitorproxy" {
			// 若是 bkmonitorproxy 则校验proxy插件状态
			proxiesHostIds, err = s.GetProxyHostIds(proxiesHostIds)
			if err != nil {
				logger.Errorf("GetProxyHostIds with bk_host_ids [%v] failed, %v", proxiesHostIds, err)
				continue
			}
		}
		if len(proxiesHostIds) == 0 {
			logger.Errorf("cloud area [%v] has no proxy node", bkCloudId)
			continue
		}
		proxiesHostIdMap := make(map[int]int)
		for _, id := range proxiesHostIds {
			proxiesHostIdMap[id] = 1
		}
		hostInfoMap := make(map[int][]map[string]interface{})
		if cfg.PingServerEnablePingAlarm {
			// 如果开启了PING服务，则按hash分配给不同的server执行
			hostRing := hashring.NewHashRing(proxiesHostIdMap, 1<<16)
			for _, h := range targetIps {
				hostId := hostRing.GetNode(h.IP)
				mapx.AddSliceItems(hostInfoMap, hostId, map[string]interface{}{
					"target_ip":       h.IP,
					"target_cloud_id": h.BkCloudId,
					"target_biz_id":   h.BkBizId,
				})
			}
		} else {
			// 如果关闭了PING服务，则清空目标Proxy上的任务iplist
			for _, id := range proxiesHostIds {
				hostInfoMap[id] = make([]map[string]interface{}, 0)
			}
		}

		// 针对直连区域做一定处理，如果关闭直连区域的PING采集，则清空目标Proxy上的任务iplist
		if bkCloudId == 0 && !cfg.PingServerEnableDirectAreaPingCollect {
			for _, id := range proxiesHostIds {
				hostInfoMap[id] = make([]map[string]interface{}, 0)
			}
		}

		// 4. 通过节点管理订阅任务将分配好的ip下发到机器
		if err := s.CreateSubscription(bkCloudId, hostInfoMap, targetHosts, pluginName); err != nil {
			logger.Errorf("CreateSubscription with bk_cloud_id [%v] proxies_ips [%v] plugin [%s] failed, %v", bkCloudId, proxiesHostIds, pluginName)
			continue
		}
	}
	return nil
}

// GetProxyHostIds 校验proxy插件状态
func (s PingServerSubscriptionConfigSvc) GetProxyHostIds(bkHostIds []int) ([]int, error) {
	if len(bkHostIds) == 0 {
		return nil, nil
	}
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return nil, errors.Wrap(err, "GetNodemanApi failed")
	}
	var resp nodeman.PluginSearchResp
	if _, err = nodemanApi.PluginSearch().SetBody(map[string]interface{}{
		"page":       1,
		"pagesize":   len(bkHostIds),
		"conditions": []interface{}{},
		"bk_host_id": bkHostIds,
	}).SetResult(&resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "PluginSearch with bk_host_id [%v] failed", bkHostIds)
	}
	var proxyHostIds []int
	var removeHostIds []int
	for _, plugin := range resp.Data.List {
		var proxyPlugins []nodeman.PluginSearchDataItemPluginStatus
		for _, ps := range plugin.PluginStatus {
			if ps.Name == "bkmonitorproxy" {
				proxyPlugins = append(proxyPlugins, ps)
			}
		}
		// 如果bkmonitorproxy插件存在，且状态为未停用，则下发子配置文件
		if len(proxyPlugins) != 0 && proxyPlugins[0].Status != "MANUAL_STOP" {
			proxyHostIds = append(proxyHostIds, plugin.BkHostId)
		} else {
			removeHostIds = append(removeHostIds, plugin.BkHostId)
		}
	}
	if len(removeHostIds) != 0 {
		logger.Infof("target_hosts [%v]: No bkmonitorproxy found or bkmonitorproxy status is MANUAL_STOP", removeHostIds)
	}
	return proxyHostIds, nil
}

func (s PingServerSubscriptionConfigSvc) CreateSubscription(bkCloudId int, items map[int][]map[string]interface{}, targetHosts []hostInfo, pluginName string) error {
	logger.Infof("update or create ping server subscription task, bk_cloud_id [%v], target_hosts [%v], plugin [%s]", bkCloudId, targetHosts, pluginName)
	db := mysql.GetDBSession().DB
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return errors.Wrap(err, "GetNodemanApi failed")
	}
	var configs []pingserver.PingServerSubscriptionConfig
	if err := pingserver.NewPingServerSubscriptionConfigQuerySet(db).BkCloudIdEq(bkCloudId).PluginNameEq(pluginName).All(&configs); err != nil {
		return errors.Wrapf(err, "query PingServerSubscriptionConfig with bk_cloud_id [%v] plugin_name [%s] failed", bkCloudId, pluginName)
	}
	var configIPs []string
	for _, c := range configs {
		if c.BkHostId == nil && c.IP != "" {
			configIPs = append(configIPs, c.IP)
		}
	}

	hosts, err := apiservice.CMDB.GetHostWithoutBiz(configIPs, []int{bkCloudId})
	if err != nil {
		return errors.Wrapf(err, "GetHostWithoutBiz with ips [%v] bk_cloud_ids [%v] failed", configIPs, bkCloudId)
	}
	ipToIdMap := make(map[string]int)
	for _, h := range hosts {
		ipToIdMap[h.BkHostInnerip] = h.BkHostId
	}
	hostConfigsMap := make(map[int]*pingserver.PingServerSubscriptionConfig)
	additionHostConfigsMap := make(map[string]*pingserver.PingServerSubscriptionConfig) // 无对应主机实例id
	for _, c := range configs {
		if c.BkHostId != nil {
			hostConfigsMap[*c.BkHostId] = &c
		} else if c.IP != "" {
			// 存量 pingserver 订阅配置如果相关ip已无对应主机实例id，则使用ip作为config键值进行管理
			hostKey, ok := ipToIdMap[c.IP]
			if !ok {
				additionHostConfigsMap[c.IP] = &c
			} else {
				hostConfigsMap[hostKey] = &c
			}
		}
	}
	for _, host := range targetHosts {
		var ip string
		var serverIP string
		if host.IsIPV6Biz() {
			ip = host.IPv6
			serverIP = "{{ cmdb_instance.host.bk_host_innerip_v6 }}"
		} else {
			ip = host.IP
			serverIP = "{{ cmdb_instance.host.bk_host_innerip }}"
		}
		scope := map[string]interface{}{
			"object_type": "HOST",
			"node_type":   "INSTANCE",
			"nodes":       []map[string]interface{}{{"bk_host_id": host.BkHostId}},
		}

		var ipToItems interface{}
		if pluginName == "bk-collector" {
			// bk-collector升级后将使用collector新版参数下发，host_id作为key
			ipToItems = map[int]interface{}{host.BkHostId: items[host.BkHostId]}
		} else {
			// 当bk-collector暂未升级时，使用proxy旧版参数下发，ip作为key
			ipToItems = map[string]interface{}{ip: items[host.BkHostId]}
		}

		subscriptionParams := map[string]interface{}{
			"scope": scope,
			"type":  "PLUGIN",
			"config": map[string]interface{}{
				"plugin_name":      pluginName,
				"plugin_version":   "latest",
				"config_templates": []map[string]interface{}{{"name": "bkmonitorproxy_ping.conf", "version": "latest"}},
			},
			"params": map[string]interface{}{
				"context": map[string]interface{}{
					"dataid":          cfg.PingServerDataid,
					"period":          models.PingServerDefaultDataReportInterval,
					"total_num":       models.PingServerDefaultExecTotalNum,
					"max_batch_size":  models.PingServerDefaultMaxBatchSize,
					"ping_size":       models.PingServerDefaultPingSize,
					"ping_timeout":    models.PingServerDefaultPingTimeout,
					"server_ip":       serverIP,
					"server_host_id":  "{{ cmdb_instance.host.bk_host_id }}",
					"server_cloud_id": host.BkCloudId,
					"ip_to_items":     ipToItems,
				},
			},
		}
		var config *pingserver.PingServerSubscriptionConfig
		if c, ok := hostConfigsMap[host.BkHostId]; ok {
			config = c
			delete(hostConfigsMap, host.BkHostId)
		}
		if config == nil {
			if c, ok := additionHostConfigsMap[ip]; ok {
				config = c
				delete(additionHostConfigsMap, ip)
			}
		}
		if config == nil {
			if c, ok := additionHostConfigsMap[host.IP]; ok {
				config = c
				delete(additionHostConfigsMap, host.IP)
			}
		}
		if config != nil && config.BkHostId == nil {
			config.BkHostId = &host.BkHostId
			_ = metrics.MysqlCount(config.TableName(), "CreateSubscription_update_bkHostId", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] update PingServerSubscriptionConfig [%v] with bk_host_id [%v]", config.SubscriptionId, config.BkHostId)
			} else {
				if err := config.Update(db, pingserver.PingServerSubscriptionConfigDBSchema.BkHostId); err != nil {
					logger.Errorf("update PingServerSubscriptionConfig [%v] with bk_host_id [%v] failed, %v", config.SubscriptionId, config.BkHostId, err)
					continue
				}
			}
		}

		if config != nil {
			logger.Infof("ping server subscription task(ip:[%s], host_id: [%v] already exists", ip, host.BkHostId)
			subscriptionParams["subscription_id"] = config.SubscriptionId
			subscriptionParams["run_immediately"] = true

			subscriptionParamsStr, err := jsonx.MarshalString(subscriptionParams)
			if err != nil {
				logger.Errorf("marshal PingServerSubscriptionConfig [%v] new config [%v] failed, %v", config.SubscriptionId, subscriptionParams, err)
				continue
			}
			equal, _ := jsonx.CompareJson(subscriptionParamsStr, config.Config)
			if !equal {
				logger.Infof("ping server subscription task [%v] config has changed, update it", config.SubscriptionId)
				_ = metrics.MysqlCount(config.TableName(), "CreateSubscription_update_config", 1)
				if cfg.BypassSuffixPath != "" {
					logger.Infof("[db_diff] UpdateSubscription with config [%s]", subscriptionParamsStr)
					logger.Infof("[db_diff] update PingServerSubscriptionConfig [%v] with config [%s]", config.SubscriptionId, subscriptionParamsStr)
				} else {
					var resp define.APICommonResp
					_, err := nodemanApi.UpdateSubscription().SetBody(subscriptionParams).SetResult(&resp).Request()
					if err != nil {
						logger.Errorf("UpdateSubscription with config [%s] faild, %v", subscriptionParamsStr, err)
						continue
					}
					logger.Infof("update ping server subscription [%v] successful, %v", config.SubscriptionId, resp.Message)
					config.Config = subscriptionParamsStr
					if err := config.Update(db, pingserver.PingServerSubscriptionConfigDBSchema.Config); err != nil {
						logger.Errorf("update PingServerSubscriptionConfig [%v] with config [%s] failed, %v", config.SubscriptionId, subscriptionParamsStr, err)
						continue
					}
					_, err = nodemanApi.RunSubscription().SetBody(map[string]interface{}{"subscription_id": config.SubscriptionId}).Request()
					if err != nil {
						logger.Errorf("RunSubscription with subscription_id [%v] failed, %v", config.SubscriptionId, err)
						continue
					}
				}
			}
		} else {
			logger.Info("ping server subscription task not exists, create it")
			subscriptionParamsStr, err := jsonx.MarshalString(subscriptionParams)
			if err != nil {
				logger.Errorf("marshal PingServerSubscriptionConfig new config [%v] failed, %v", subscriptionParams, err)
				continue
			}
			_ = metrics.MysqlCount(config.TableName(), "CreateSubscription_create", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] CreateSubscription with config [%s]", subscriptionParamsStr)
				logger.Infof("[db_diff] create PingServerSubscriptionConfig with config [%s]", subscriptionParamsStr)
			} else {
				var resp define.APICommonMapResp
				_, err = nodemanApi.CreateSubscription().SetBody(subscriptionParams).SetResult(&resp).Request()
				logger.Infof("create subscription successful, result: %s", resp.Message)
				// 创建订阅成功后，优先存储下来，不然因为其他报错会导致订阅ID丢失
				subscripId, ok := resp.Data["subscription_id"].(float64)
				if !ok {
					logger.Errorf("parse api response subscription_id error, %v", err)
					continue
				}
				newSub := pingserver.PingServerSubscriptionConfig{
					SubscriptionId: int(subscripId),
					IP:             ip,
					BkCloudId:      host.BkCloudId,
					BkHostId:       &host.BkHostId,
					Config:         subscriptionParamsStr,
					PluginName:     pluginName,
				}
				if err := newSub.Create(db); err != nil {
					logger.Errorf("create PingServerSubscriptionConfig with subscription_id [%v] IP [%s] bk_cloud_id [%v] bk_host_id [%v] config [%s] plugin_name [%s] failed, %v", newSub.SubscriptionId, newSub.IP, newSub.BkCloudId, newSub.BkHostId, newSub.Config, pluginName, err)
					continue
				}
				var installResp define.APICommonResp
				_, err = nodemanApi.RunSubscription().SetBody(map[string]interface{}{
					"subscription_id": subscripId, "actions": map[string]interface{}{pluginName: "INSTALL"},
				}).SetResult(&installResp).Request()
				if err != nil {
					logger.Errorf("RunSubscription with subscription_id [%v] action [INSTALL] failed, %v", config.SubscriptionId, err)
					continue
				}
				logger.Infof("run subscription result [%v]", installResp.Data)
			}
		}
	}

	// 停用未使用的节点
	var unusedConfig []*pingserver.PingServerSubscriptionConfig
	for _, c := range hostConfigsMap {
		unusedConfig = append(unusedConfig, c)
	}

	for _, c := range additionHostConfigsMap {
		unusedConfig = append(unusedConfig, c)
	}

	for _, config := range unusedConfig {
		var configObj map[string]interface{}
		if err := jsonx.UnmarshalString(config.Config, &configObj); err != nil {
			logger.Errorf("UnmarshalString  PingServerSubscriptionConfig [%v] config [%s] failed, %v", config.SubscriptionId, config.Config, err)
			continue
		}
		status, ok := configObj["status"].(string)
		if !ok {
			logger.Errorf("get PingServerSubscriptionConfig [%v] status failed", config.SubscriptionId)
			continue
		}
		if status == "STOP" {
			continue
		}
		_ = metrics.MysqlCount(config.TableName(), "CreateSubscription_update_status", 1)
		if cfg.BypassSuffixPath != "" {
			logger.Infof("[db_diff] SwitchSubscription to disable for subscription_id [%v]", config.SubscriptionId)
			logger.Infof("[db_diff] update PingServerSubscriptionConfig to disable for subscription_id [%v]", config.SubscriptionId)
		} else {
			_, err := nodemanApi.SwitchSubscription().SetBody(map[string]interface{}{"subscription_id": config.SubscriptionId, "action": "disable"}).Request()
			if err != nil {
				logger.Errorf("SwitchSubscription to disable for subscription_id [%v] failed, %v", config.SubscriptionId, err)
				continue
			}
			configObj["status"] = "STOP"
			newConfigStr, err := jsonx.MarshalString(configObj)
			if err != nil {
				logger.Errorf("marsharl new config [%v] for PingServerSubscriptionConfig [%v] failed, %v", configObj, config.SubscriptionId, err)
				continue
			}
			config.Config = newConfigStr
			if err := config.Update(db, pingserver.PingServerSubscriptionConfigDBSchema.Config); err != nil {
				logger.Errorf("update PingServerSubscriptionConfig with config [%s] failed, %v", newConfigStr, err)
				continue
			}
		}
	}
	return nil
}
