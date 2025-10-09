// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"fmt"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/kubernetesd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func serviceMonitorID(obj *promv1.ServiceMonitor) string {
	return fmt.Sprintf("%s/%s", obj.Namespace, obj.Name)
}

func (c *Operator) handleServiceMonitorAdd(obj any) {
	serviceMonitor, ok := obj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", obj)
		return
	}

	// 新增的 servicemonitor 命中黑名单则流程终止
	if ifRejectServiceMonitor(serviceMonitor) {
		logger.Infof("add action match blacklist rules, serviceMonitor=%s", serviceMonitorID(serviceMonitor))
		return
	}

	discovers := c.createServiceMonitorDiscovers(serviceMonitor)
	for _, dis := range discovers {
		if err := c.addOrUpdateDiscover(dis); err != nil {
			logger.Errorf("add or update serviceMonitor discover %s failed: %s", dis, err)
		}
	}
}

func (c *Operator) handleServiceMonitorUpdate(oldObj any, newObj any) {
	old, ok := oldObj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", oldObj)
		return
	}
	cur, ok := newObj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", newObj)
		return
	}

	if old.ResourceVersion == cur.ResourceVersion {
		logger.Debugf("serviceMonitor '%s' does not change", serviceMonitorID(old))
		return
	}

	// 对于更新的 servicemonitor 如果新的 spec 命中黑名单 则需要将原有的 servicemonitor 移除
	if ifRejectServiceMonitor(cur) {
		logger.Infof("update action match blacklist rules, serviceMonitor=%s", serviceMonitorID(cur))
		for _, name := range c.getServiceMonitorDiscoversName(cur) {
			c.deleteDiscoverByName(name)
		}
		return
	}

	for _, name := range c.getServiceMonitorDiscoversName(old) {
		c.deleteDiscoverByName(name)
	}
	for _, dis := range c.createServiceMonitorDiscovers(cur) {
		if err := c.addOrUpdateDiscover(dis); err != nil {
			logger.Errorf("add or update serviceMonitor discover %s failed: %s", dis, err)
		}
	}
}

func (c *Operator) handleServiceMonitorDelete(obj any) {
	serviceMonitor, ok := obj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", obj)
		return
	}

	for _, name := range c.getServiceMonitorDiscoversName(serviceMonitor) {
		c.deleteDiscoverByName(name)
	}
}

func (c *Operator) getServiceMonitorDiscoversName(serviceMonitor *promv1.ServiceMonitor) []string {
	var names []string
	for index := range serviceMonitor.Spec.Endpoints {
		monitorMeta := define.MonitorMeta{
			Name:      serviceMonitor.Name,
			Kind:      monitorKindServiceMonitor,
			Namespace: serviceMonitor.Namespace,
			Index:     index,
		}
		names = append(names, monitorMeta.ID())
	}
	return names
}

