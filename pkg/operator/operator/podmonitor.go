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
	"strings"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/kubernetesd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func podMonitorID(obj *promv1.PodMonitor) string {
	return fmt.Sprintf("%s/%s", obj.Namespace, obj.Name)
}

func (c *Operator) handlePodMonitorAdd(obj any) {
	podMonitor, ok := obj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", obj)
		return
	}

	// 新增的 podmonitor 命中黑名单则流程终止
	if ifRejectPodMonitor(podMonitor) {
		logger.Infof("add action match blacklist rules, podMonitor=%s", podMonitorID(podMonitor))
		return
	}

	discovers := c.createPodMonitorDiscovers(podMonitor)
	for _, dis := range discovers {
		if err := c.addOrUpdateDiscover(dis); err != nil {
			logger.Errorf("add or update podMonitor discover %s failed: %s", dis, err)
		}
	}
}

func (c *Operator) handlePodMonitorUpdate(oldObj any, newObj any) {
	old, ok := oldObj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", oldObj)
		return
	}
	cur, ok := newObj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", newObj)
		return
	}

	if old.ResourceVersion == cur.ResourceVersion {
		logger.Debugf("podMonitor '%s' does not change", podMonitorID(old))
		return
	}

	// 对于更新的 podmonitor 如果新的 spec 命中黑名单 则需要将原有的 podmonitor 移除
	if ifRejectPodMonitor(cur) {
		logger.Infof("update action match blacklist rules, podMonitor=%s", podMonitorID(cur))
		for _, name := range c.getPodMonitorDiscoversName(cur) {
			c.deleteDiscoverByName(name)
		}
		return
	}

	for _, name := range c.getPodMonitorDiscoversName(old) {
		c.deleteDiscoverByName(name)
	}
	for _, dis := range c.createPodMonitorDiscovers(cur) {
		if err := c.addOrUpdateDiscover(dis); err != nil {
			logger.Errorf("add or update podMonitor discover %s failed: %s", dis, err)
		}
	}
}

func (c *Operator) handlePodMonitorDelete(obj any) {
	podMonitor, ok := obj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", obj)
		return
	}

	for _, name := range c.getPodMonitorDiscoversName(podMonitor) {
		c.deleteDiscoverByName(name)
	}
}

func (c *Operator) getPodMonitorDiscoversName(podMonitor *promv1.PodMonitor) []string {
	var names []string
	for index := range podMonitor.Spec.PodMetricsEndpoints {
		monitorMeta := define.MonitorMeta{
			Name:      podMonitor.Name,
			Kind:      monitorKindPodMonitor,
			Namespace: podMonitor.Namespace,
			Index:     index,
		}
		names = append(names, monitorMeta.ID())
	}
	return names
}

func (c *Operator) pickMonitorDataID(meta define.MonitorMeta, annotation map[string]string) (*v1beta1.DataID, error) {
	fillLabels := func(obj *v1beta1.DataID) {
		// labels 高优先级
		extLabels := feature.ExtendLabels(annotation)
		for k, v := range extLabels {
			obj.Spec.Labels[k] = v
		}
	}

	// 1) 优先选择 scheduled dataID
	schedDataID := feature.ScheduledDataID(annotation)
	if schedDataID > 0 {
		dataID, err := c.dw.MatchMetricDataID(define.MonitorMeta{}, true)
		if err != nil {
			return nil, err
		}

		cloned := dataID.DeepCopy()
		cloned.Spec.DataID = schedDataID
		cloned.Spec.Labels = make(map[string]string) // schedDataID 不需要内置 labels
		fillLabels(cloned)
		return cloned, nil
	}

	// 2) 根据匹配规则选择
	systemResource := feature.IfSystemResource(annotation)
	dataID, err := c.dw.MatchMetricDataID(meta, systemResource)
	if err != nil {
		return nil, err
	}

	cloned := dataID.DeepCopy()
	fillLabels(cloned)
	return cloned, nil
}

