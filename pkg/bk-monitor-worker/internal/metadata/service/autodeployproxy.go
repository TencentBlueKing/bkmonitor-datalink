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
	"regexp"

	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// AutoDeployProxySvc auto deploy proxy service
type AutoDeployProxySvc struct {
}

func NewAutoDeployProxySvc() AutoDeployProxySvc {
	return AutoDeployProxySvc{}
}

// Refresh refresh plugin
func (s AutoDeployProxySvc) Refresh(pluginName string) error {
	if pluginName == "" {
		pluginName = "bk-collector"
	}
	if !cfg.GlobalIsAutoDeployCustomReportServer {
		logger.Info("auto deploy custom report server is closed, do nothing")
		return nil
	}

	pluginLatestVersion, err := s.findLatestVersion(pluginName)
	if err != nil {
		return errors.Wrapf(err, "findLatestVersion with plugin_name [%s] failed", pluginName)
	}
	logger.Infof("find [%s] version [%s] from bk_nodeman, start auto deploy", pluginName, pluginLatestVersion)
	// 云区域
	cloudAreas, err := apiservice.CMDB.SearchCloudArea()
	if err != nil {
		return errors.Wrap(err, "SearchCloudArea failed")
	}
	for _, cloudArea := range cloudAreas {
		if cloudArea.BkCloudId == 0 {
			continue
		}
		err := s.deployWithCloudId(pluginName, pluginLatestVersion, cloudArea.BkCloudId)
		if err != nil {
			logger.Errorf("deployWithCloudId with plugin_name [%s] version [%s] cloud_id [%v] failed, %v", pluginName, pluginLatestVersion, cloudArea.BkCloudId, err)
			continue
		}
	}
	// 直连区域
	err = s.deployDirectAreaProxy(pluginName, pluginLatestVersion)
	if err != nil {
		logger.Errorf("deployWithCloudId with plugin_name [%s] version [%s] failed, %v", pluginName, pluginLatestVersion, err)
	}
	return nil
}

func (s AutoDeployProxySvc) findLatestVersion(pluginName string) (string, error) {
	defaultVersion := "0.0.0"
	plugins, err := apiservice.Nodeman.PluginInfo(pluginName, "")
	if err != nil {
		return "", errors.Wrapf(err, "query PluginInfo with plugin_name [%s] failed", pluginName)
	}
	var versionStrList []string
	for _, p := range plugins {
		if p.IsReady {
			if p.Version != "" {
				versionStrList = append(versionStrList, p.Version)
			} else {
				versionStrList = append(versionStrList, defaultVersion)
			}
		}
	}
	maxVersion := getMaxVersion(defaultVersion, versionStrList)
	return maxVersion, nil
}

func (s AutoDeployProxySvc) deployWithCloudId(pluginName, version string, bkCloudId int) error {
	logger.Infof("deploy plugin_name [%s] version [%s] with cloude_id [%v]", pluginName, version, bkCloudId)
	proxyList, err := apiservice.Nodeman.GetProxies(bkCloudId)
	if err != nil {
		return errors.Wrapf(err, "GetProxies with bk_cloud_id [%v] failed", bkCloudId)
	}
	// 获取全体proxy主机列表
	var bkHostIds []int
	for _, p := range proxyList {
		if p.Status != "RUNNING" {
			logger.Warnf("proxy [%s] can not be use, it's not running", p.InnerIp)
		} else {
			bkHostIds = append(bkHostIds, p.BkHostId)
		}
	}
	if len(bkHostIds) == 0 {
		logger.Infof("bk_cloud_id [%v] has no proxy host, skip it", bkCloudId)
		return nil
	}
	return s.deployProxy(pluginName, version, bkCloudId, bkHostIds)
}

func (s AutoDeployProxySvc) deployDirectAreaProxy(pluginName, version string) error {
	if len(cfg.GlobalCustomReportDefaultProxyIp) == 0 {
		logger.Info("no proxy host in direct area, skip it")
		return nil
	}
	hosts, err := apiservice.CMDB.GetHostWithoutBiz(cfg.GlobalCustomReportDefaultProxyIp, nil)
	if err != nil {
		return errors.Wrapf(err, "GetHostWithoutBiz with ips GlobalCustomReportDefaultProxyIp [%v]", cfg.GlobalCustomReportDefaultProxyIp)
	}
	var bkHostIds []int
	for _, h := range hosts {
		if h.BkCloudId == 0 {
			bkHostIds = append(bkHostIds, h.BkHostId)
		}
	}
	return s.deployProxy(pluginName, version, 0, bkHostIds)
}

func (s AutoDeployProxySvc) deployProxy(pluginName, version string, bkCloudId int, bkHostIds []int) error {
	logger.Infof("update proxy on bk_cloud_id [%v], get host_ids [%v]", bkCloudId, bkHostIds)
	// 查询当前版本
	pluginInfoList, err := apiservice.Nodeman.PluginSearch(nil, bkHostIds, nil, nil)
	if err != nil {
		return errors.Wrapf(err, "PluginSearch for bk_host_ids [%v] failed", bkHostIds)
	}
	var pluginIps []string
	for _, i := range pluginInfoList {
		pluginIps = append(pluginIps, i.InnerIp)
	}
	logger.Infof("get plugin info from nodeman [%v]", pluginIps)
	var deployHostList []int
	for _, p := range pluginInfoList {
		var procList []nodeman.PluginSearchDataItemPluginStatus
		for _, i := range p.PluginStatus {
			if i.Name == pluginName {
				procList = append(procList, i)
			}
		}
		var proc nodeman.PluginSearchDataItemPluginStatus
		if len(procList) != 0 {
			proc = procList[0]
		}
		currentPluginVersion := s.findVersion(proc.Version)
		// 已经是最新的版本，则无需部署
		if currentPluginVersion == version {
			continue
		}
		deployHostList = append(deployHostList, p.BkHostId)
	}
	logger.Infof("get deploy host list [%v]", deployHostList)
	if len(deployHostList) == 0 {
		logger.Infof("all proxy of bk_cloud_id [%v] is already deployed", bkCloudId)
		return nil
	}
	params := map[string]interface{}{
		"plugin_params": map[string]interface{}{"name": pluginName, "version": version},
		"job_type":      "MAIN_INSTALL_PLUGIN",
		"bk_host_id":    deployHostList,
	}
	if cfg.BypassSuffixPath != "" {
		logger.Infof("[db_diff] update [%s] to version [%s] for host_id [%v]", pluginName, version, deployHostList)
	} else {
		nodemanApi, err := api.GetNodemanApi()
		if err != nil {
			return errors.Wrap(err, "GetNodemanApi failed")
		}
		var resp define.APICommonResp
		_, err = nodemanApi.PluginOperate().SetBody(params).SetResult(&resp).Request()
		if err != nil {
			return errors.Wrapf(err, "update [%s] to version [%s] for host_id [%v] failed", pluginName, version, deployHostList)
		}
		if err := resp.Err(); err != nil {
			return errors.Wrapf(err, "update [%s] to version [%s] for host_id [%v] failed", pluginName, version, deployHostList)
		}
		logger.Infof("update [%s] to version [%s] for host_id [%v], result: %v", pluginName, version, deployHostList, resp)
	}
	logger.Infof("refresh bk_cloud_id [%v] proxy finished", bkCloudId)
	return nil
}

func (s AutoDeployProxySvc) findVersion(version string) string {
	p := regexp.MustCompile(`[vV]?(\d+\.){1,5}\d+$`)
	v := p.FindString(version)
	return v
}
