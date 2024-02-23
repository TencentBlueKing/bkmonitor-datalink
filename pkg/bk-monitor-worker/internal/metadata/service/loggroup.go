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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// LogGroupSvc log group service
type LogGroupSvc struct {
	*customreport.LogGroup
	pluginName                  string
	pluginLogConfigTemplateName string
}

func NewLogGroupSvc(obj *customreport.LogGroup) LogGroupSvc {
	return LogGroupSvc{
		LogGroup:                    obj,
		pluginName:                  "bk-collector",
		pluginLogConfigTemplateName: "bk-collector-application.conf",
	}
}

// Refresh refresh custom log config to bk collector
func (s *LogGroupSvc) Refresh() error {
	if s.LogGroup == nil {
		return errors.New("LogGroup obj can not be nil")
	}
	logger.Infof("start to refresh LogGroup [%v(%s)]", s.LogGroupID, s.LogGroupName)
	hosts, err := s.getTargetHosts()
	if err != nil {
		return errors.Wrap(err, "getTargetHosts failed")
	}
	logConfig := s.getLogConfig()

	return s.deploy(logConfig, hosts)
}

// 查询云区域下所有的Proxy机器列表
func (s *LogGroupSvc) getTargetHosts() ([]map[string]interface{}, error) {
	var targetHosts []map[string]interface{}
	for _, ip := range cfg.GlobalCustomReportDefaultProxyIp {
		targetHosts = append(targetHosts, map[string]interface{}{"ip": ip, "bk_cloud_id": 0, "bk_supplier_id": 0})
	}
	cloudInfos, err := apiservice.CMDB.SearchCloudArea()
	if err != nil {
		return nil, errors.Wrap(err, "SearchCloudArea failed")
	}
	for _, cloud := range cloudInfos {
		if cloud.BkCloudId == 0 {
			continue
		}
		proxies, err := apiservice.Nodeman.GetProxies(cloud.BkCloudId)
		if err != nil {
			return nil, errors.Wrapf(err, "GetProxies with bk_cloud_id [%v] failed", cloud.BkCloudId)
		}
		for _, p := range proxies {
			if p.Status != "RUNNING" {
				logger.Warnf("proxy [%s] can not be use with bk-collector, it's not running", p.InnerIp)
			} else {
				targetHosts = append(targetHosts, map[string]interface{}{"ip": p.InnerIp, "bk_cloud_id": p.BkCloudId, "bk_supplier_id": 0})
			}
		}
	}
	return targetHosts, nil
}

// Get Log Config
func (s *LogGroupSvc) getLogConfig() map[string]interface{} {
	bkDataToken := cipher.TransformDataidToToken(-1, -1, int(s.BkDataID), s.BkBizID, s.LogGroupName)
	return map[string]interface{}{
		"bk_data_token": bkDataToken,
		"bk_biz_id":     s.BkBizID,
		"bk_app_name":   s.LogGroupName,
		"qps_config":    s.getQPSConfig(),
	}
}

func (s *LogGroupSvc) getQPSConfig() map[string]interface{} {
	qps := models.LogReportMaxQPS
	if s.MaxRate > 0 {
		qps = s.MaxRate
	}
	return map[string]interface{}{
		"name":        "rate_limiter/token_bucket",
		"type":        "token_bucket",
		"bk_app_name": s.LogGroupName,
		"qps_config":  qps,
	}
}