func (c *Operator) createPodMonitorDiscovers(podMonitor *promv1.PodMonitor) []discover.Discover {
	var (
		namespaces []string
		discovers  []discover.Discover
	)

	systemResource := feature.IfSystemResource(podMonitor.Annotations)
	meta := define.MonitorMeta{
		Name:      podMonitor.Name,
		Kind:      monitorKindPodMonitor,
		Namespace: podMonitor.Namespace,
	}
	dataID, err := c.pickMonitorDataID(meta, podMonitor.Annotations)
	if err != nil {
		logger.Errorf("podmonitor (%+v) no dataid matched", meta)
		return discovers
	}
	specLabels := dataID.Spec.Labels

	if podMonitor.Spec.NamespaceSelector.Any {
		namespaces = []string{}
	} else if len(podMonitor.Spec.NamespaceSelector.MatchNames) == 0 {
		namespaces = []string{podMonitor.Namespace}
	} else {
		namespaces = podMonitor.Spec.NamespaceSelector.MatchNames
	}

	logger.Infof("found new podMonitor '%s'", podMonitorID(podMonitor))
	for index, endpoint := range podMonitor.Spec.PodMetricsEndpoints {
		if endpoint.Path == "" {
			endpoint.Path = "/metrics"
		}
		if endpoint.Scheme == "" {
			endpoint.Scheme = "http"
		}

		relabels := getPodMonitorRelabels(podMonitor, &endpoint)
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

		logger.Debugf("podMonitor '%s' get relabels: %v", podMonitorID(podMonitor), relabels)

		monitorMeta := meta
		monitorMeta.Index = index

		var proxyURL string
		if endpoint.ProxyURL != nil {
			proxyURL = *endpoint.ProxyURL
		}

		var safeTlsConfig promv1.SafeTLSConfig
		tlsConfig := endpoint.TLSConfig.DeepCopy()
		if tlsConfig != nil {
			safeTlsConfig = tlsConfig.SafeTLSConfig
		}

		dis := kubernetesd.New(c.ctx, kubernetesd.TypePod, &kubernetesd.Options{
			CommonOptions: &discover.CommonOptions{
				MonitorMeta:            monitorMeta,
				RelabelRule:            feature.RelabelRule(podMonitor.Annotations),
				RelabelIndex:           feature.RelabelIndex(podMonitor.Annotations),
				NormalizeMetricName:    feature.IfNormalizeMetricName(podMonitor.Annotations),
				AntiAffinity:           feature.IfAntiAffinity(podMonitor.Annotations),
				MatchSelector:          feature.MonitorMatchSelector(podMonitor.Annotations),
				DropSelector:           feature.MonitorDropSelector(podMonitor.Annotations),
				LabelJoinMatcher:       feature.LabelJoinMatcher(podMonitor.Annotations),
				ForwardLocalhost:       feature.IfForwardLocalhost(podMonitor.Annotations),
				Name:                   monitorMeta.ID(),
				DataID:                 dataID,
				Relabels:               resultLabels,
				Path:                   endpoint.Path,
				Scheme:                 endpoint.Scheme,
				Period:                 string(endpoint.Interval),
				Timeout:                string(endpoint.ScrapeTimeout),
				ProxyURL:               proxyURL,
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
			BasicAuth:         endpoint.BasicAuth.DeepCopy(),
			BearerTokenSecret: endpoint.BearerTokenSecret.DeepCopy(),
			TLSConfig:         &promv1.TLSConfig{SafeTLSConfig: safeTlsConfig},
			UseEndpointSlice:  useEndpointslice,
		})

		logger.Infof("create new pod discover: %s", dis.Name())
		discovers = append(discovers, dis)
	}
	return discovers
}

func ifRejectPodMonitor(monitor *promv1.PodMonitor) bool {
	if monitor == nil {
		return false
	}
	for _, rule := range configs.G().MonitorBlacklistMatchRules {
		if !rule.Validate() {
			continue
		}
		if strings.ToUpper(rule.Kind) == strings.ToUpper(monitor.Kind) && rule.Namespace == monitor.Namespace && rule.Name == monitor.Name {
			return true
		}
	}
	return false
}
