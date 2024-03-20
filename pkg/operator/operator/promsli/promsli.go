// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promsli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	namespacelabeler "github.com/prometheus-operator/prometheus-operator/pkg/namespace-labeler"
	"github.com/prometheus/prometheus/model/rulefmt"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	sliAnnotation = "sli_monitor"
)

type Controller struct {
	mut             sync.Mutex
	rules           map[string]*promv1.PrometheusRule
	rulesRelation   map[string]string
	serviceMonitors map[string]*promv1.ServiceMonitor
	podMonitors     map[string]*promv1.PodMonitor
}

func NewController() *Controller {
	c := &Controller{
		rules:           make(map[string]*promv1.PrometheusRule),
		rulesRelation:   make(map[string]string),
		serviceMonitors: make(map[string]*promv1.ServiceMonitor),
		podMonitors:     make(map[string]*promv1.PodMonitor),
	}

	go func() {
		for range time.Tick(time.Second * 30) {
			c.createOrUpdateResource()
		}
	}()

	return c
}

func (c *Controller) createOrUpdateResource() {
	c.GeneratePromScrapeSecret()
	c.GeneratePromRuleConfigMap()
}

func (c *Controller) UpdatePrometheusRule(pr *promv1.PrometheusRule) {
	c.mut.Lock()
	defer c.mut.Unlock()

	v, ok := pr.Annotations[sliAnnotation]
	if !ok {
		return
	}

	id := pr.Namespace + "-" + pr.Name
	c.rules[id] = pr
	c.rulesRelation[id] = v
}

func (c *Controller) DeletePrometheusRule(pr *promv1.PrometheusRule) {
	c.mut.Lock()
	defer c.mut.Unlock()

	id := pr.Namespace + "-" + pr.Name
	delete(c.rules, id)
	delete(c.rulesRelation, id)
}

func (c *Controller) UpdateServiceMonitor(sm *promv1.ServiceMonitor) {
	c.mut.Lock()
	defer c.mut.Unlock()

	id := sm.Namespace + "-" + sm.Name
	c.serviceMonitors[id] = sm
}

func (c *Controller) DeleteServiceMonitor(sm *promv1.ServiceMonitor) {
	c.mut.Lock()
	defer c.mut.Unlock()

	id := sm.Namespace + "-" + sm.Name
	delete(c.serviceMonitors, id)
}

func (c *Controller) UpdatePodMonitor(pm *promv1.PodMonitor) {
	c.mut.Lock()
	defer c.mut.Unlock()

	id := pm.Namespace + "-" + pm.Name
	c.podMonitors[id] = pm
}

func (c *Controller) DeletePodMonitor(pm *promv1.PodMonitor) {
	c.mut.Lock()
	defer c.mut.Unlock()

	id := pm.Namespace + "-" + pm.Name
	delete(c.podMonitors, id)
}

func (c *Controller) GeneratePromRuleConfigMap() corev1.ConfigMap {
	c.mut.Lock()
	defer c.mut.Unlock()

	data := make(map[string]string)
	for id, rule := range c.rules {
		for i := range rule.Spec.Groups {
			rule.Spec.Groups[i].PartialResponseStrategy = ""
		}

		content, err := yaml.Marshal(rule.Spec)
		if err != nil {
			logger.Errorf("marshal ruleconfig(%s) failed, err: %v", id, err)
			continue
		}
		_, errs := rulefmt.Parse(content)
		if len(errs) > 0 {
			for _, err = range errs {
				logger.Errorf("parse ruleconfig(%s) failed, err: %v", id, err)
			}
			continue
		}
		data[id] = string(content)
	}

	logger.Infof("GeneratePromRulConfigMap: %+v", data) //TODO(remove)
	return corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "prometheus-rulefiles",
			Labels: map[string]string{
				"controller": "bkm-operator",
			},
		},
		Data: data,
	}
}

var invalidLabelCharRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func sanitizeLabelName(name string) string {
	return invalidLabelCharRE.ReplaceAllString(name, "_")
}

