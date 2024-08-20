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
	"bytes"
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	promyaml "github.com/ghodss/yaml"
	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	namespacelabeler "github.com/prometheus-operator/prometheus-operator/pkg/namespace-labeler"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/compressor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	sliAnnotationBuiltin = "ServiceMonitor/sli"

	rulesMetric = "alert_rules"
)

type Controller struct {
	ctx    context.Context
	cancel context.CancelFunc
	client kubernetes.Interface
	bus    *notifier.RateBus

	mut                    sync.Mutex
	rules                  map[string]*promv1.PrometheusRule
	rulesRelation          map[string]string
	smMetrics              map[string]map[string]struct{}
	serviceMonitors        map[string]*promv1.ServiceMonitor
	registerRules          map[string]struct{}
	endpointSliceSupported bool

	prevScrapeContent []byte
	prevRuleContent   map[string]string
}

func NewController(ctx context.Context, client kubernetes.Interface, endpointSliceSupported bool) *Controller {
	ctx, cancel := context.WithCancel(ctx)
	c := &Controller{
		ctx:                    ctx,
		cancel:                 cancel,
		client:                 client,
		bus:                    notifier.NewDefaultRateBus(),
		rules:                  make(map[string]*promv1.PrometheusRule),
		rulesRelation:          make(map[string]string),
		smMetrics:              map[string]map[string]struct{}{},
		endpointSliceSupported: endpointSliceSupported,
		serviceMonitors:        make(map[string]*promv1.ServiceMonitor),
		registerRules:          map[string]struct{}{},
	}

	go c.handle()
	return c
}

func (c *Controller) handle() {
	createOrUpdateResource := func() {
		if err := c.CreateOrUpdatePromScrapeSecret(); err != nil {
			logger.Errorf("failed to update prometheus scrape secret: %v", err)
		}
		if err := c.CreateOrUpdatePromRuleConfigMap(); err != nil {
			logger.Errorf("failed to update prometheus rules configmap: %v", err)
		}
	}

	ticker := time.NewTicker(2 * time.Hour) // 兜底检查
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return

		case <-c.bus.Subscribe(): // 信号收敛
			createOrUpdateResource()

		case <-ticker.C:
			createOrUpdateResource()
		}
	}
}

func verifyServiceMonitor(pr *promv1.PrometheusRule) (string, bool) {
	v := feature.SliMonitor(pr.Annotations)
	if v == "" {
		logger.Infof("skip none sli-annotations PrometheusRule: %s/%s", pr.Namespace, pr.Name)
		return "", false
	}

	if v == sliAnnotationBuiltin {
		return v, true
	}

	parts := strings.Split(v, "/")
	if len(parts) != 3 {
		logger.Warnf("annotations requeire format(monitorType/namespace/name), but got %s", v)
		return "", false
	}

	if parts[0] != "ServiceMonitor" {
		logger.Warnf("only ServiceMonitor supported, got: %s", parts[0])
		return "", false
	}
	return v, true
}