func (c *Operator) createServiceMonitorDiscovers(serviceMonitor *promv1.ServiceMonitor) []discover.Discover {
	var (
		namespaces []string
		discovers  []discover.Discover
	)

	systemResource := feature.IfSystemResource(serviceMonitor.Annotations)
	meta := define.MonitorMeta{
		Name:      serviceMonitor.Name,
		Kind:      monitorKindServiceMonitor,
		Namespace: serviceMonitor.Namespace,
	}
	dataID, err := c.pickMonitorDataID(meta, serviceMonitor.Annotations)
	if err != nil {
		logger.Errorf("servicemonitor (%+v) no dataid matched", meta)
		return discovers
	}
	specLabels := dataID.Spec.Labels

	if serviceMonitor.Spec.NamespaceSelector.Any {
		namespaces = []string{}
	} else if len(serviceMonitor.Spec.NamespaceSelector.MatchNames) == 0 {
		namespaces = []string{serviceMonitor.Namespace}
	} else {
		namespaces = serviceMonitor.Spec.NamespaceSelector.MatchNames
	}

	logger.Infof("found new serviceMonitor '%s'", serviceMonitorID(serviceMonitor))
	for index, endpoint := range serviceMonitor.Spec.Endpoints {
		if endpoint.Path == "" {
			endpoint.Path = "/metrics"
		}
		if endpoint.Scheme == "" {
			endpoint.Scheme = "http"
		}

		relabels := getServiceMonitorRelabels(serviceMonitor, &endpoint)
		resultLabels, err := yamlToRelabels(relabels)
		if err != nil {
			logger.Errorf("failed to convert relabels, err: %s", err)
			continue
		}

		metricRelabelings := make([]yaml.MapSlice, 0)
		if len(endpoint.MetricRelabelConfigs) != 0 {
			for _, cfg := range endpoint.MetricRelabelConfigs {
				relabeling := generatePromv1RelabelConfig(cfg)
				metricRelabelings = append(metricRelabelings, relabeling)
			}
		}
		logger.Debugf("serviceMonitor '%s' get relabels config: %+v", serviceMonitorID(serviceMonitor), relabels)

		monitorMeta := meta
		monitorMeta.Index = index

		var proxyURL string
		if endpoint.ProxyURL != nil {
			proxyURL = *endpoint.ProxyURL
		}

		dis := kubernetesd.New(c.ctx, kubernetesd.TypeEndpoints(useEndpointslice), &kubernetesd.Options{
			CommonOptions: &discover.CommonOptions{
				MonitorMeta:            monitorMeta,
				RelabelRule:            feature.RelabelRule(serviceMonitor.Annotations),
				RelabelIndex:           feature.RelabelIndex(serviceMonitor.Annotations),
				NormalizeMetricName:    feature.IfNormalizeMetricName(serviceMonitor.Annotations),
				AntiAffinity:           feature.IfAntiAffinity(serviceMonitor.Annotations),
				MatchSelector:          feature.MonitorMatchSelector(serviceMonitor.Annotations),
				DropSelector:           feature.MonitorDropSelector(serviceMonitor.Annotations),
				LabelJoinMatcher:       feature.LabelJoinMatcher(serviceMonitor.Annotations),
				ForwardLocalhost:       feature.IfForwardLocalhost(serviceMonitor.Annotations),
				Name:                   monitorMeta.ID(),
				DataID:                 dataID,
				Relabels:               resultLabels,
				Path:                   endpoint.Path,
				Scheme:                 endpoint.Scheme,
				BearerTokenFile:        endpoint.BearerTokenFile,
				Period:                 string(endpoint.Interval),
				ProxyURL:               proxyURL,
				Timeout:                string(endpoint.ScrapeTimeout),
				ExtraLabels:            specLabels,
				DisableCustomTimestamp: !ifHonorTimestamps(endpoint.HonorTimestamps),
				System:                 systemResource,
				UrlValues:              endpoint.Params,
				MetricRelabelConfigs:   metricRelabelings,
				CheckNodeNameFunc:      c.objectsController.CheckNodeName,
				NodeLabelsFunc:         c.objectsController.NodeLabels,
			},
			Client:            c.client,
			Namespaces:        namespaces,
			KubeConfig:        configs.G().KubeConfig,
			TLSConfig:         endpoint.TLSConfig.DeepCopy(),
			BasicAuth:         endpoint.BasicAuth.DeepCopy(),
			BearerTokenSecret: endpoint.BearerTokenSecret.DeepCopy(),
			UseEndpointSlice:  useEndpointslice,
		})

		logger.Infof("create new endpoint discover: %s", dis.Name())
		discovers = append(discovers, dis)
	}
	return discovers
}

func ifRejectServiceMonitor(monitor *promv1.ServiceMonitor) bool {
	if monitor == nil {
		return false
	}
	for _, rule := range configs.G().MonitorBlacklistMatchRules {
		if !rule.Validate() {
			continue
		}
		if utils.LowerEq(rule.Kind, monitor.Kind) && rule.Namespace == monitor.Namespace && rule.Name == monitor.Name {
			return true
		}
	}
	return false
}