func (c *Controller) GeneratePromScrapeSecret() {
	c.mut.Lock()
	defer c.mut.Unlock()

	var cfg yaml.MapSlice
	globalCfg := yaml.MapItem{
		Key: "global",
		Value: yaml.MapSlice{
			{
				Key:   "evaluation_interval",
				Value: "1m",
			},
			{
				Key:   "scrape_interval",
				Value: "1m",
			},
		},
	}
	cfg = append(cfg, globalCfg)

	cfg = append(cfg, yaml.MapItem{
		Key:   "rule_files",
		Value: []string{"/etc/prometheus/rules/prometheus-po-kube-prometheus-stack-prometheus-rulefiles-0/*.yaml"},
	})

	cfg = append(cfg, yaml.MapItem{
		Key:   "scrape_configs",
		Value: c.generateServiceMonitorScrapeConfigs(),
	})
	logger.Infof("render cfg: %+v", cfg) //TODO(remove)
}

func (c *Controller) generateServiceMonitorScrapeConfigs() []yaml.MapSlice {
	var cfg []yaml.MapSlice
	for _, sm := range c.serviceMonitors {
		var matched bool
		s := fmt.Sprintf("ServiceMonitor/%s/%s", sm.Namespace, sm.Name)
		for _, relation := range c.rulesRelation {
			if s == relation {
				matched = true
				break
			}
		}

		if !matched {
			logger.Infof("skip no matched %s", s)
			continue
		}
		for i, ep := range sm.Spec.Endpoints {
			cfg = append(cfg, generateServiceMonitorScrapeConfig(sm, ep, i))
		}
	}
	return cfg
}