func (c *Controller) RuleMetrics() []byte {
	c.mut.Lock()
	defer c.mut.Unlock()

	var buf bytes.Buffer
	for line := range c.registerRules {
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

func (c *Controller) UpdatePrometheusRule(pr *promv1.PrometheusRule) {
	c.mut.Lock()
	defer c.mut.Unlock()

	v, ok := verifyServiceMonitor(pr)
	if !ok {
		return
	}
	c.bus.Publish()

	logger.Infof("found new PrometheusRule: %s/%s", pr.Namespace, pr.Name)
	id := pr.Namespace + "-" + pr.Name
	c.rules[id] = pr
	c.rulesRelation[id] = v

	// 存储 servicemonitor <-> metrics 对应关系
	// Note: 单 rule 的告警指标只能来自单 servicemonitor
	// 暂不做清理
	if _, ok := c.smMetrics[v]; !ok {
		c.smMetrics[v] = map[string]struct{}{}
	}
	for _, group := range pr.Spec.Groups {
		for _, rule := range group.Rules {
			metrics := parsePromQLMetrics(rule.Expr.String())
			for _, metric := range metrics {
				c.smMetrics[v][metric] = struct{}{}
			}

			// 记录 alerts 并记录上报
			labels := map[string]string{"alertname": rule.Alert}
			for name, val := range rule.Labels {
				labels[name] = val
			}
			c.registerRules[toToPromFormat(labels)] = struct{}{}
		}
	}
}

func (c *Controller) DeletePrometheusRule(pr *promv1.PrometheusRule) {
	c.mut.Lock()
	defer c.mut.Unlock()

	c.bus.Publish()
	id := pr.Namespace + "-" + pr.Name
	delete(c.rules, id)
	delete(c.rulesRelation, id)
}

func (c *Controller) UpdateServiceMonitor(sm *promv1.ServiceMonitor) {
	c.mut.Lock()
	defer c.mut.Unlock()

	c.bus.Publish()
	id := sm.Namespace + "-" + sm.Name
	c.serviceMonitors[id] = sm
}

func (c *Controller) DeleteServiceMonitor(sm *promv1.ServiceMonitor) {
	c.mut.Lock()
	defer c.mut.Unlock()

	c.bus.Publish()
	id := sm.Namespace + "-" + sm.Name
	delete(c.serviceMonitors, id)
}

func (c *Controller) GeneratePromRuleContent() map[string]string {
	c.mut.Lock()
	defer c.mut.Unlock()

	data := make(map[string]string)
	for id, rule := range c.rules {
		for i := range rule.Spec.Groups {
			rule.Spec.Groups[i].PartialResponseStrategy = ""
		}

		content, err := promyaml.Marshal(rule.Spec)
		if err != nil {
			logger.Errorf("marshal prometheus rule '%s' failed, err: %v", id, err)
			continue
		}
		_, errs := rulefmt.Parse(content)
		if len(errs) > 0 {
			for _, err = range errs {
				logger.Errorf("parse prometheus rule '%s' failed, err: %v", id, err)
			}
			continue
		}
		data[id+".yaml"] = string(content)
	}
	return data
}

func (c *Controller) CreateOrUpdatePromRuleConfigMap() error {
	content := c.GeneratePromRuleContent()
	if len(content) <= 0 {
		logger.Info("no prometheus rule content found, skipped")
		return nil
	}

	if reflect.DeepEqual(content, c.prevRuleContent) {
		logger.Info("no prometheus rule content changed, skipped")
		return nil
	}
	c.prevRuleContent = content

	for k := range content {
		logger.Infof("create or update prometheus rule: %s", k)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: ConfConfig.ConfigMapName,
			Labels: map[string]string{
				"controller": "bkm-operator",
			},
		},
		Data: content,
	}
	logger.Info("create or update prometheus rules configmap")
	cli := c.client.CoreV1().ConfigMaps(ConfConfig.Namespace)
	return k8sutils.CreateOrUpdateConfigMap(c.ctx, cli, cm)
}

var invalidLabelCharRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func sanitizeLabelName(name string) string {
	return invalidLabelCharRE.ReplaceAllString(name, "_")
}

func (c *Controller) GeneratePromScrapeYaml() yaml.MapSlice {
	c.mut.Lock()
	defer c.mut.Unlock()

	var cfg yaml.MapSlice
	var globalCfg yaml.MapSlice
	for k, v := range ConfConfig.Scrape.Global {
		globalCfg = append(globalCfg, yaml.MapItem{Key: k, Value: v})
	}
	var alertingCfg yaml.MapSlice
	for k, v := range ConfConfig.Scrape.Alerting {
		alertingCfg = append(alertingCfg, yaml.MapItem{Key: k, Value: v})
	}

	cfg = append(cfg, yaml.MapItem{Key: "global", Value: globalCfg})
	cfg = append(cfg, yaml.MapItem{Key: "alerting", Value: alertingCfg})
	cfg = append(cfg, yaml.MapItem{Key: "rule_files", Value: ConfConfig.Scrape.RuleFiles})
	cfg = append(cfg, yaml.MapItem{Key: "scrape_configs", Value: c.generateServiceMonitorScrapeConfigs()})
	return cfg
}

func (c *Controller) CreateOrUpdatePromScrapeSecret() error {
	cfg := c.GeneratePromScrapeYaml()
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return errors.Wrap(err, "yaml unmarshal failed")
	}

	compressed, err := compressor.Compress(b)
	if err != nil {
		return errors.Wrap(err, "compress data failed")
	}

	if bytes.Equal(c.prevScrapeContent, compressed) {
		logger.Info("no scrape content changed, skipped")
		return nil
	}
	c.prevScrapeContent = compressed

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: ConfConfig.SecretName,
			Labels: map[string]string{
				"controller": "bkm-operator",
			},
		},
		Data: map[string][]byte{
			"prometheus.yaml.gz": compressed,
		},
	}

	logger.Infof("create or update prometheus scrape secret, size=%dB", len(compressed))
	cli := c.client.CoreV1().Secrets(ConfConfig.Namespace)
	return k8sutils.CreateOrUpdateSecret(c.ctx, cli, secret)
}