// Deploy Custom Log Config
func (s *LogGroupSvc) deploy(platformConfig map[string]interface{}, hosts []map[string]interface{}) error {
	// Build Subscription Params
	var nodes []map[string]interface{}
	for _, h := range hosts {
		// TODO 这里不确定是不是原逻辑有问题，是否要传bk_host_id?
		nodes = append(nodes, map[string]interface{}{"bk_host_id": h})
	}
	scope := map[string]interface{}{
		"object_type": "HOST",
		"node_type":   "INSTANCE",
		"nodes":       nodes,
	}
	subscriptionParams := map[string]interface{}{
		"scope": scope,
		"steps": []map[string]interface{}{
			{
				"id":   s.pluginName,
				"type": "PLUGIN",
				"config": map[string]interface{}{
					"plugin_name":    s.pluginName,
					"plugin_version": "latest",
					"config_templates": []map[string]interface{}{
						{"name": s.pluginLogConfigTemplateName, "version": "latest"},
					},
				},
				"params": map[string]interface{}{"context": platformConfig},
			},
		},
	}
	db := mysql.GetDBSession().DB
	var subscripList []customreport.LogSubscriptionConfig
	qs := customreport.NewLogSubscriptionConfigQuerySet(db).BkBizIdEq(s.BkBizID).LogNameEq(s.LogGroupName)
	if err := qs.All(&subscripList); err != nil {
		return errors.Wrapf(err, "query LogSubscriptionConfig with bk_biz_id [%v] log_name [%s] failed", s.BkBizID, s.LogGroupName)
	}
	// 存在则更新
	if len(subscripList) != 0 {
		logger.Infof("custom log config subscription task already exists")
		subscrip := subscripList[0]
		subscriptionParams["subscription_id"] = subscrip.SubscriptionId
		subscriptionParams["run_immediately"] = true
		newConfig, err := jsonx.MarshalString(subscriptionParams)
		if err != nil {
			return errors.Wrapf(err, "marshal newConfig [%v] failed", subscriptionParams)
		}
		// 对比新老配置
		equal, _ := jsonx.CompareJson(subscrip.Config, newConfig)
		if !equal {
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] custom log subscription task config has changed, old [%s] new [%s]", subscrip.Config, newConfig)
				_ = metrics.MysqlCount(subscrip.TableName(), "deploy_update_config", float64(len(subscripList)))
				return nil
			}
			logger.Infof("custom log subscription task config has changed, update it")
			resp, err := apiservice.Nodeman.UpdateSubscription(subscriptionParams)
			if err != nil {
				return err
			}
			logger.Infof("update custom log config subscription successful, result [%s]", resp.Data)

			if err := qs.GetUpdater().SetConfig(newConfig).Update(); err != nil {
				return errors.Wrapf(err, "update subscrips bk_biz_id [%v] log_name [%v] with config [%s] failed", subscrip.BkBizId, subscrip.LogName, newConfig)
			}
		}
		return nil
	}
	// 不存在则创建订阅
	logger.Info("custom log config subscription task not exists, create it")
	newConfig, err := jsonx.MarshalString(subscriptionParams)
	if err != nil {
		return err
	}
	if cfg.BypassSuffixPath != "" {
		logger.Infof("[db_diff]create LogSubscriptionConfig with bk_biz_id [%v] log_name [%v] config [%s]", s.BkBizID, s.LogGroupName, newConfig)
		_ = metrics.MysqlCount(customreport.LogSubscriptionConfig{}.TableName(), "deploy_create", 1)
		return nil
	}
	resp, err := apiservice.Nodeman.CreateSubscription(subscriptionParams)
	if err != nil {
		return err
	}
	logger.Infof("create custom log config  subscription successful, result: %v", resp.Data)
	// 创建订阅成功后，优先存储下来，不然因为其他报错会导致订阅ID丢失
	subscripId, ok := resp.Data["subscription_id"].(float64)
	if !ok {
		return errors.New("parse api response subscription_id error")
	}
	newSub := customreport.LogSubscriptionConfig{
		BkBizId:        s.BkBizID,
		SubscriptionId: int(subscripId),
		LogName:        s.LogGroupName,
		Config:         newConfig,
	}
	if err := newSub.Create(db); err != nil {
		return errors.Wrapf(err, "create LogSubscriptionConfig with bk_biz_id [%v] subscription_id [%v] log_name [%v] config [%s] failed", s.BkBizID, subscripId, s.LogGroupName, newConfig)
	}
	installResp, err := apiservice.Nodeman.RunSubscription(map[string]interface{}{
		"subscription_id": subscripId, "actions": map[string]interface{}{s.pluginName: "INSTALL"},
	})
	if err != nil {
		return errors.Wrapf(err, "RunSubscription with subscription_id [%v] actions [{%s: %s}] failed", subscripId, s.pluginName, "INSTALL")
	}
	logger.Infof("run custom log config subscription result [%v]", installResp.Data)
	return nil
}