func generateServiceMonitorScrapeConfig(sm *promv1.ServiceMonitor, ep promv1.Endpoint, index int) yaml.MapSlice {
	cfg := yaml.MapSlice{
		{
			Key:   "job_name",
			Value: fmt.Sprintf("serviceMonitor/%s/%s/%d", sm.Namespace, sm.Name, index),
		},
	}
	cfg = append(cfg, generateServiceMonitorK8sSDConfig(sm))

	var labelKeys []string
	for k := range sm.Spec.Selector.MatchLabels {
		labelKeys = append(labelKeys, k)
	}
	sort.Strings(labelKeys)

	relabelings := []yaml.MapSlice{
		{
			{Key: "source_labels", Value: []string{"job"}},
			{Key: "target_label", Value: "__tmp_prometheus_job_name"},
		},
	}

	for _, k := range labelKeys {
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "action", Value: "keep"},
			{Key: "source_labels", Value: []string{"__meta_kubernetes_service_label_" + sanitizeLabelName(k), "__meta_kubernetes_service_labelpresent_" + sanitizeLabelName(k)}},
			{Key: "regex", Value: fmt.Sprintf("(%s);true", sm.Spec.Selector.MatchLabels[k])},
		})
	}
	// Set based label matching. We have to map the valid relations
	// `In`, `NotIn`, `Exists`, and `DoesNotExist`, into relabeling rules.
	for _, exp := range sm.Spec.Selector.MatchExpressions {
		switch exp.Operator {
		case metav1.LabelSelectorOpIn:
			relabelings = append(relabelings, yaml.MapSlice{
				{Key: "action", Value: "keep"},
				{Key: "source_labels", Value: []string{"__meta_kubernetes_service_label_" + sanitizeLabelName(exp.Key), "__meta_kubernetes_service_labelpresent_" + sanitizeLabelName(exp.Key)}},
				{Key: "regex", Value: fmt.Sprintf("(%s);true", strings.Join(exp.Values, "|"))},
			})
		case metav1.LabelSelectorOpNotIn:
			relabelings = append(relabelings, yaml.MapSlice{
				{Key: "action", Value: "drop"},
				{Key: "source_labels", Value: []string{"__meta_kubernetes_service_label_" + sanitizeLabelName(exp.Key), "__meta_kubernetes_service_labelpresent_" + sanitizeLabelName(exp.Key)}},
				{Key: "regex", Value: fmt.Sprintf("(%s);true", strings.Join(exp.Values, "|"))},
			})
		case metav1.LabelSelectorOpExists:
			relabelings = append(relabelings, yaml.MapSlice{
				{Key: "action", Value: "keep"},
				{Key: "source_labels", Value: []string{"__meta_kubernetes_service_labelpresent_" + sanitizeLabelName(exp.Key)}},
				{Key: "regex", Value: "true"},
			})
		case metav1.LabelSelectorOpDoesNotExist:
			relabelings = append(relabelings, yaml.MapSlice{
				{Key: "action", Value: "drop"},
				{Key: "source_labels", Value: []string{"__meta_kubernetes_service_labelpresent_" + sanitizeLabelName(exp.Key)}},
				{Key: "regex", Value: "true"},
			})
		}
	}

	// Filter targets based on correct port for the endpoint.
	if ep.Port != "" {
		sourceLabels := []string{"__meta_kubernetes_endpoint_port_name"}
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "action", Value: "keep"},
			yaml.MapItem{Key: "source_labels", Value: sourceLabels},
			{Key: "regex", Value: ep.Port},
		})
	} else if ep.TargetPort != nil {
		if ep.TargetPort.StrVal != "" {
			relabelings = append(relabelings, yaml.MapSlice{
				{Key: "action", Value: "keep"},
				{Key: "source_labels", Value: []string{"__meta_kubernetes_pod_container_port_name"}},
				{Key: "regex", Value: ep.TargetPort.String()},
			})
		} else if ep.TargetPort.IntVal != 0 {
			relabelings = append(relabelings, yaml.MapSlice{
				{Key: "action", Value: "keep"},
				{Key: "source_labels", Value: []string{"__meta_kubernetes_pod_container_port_number"}},
				{Key: "regex", Value: ep.TargetPort.String()},
			})
		}
	}

	sourceLabels := []string{"__meta_kubernetes_endpoint_address_target_kind", "__meta_kubernetes_endpoint_address_target_name"}
	// Relabel namespace and pod and service labels into proper labels.
	relabelings = append(relabelings, []yaml.MapSlice{
		{ // Relabel node labels with meta labels available with Prometheus >= v2.3.
			yaml.MapItem{Key: "source_labels", Value: sourceLabels},
			{Key: "separator", Value: ";"},
			{Key: "regex", Value: "Node;(.*)"},
			{Key: "replacement", Value: "${1}"},
			{Key: "target_label", Value: "node"},
		},
		{ // Relabel pod labels for >=v2.3 meta labels
			yaml.MapItem{Key: "source_labels", Value: sourceLabels},
			{Key: "separator", Value: ";"},
			{Key: "regex", Value: "Pod;(.*)"},
			{Key: "replacement", Value: "${1}"},
			{Key: "target_label", Value: "pod"},
		},
		{
			{Key: "source_labels", Value: []string{"__meta_kubernetes_namespace"}},
			{Key: "target_label", Value: "namespace"},
		},
		{
			{Key: "source_labels", Value: []string{"__meta_kubernetes_service_name"}},
			{Key: "target_label", Value: "service"},
		},
		{
			{Key: "source_labels", Value: []string{"__meta_kubernetes_pod_name"}},
			{Key: "target_label", Value: "pod"},
		},
		{
			{Key: "source_labels", Value: []string{"__meta_kubernetes_pod_container_name"}},
			{Key: "target_label", Value: "container"},
		},
	}...)

	// Relabel targetLabels from Service onto target.
	for _, l := range sm.Spec.TargetLabels {
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "source_labels", Value: []string{"__meta_kubernetes_service_label_" + sanitizeLabelName(l)}},
			{Key: "target_label", Value: sanitizeLabelName(l)},
			{Key: "regex", Value: "(.+)"},
			{Key: "replacement", Value: "${1}"},
		})
	}

	// By default, generate a safe job name from the service name.  We also keep
	// this around if a jobLabel is set in case the targets don't actually have a
	// value for it.
	relabelings = append(relabelings, yaml.MapSlice{
		{Key: "source_labels", Value: []string{"__meta_kubernetes_service_name"}},
		{Key: "target_label", Value: "job"},
		{Key: "replacement", Value: "${1}"},
	})
	if sm.Spec.JobLabel != "" {
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "source_labels", Value: []string{"__meta_kubernetes_service_label_" + sanitizeLabelName(sm.Spec.JobLabel)}},
			{Key: "target_label", Value: "job"},
			{Key: "regex", Value: "(.+)"},
			{Key: "replacement", Value: "${1}"},
		})
	}

	// A single service may potentially have multiple metrics
	//	endpoints, therefore the endpoints labels is filled with the ports name or
	//	as a fallback the port number.
	if ep.Port != "" {
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "target_label", Value: "endpoint"},
			{Key: "replacement", Value: ep.Port},
		})
	} else if ep.TargetPort != nil && ep.TargetPort.String() != "" {
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "target_label", Value: "endpoint"},
			{Key: "replacement", Value: ep.TargetPort.String()},
		})
	}

	labeler := namespacelabeler.New("", nil, false)
	cfg = append(cfg, yaml.MapItem{Key: "relabel_configs", Value: relabelings})
	cfg = append(cfg, yaml.MapItem{
		Key:   "metric_relabel_configs",
		Value: generateRelabelConfig(labeler.GetRelabelingConfigs(sm.TypeMeta, sm.ObjectMeta, ep.MetricRelabelConfigs)),
	})

	return cfg
}