func (c *Controller) generateServiceMonitorScrapeConfigs() []yaml.MapSlice {
	var cfg []yaml.MapSlice
	for _, sm := range c.serviceMonitors {
		// 内置白名单
		if feature.SliMonitor(sm.Annotations) == sliAnnotationBuiltin {
			for i, ep := range sm.Spec.Endpoints {
				cfg = append(cfg, c.generateServiceMonitorScrapeConfig(sm, ep, i, nil))
			}
			continue
		}

		// relation 匹配
		var matched bool
		s := fmt.Sprintf("ServiceMonitor/%s/%s", sm.Namespace, sm.Name)
		for _, relation := range c.rulesRelation {
			if s == relation {
				matched = true
				break
			}
		}

		// 确保 relation 描述了对应关系才进行数据采集
		if !matched {
			logger.Infof("skip no matched %s", s)
			continue
		}

		var keep []string
		for k := range c.smMetrics[s] {
			keep = append(keep, k)
		}

		for i, ep := range sm.Spec.Endpoints {
			cfg = append(cfg, c.generateServiceMonitorScrapeConfig(sm, ep, i, keep))
		}
	}
	return cfg
}

func (c *Controller) generateServiceMonitorScrapeConfig(sm *promv1.ServiceMonitor, ep promv1.Endpoint, index int, keepMetrics []string) yaml.MapSlice {
	cfg := yaml.MapSlice{
		{
			Key:   "job_name",
			Value: fmt.Sprintf("serviceMonitor/%s/%s/%d", sm.Namespace, sm.Name, index),
		},
	}

	cfg = append(cfg, generateServiceMonitorK8sSDConfig(sm))
	if ep.Interval != "" {
		cfg = append(cfg, yaml.MapItem{Key: "scrape_interval", Value: ep.Interval})
	}
	if ep.ScrapeTimeout != "" {
		cfg = append(cfg, yaml.MapItem{Key: "scrape_timeout", Value: ep.ScrapeTimeout})
	}
	if ep.Path != "" {
		cfg = append(cfg, yaml.MapItem{Key: "metrics_path", Value: ep.Path})
	}
	if ep.ProxyURL != nil {
		cfg = append(cfg, yaml.MapItem{Key: "proxy_url", Value: ep.ProxyURL})
	}
	if ep.Params != nil {
		cfg = append(cfg, yaml.MapItem{Key: "params", Value: ep.Params})
	}
	if ep.Scheme != "" {
		cfg = append(cfg, yaml.MapItem{Key: "scheme", Value: ep.Scheme})
	}

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
		relabelings = append(relabelings, yaml.MapSlice{
			{Key: "action", Value: "keep"},
			yaml.MapItem{Key: "source_labels", Value: []string{define.LabelEndpointAddressPortName(c.endpointSliceSupported)}},
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

	sourceLabels := []string{define.LabelEndpointAddressTargetKind(c.endpointSliceSupported), define.LabelEndpointAddressTargetName(c.endpointSliceSupported)}
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

	metricRelabels := make([]yaml.MapSlice, 0)
	if len(keepMetrics) > 0 {
		re := strings.Join(keepMetrics, "|")
		metricRelabels = append(metricRelabels, yaml.MapSlice{
			{Key: "action", Value: "keep"},
			{Key: "regex", Value: re},
			{Key: "separator", Value: ";"},
			{Key: "source_labels", Value: []string{"__name__"}},
		})
	}

	metricRelabels = append(metricRelabels, generateRelabelConfig(labeler.GetRelabelingConfigs(sm.TypeMeta, sm.ObjectMeta, ep.MetricRelabelConfigs))...)
	cfg = append(cfg, yaml.MapItem{
		Key:   "metric_relabel_configs",
		Value: metricRelabels,
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

func parsePromQLMetrics(s string) []string {
	filter := make(map[string]struct{})
	expr, err := parser.ParseExpr(s)
	if err != nil {
		return nil
	}
	parser.Inspect(expr, func(node parser.Node, nodes []parser.Node) error {
		switch node.(type) {
		case *parser.VectorSelector:
			filter[node.String()] = struct{}{}
		}
		return nil
	})

	var metrics []string
	for k := range filter {
		parts := strings.Split(k, "{")
		if len(parts) <= 0 {
			continue
		}
		metrics = append(metrics, parts[0])
	}

	sort.Strings(metrics)
	return metrics
}

func toToPromFormat(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString(rulesMetric)
	buf.WriteString("{")
	var n int
	for _, name := range keys {
		value := labels[name]
		if n > 0 {
			buf.WriteString(`,`)
		}
		n++
		buf.WriteString(name)
		buf.WriteString(`="`)
		buf.WriteString(value)
		buf.WriteString(`"`)
	}

	buf.WriteString("} 1")
	return buf.String()
}
