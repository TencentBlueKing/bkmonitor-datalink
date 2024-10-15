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
	"reflect"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/kubernetesd"
	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	promhttpsd "github.com/prometheus/prometheus/discovery/http"
	promk8ssd "github.com/prometheus/prometheus/discovery/kubernetes"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/httpd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (c *Operator) getPromScrapeConfigs() ([]config.ScrapeConfig, bool) {
	if len(configs.G().PromSDSecrets) == 0 {
		return nil, false
	}

	var cfgs []config.ScrapeConfig
	round := make(map[string][]byte) // 本轮获取到的数据
	for _, secret := range configs.G().PromSDSecrets {
		m, err := c.getPromSDSecretData(secret)
		if err != nil {
			logger.Errorf("get secrets sesource failed, config=(%+v), err: %v", secret, err)
			continue
		}

		for k, v := range m {
			sdc, err := unmarshalPromSdConfigs(v)
			if err != nil {
				logger.Errorf("unmarshal prom sdconfigs failed, resource=(%s), err: %v", k, err)
				continue
			}

			round[k] = v
			cfgs = append(cfgs, sdc...)
		}
	}

	eq := reflect.DeepEqual(c.promSdConfigsBytes, round) // 对比是否需要更新操作
	c.promSdConfigsBytes = round
	return cfgs, !eq // changed
}