func generateServiceMonitorK8sSDConfig(sm *promv1.ServiceMonitor) yaml.MapItem {
	var namespaces []string
	if len(sm.Spec.NamespaceSelector.MatchNames) == 0 {
		namespaces = []string{sm.Namespace}
	} else {
		namespaces = sm.Spec.NamespaceSelector.MatchNames
	}

	k8sSDConfig := yaml.MapSlice{{
		Key:   "role",
		Value: "endpoints",
	}}

	if len(namespaces) > 0 {
		k8sSDConfig = append(k8sSDConfig, yaml.MapItem{
			Key: "namespaces",
			Value: yaml.MapSlice{{
				Key:   "names",
				Value: namespaces,
			}},
		})
	}

	return yaml.MapItem{
		Key: "kubernetes_sd_configs",
		Value: []yaml.MapSlice{
			k8sSDConfig,
		},
	}
}

func generateRelabelConfig(rc []*promv1.RelabelConfig) []yaml.MapSlice {
	var cfg []yaml.MapSlice

	for _, c := range rc {
		relabeling := yaml.MapSlice{}

		if len(c.SourceLabels) > 0 {
			relabeling = append(relabeling, yaml.MapItem{Key: "source_labels", Value: c.SourceLabels})
		}

		if c.Separator != "" {
			relabeling = append(relabeling, yaml.MapItem{Key: "separator", Value: c.Separator})
		}

		if c.TargetLabel != "" {
			relabeling = append(relabeling, yaml.MapItem{Key: "target_label", Value: c.TargetLabel})
		}

		if c.Regex != "" {
			relabeling = append(relabeling, yaml.MapItem{Key: "regex", Value: c.Regex})
		}

		if c.Modulus != uint64(0) {
			relabeling = append(relabeling, yaml.MapItem{Key: "modulus", Value: c.Modulus})
		}

		if c.Replacement != "" {
			relabeling = append(relabeling, yaml.MapItem{Key: "replacement", Value: c.Replacement})
		}

		if c.Action != "" {
			relabeling = append(relabeling, yaml.MapItem{Key: "action", Value: strings.ToLower(c.Action)})
		}

		cfg = append(cfg, relabeling)
	}
	return cfg
}