func (c *Operator) getPromSDSecretData(secret configs.PromSDSecret) (map[string][]byte, error) {
	// 需要同时指定 namespace/name
	if secret.Namespace == "" || secret.Name == "" {
		return nil, errors.New("empty sdconfig namespace/name")
	}
	secretClient := c.client.CoreV1().Secrets(secret.Namespace)
	obj, err := secretClient.Get(c.ctx, secret.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	ret := make(map[string][]byte)
	for file, data := range obj.Data {
		ret[secretKeyFunc(secret, file)] = data
	}
	return ret, nil
}

func secretKeyFunc(secret configs.PromSDSecret, file string) string {
	return fmt.Sprintf("%s/%s/%s", secret.Namespace, secret.Name, file)
}

func unmarshalPromSdConfigs(b []byte) ([]config.ScrapeConfig, error) {
	var objs []interface{}
	if err := yaml.Unmarshal(b, &objs); err != nil {
		return nil, err
	}

	var ret []config.ScrapeConfig
	for i := 0; i < len(objs); i++ {
		obj := objs[i]
		var sc config.ScrapeConfig

		bs, err := yaml.Marshal(obj)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(bs, &sc); err != nil {
			return nil, err
		}
		ret = append(ret, sc)
	}

	return ret, nil
}

func (c *Operator) createHttpSdDiscover(scrapeConfig config.ScrapeConfig, sdConfig *promhttpsd.SDConfig, index int) (discover.Discover, error) {
	metricRelabelings := make([]yaml.MapSlice, 0)
	if len(scrapeConfig.MetricRelabelConfigs) != 0 {
		for _, cfg := range scrapeConfig.MetricRelabelConfigs {
			relabeling := generatePromRelabelConfig(cfg)
			metricRelabelings = append(metricRelabelings, relabeling)
		}
	}

	monitorMeta := define.MonitorMeta{
		Name:      scrapeConfig.JobName,
		Kind:      monitorKindHttpSd,
		Namespace: "-", // 不标记 namespace
		Index:     index,
	}
	// 默认使用 custommetric dataid
	dataID, err := c.dw.MatchMetricDataID(monitorMeta, false)
	if err != nil {
		return nil, err
	}

	specLabels := dataID.Spec.Labels
	httpClientConfig := scrapeConfig.HTTPClientConfig

	var proxyURL string
	if httpClientConfig.ProxyURL.URL != nil {
		proxyURL = httpClientConfig.ProxyURL.String()
	}

	var bearerTokenFile string
	auth := httpClientConfig.Authorization
	if auth != nil && auth.Type == "Bearer" {
		bearerTokenFile = auth.CredentialsFile
	}

	castDuration := func(d model.Duration) string {
		if d <= 0 {
			return ""
		}
		return d.String()
	}
	dis := httpd.New(c.ctx, c.objectsController.NodeNameExists, &httpd.Options{
		CommonOptions: &discover.CommonOptions{
			MonitorMeta:            monitorMeta,
			Name:                   monitorMeta.ID(),
			DataID:                 dataID,
			Relabels:               scrapeConfig.RelabelConfigs,
			Path:                   scrapeConfig.MetricsPath,
			Scheme:                 scrapeConfig.Scheme,
			BearerTokenFile:        bearerTokenFile,
			ProxyURL:               proxyURL,
			Period:                 castDuration(scrapeConfig.ScrapeInterval),
			Timeout:                castDuration(scrapeConfig.ScrapeTimeout),
			DisableCustomTimestamp: !ifHonorTimestamps(&scrapeConfig.HonorTimestamps),
			UrlValues:              scrapeConfig.Params,
			ExtraLabels:            specLabels,
			MetricRelabelConfigs:   metricRelabelings,
		},
		SDConfig:         sdConfig,
		HTTPClientConfig: httpClientConfig,
	})
	logger.Infof("create httpsd discover: %s", dis.Name())

	return dis, nil
}

func (c *Operator) createKubernetesSdDiscover(scrapeConfig config.ScrapeConfig, sdConfig *promk8ssd.SDConfig, index int) (discover.Discover, error) {
	metricRelabelings := make([]yaml.MapSlice, 0)
	if len(scrapeConfig.MetricRelabelConfigs) != 0 {
		for _, cfg := range scrapeConfig.MetricRelabelConfigs {
			relabeling := generatePromRelabelConfig(cfg)
			metricRelabelings = append(metricRelabelings, relabeling)
		}
	}

	monitorMeta := define.MonitorMeta{
		Name:      scrapeConfig.JobName,
		Kind:      monitorKindKubernetesSd,
		Namespace: "-", // 不标记 namespace
		Index:     index,
	}
	// 默认使用 custommetric dataid
	dataID, err := c.dw.MatchMetricDataID(monitorMeta, false)
	if err != nil {
		return nil, err
	}

	specLabels := dataID.Spec.Labels
	httpClientConfig := scrapeConfig.HTTPClientConfig

	var proxyURL string
	if httpClientConfig.ProxyURL.URL != nil {
		proxyURL = httpClientConfig.ProxyURL.String()
	}

	var bearerTokenFile string
	auth := httpClientConfig.Authorization
	if auth != nil && auth.Type == "Bearer" {
		bearerTokenFile = auth.CredentialsFile
	}

	castDuration := func(d model.Duration) string {
		if d <= 0 {
			return ""
		}
		return d.String()
	}

	scrapeConfig.HTTPClientConfig.TLSConfig.CAFile

	dis := kubernetesd.New(c.ctx, string(sdConfig.Role), c.objectsController.NodeNameExists, &kubernetesd.Options{
		CommonOptions: &discover.CommonOptions{
			MonitorMeta:            monitorMeta,
			Name:                   monitorMeta.ID(),
			DataID:                 dataID,
			Relabels:               scrapeConfig.RelabelConfigs,
			Path:                   scrapeConfig.MetricsPath,
			Scheme:                 scrapeConfig.Scheme,
			BearerTokenFile:        bearerTokenFile,
			ProxyURL:               proxyURL,
			Period:                 castDuration(scrapeConfig.ScrapeInterval),
			Timeout:                castDuration(scrapeConfig.ScrapeTimeout),
			DisableCustomTimestamp: !ifHonorTimestamps(&scrapeConfig.HonorTimestamps),
			UrlValues:              scrapeConfig.Params,
			ExtraLabels:            specLabels,
			MetricRelabelConfigs:   metricRelabelings,
		},
		KubeConfig:       configs.G().KubeConfig,
		Namespaces:       sdConfig.NamespaceDiscovery.Names,
		Client:           c.client,
		UseEndpointSlice: useEndpointslice,
	})
	logger.Infof("create httpsd discover: %s", dis.Name())

	return dis, nil
}

func (c *Operator) createPromScrapeConfigDiscovers() []discover.Discover {
	scrapeConfigs, ok := c.getPromScrapeConfigs()
	if !ok {
		return nil
	}

	logger.Infof("got prom scrapeConfigs, count=%d", len(scrapeConfigs))
	var discovers []discover.Discover
	for i := 0; i < len(scrapeConfigs); i++ {
		scrapeConfig := scrapeConfigs[i]
		for idx, rc := range scrapeConfig.ServiceDiscoveryConfigs {
			switch obj := rc.(type) {
			case *promhttpsd.SDConfig: // TODO(mando): 目前仅支持 httpsd
				httpSdDiscover, err := c.createHttpSdDiscover(scrapeConfig, obj, idx)
				if err != nil {
					logger.Errorf("failed to create httpsd discover: %v", err)
					continue
				}
				discovers = append(discovers, httpSdDiscover)
			case *promk8ssd.SDConfig:
				//scrapeConfig.
				dis := kubernetesd.New(c.ctx, string(obj.Role), c.objectsController.NodeNameExists, &kubernetesd.Options{
					CommonOptions: &discover.CommonOptions{
						MonitorMeta: monitorMeta,
						//RelabelRule:            feature.RelabelRule(podMonitor.Annotations),
						//RelabelIndex:           feature.RelabelIndex(podMonitor.Annotations),
						//NormalizeMetricName:    feature.IfNormalizeMetricName(podMonitor.Annotations),
						//AntiAffinity:           feature.IfAntiAffinity(podMonitor.Annotations),
						//MatchSelector:          feature.MonitorMatchSelector(podMonitor.Annotations),
						//DropSelector:           feature.MonitorDropSelector(podMonitor.Annotations),
						//LabelJoinMatcher:       feature.LabelJoinMatcher(podMonitor.Annotations),
						//ForwardLocalhost:       feature.IfForwardLocalhost(podMonitor.Annotations),
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
					},
					Client:            c.client,
					Namespaces:        namespaces,
					KubeConfig:        configs.G().KubeConfig,
					BasicAuth:         endpoint.BasicAuth.DeepCopy(),
					BearerTokenSecret: endpoint.BearerTokenSecret.DeepCopy(),
					TLSConfig:         &promv1.TLSConfig{SafeTLSConfig: safeTlsConfig},
					UseEndpointSlice:  useEndpointslice,
				})
			}
		}
	}
	return discovers
}

func (c *Operator) loopHandlePromSdConfigs() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	fn := func() {
		discovers := c.createPromScrapeConfigDiscovers()
		for _, dis := range discovers {
			if err := c.addOrUpdateDiscover(dis); err != nil {
				logger.Errorf("add or update prom scrapeConfigs discover %s failed, err: %s", dis, err)
			}
		}
	}

	fn() // 启动即执行

	for {
		select {
		case <-c.ctx.Done():
			return

		case <-ticker.C:
			fn()
		}
	}
}
