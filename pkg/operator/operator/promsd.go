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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/httpsd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/kubernetesd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/polarissd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type resourceScrapConfig struct {
	resource string
	conf     config.ScrapeConfig
}

func (c *Operator) getPromScrapeConfigs() ([]resourceScrapConfig, bool) {
	if len(configs.G().PromSDSecrets) == 0 {
		return nil, false
	}

	var rscs []resourceScrapConfig
	newRound := make(map[string][]byte) // 本轮获取到的数据
	for _, secret := range configs.G().PromSDSecrets {
		secData, err := c.getPromSDSecretData(secret)
		if err != nil {
			logger.Errorf("get secrets sesource failed, config=(%+v): %v", secret, err)
			continue
		}

		for resource, data := range secData {
			sdc, err := unmarshalPromSdConfigs(data)
			if err != nil {
				logger.Errorf("unmarshal prom sdconfigs failed, resource=(%s): %v", resource, err)
				continue
			}

			newRound[resource] = data
			for i := 0; i < len(sdc); i++ {
				rscs = append(rscs, resourceScrapConfig{
					resource: resource,
					conf:     sdc[i],
				})
			}
		}
	}

	eq := reflect.DeepEqual(c.promSdConfigsBytes, newRound) // 对比是否需要更新操作
	c.promSdConfigsBytes = newRound
	return rscs, !eq // changed
}

func (c *Operator) getPromSDSecretDataByName(sdSecret configs.PromSDSecret) (map[string][]byte, error) {
	secretClient := c.client.CoreV1().Secrets(sdSecret.Namespace)
	obj, err := secretClient.Get(c.ctx, sdSecret.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	secData := make(map[string][]byte)
	for file, data := range obj.Data {
		secData[secretKeyFunc(sdSecret.Namespace, sdSecret.Name, file)] = data
	}
	return secData, nil
}

func (c *Operator) getPromSDSecretDataBySelector(sdSecret configs.PromSDSecret) (map[string][]byte, error) {
	secretClient := c.client.CoreV1().Secrets(sdSecret.Namespace)
	objList, err := secretClient.List(c.ctx, metav1.ListOptions{
		LabelSelector: sdSecret.Selector,
	})
	if err != nil {
		return nil, err
	}

	secData := make(map[string][]byte)
	for _, obj := range objList.Items {
		for file, data := range obj.Data {
			secData[secretKeyFunc(obj.Namespace, obj.Name, file)] = data
		}
	}
	return secData, nil
}

func (c *Operator) getPromSDSecretData(sdSecret configs.PromSDSecret) (map[string][]byte, error) {
	if !sdSecret.Validate() {
		return nil, fmt.Errorf("invalid sdconfig (%#v)", sdSecret)
	}

	if len(sdSecret.Name) > 0 {
		return c.getPromSDSecretDataByName(sdSecret)
	}
	return c.getPromSDSecretDataBySelector(sdSecret)
}

func secretKeyFunc(namespace, name, file string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, name, file)
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

func castDuration(d model.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func (c *Operator) createHttpLikeSdDiscover(rsc resourceScrapConfig, sdConfig interface{}, kind string, index int) (discover.Discover, error) {
	metricRelabelings := make([]yaml.MapSlice, 0)
	if len(rsc.conf.MetricRelabelConfigs) != 0 {
		for _, cfg := range rsc.conf.MetricRelabelConfigs {
			relabeling := generatePromRelabelConfig(cfg)
			metricRelabelings = append(metricRelabelings, relabeling)
		}
	}

	monitorMeta := define.MonitorMeta{
		Name:      fmt.Sprintf("%s/%s", rsc.resource, rsc.conf.JobName),
		Kind:      kind,
		Namespace: "-", // 不标记 namespace
		Index:     index,
	}
	// 默认使用 custommetric dataid
	dataID, err := c.dw.MatchMetricDataID(monitorMeta, false)
	if err != nil {
		return nil, err
	}

	specLabels := dataID.Spec.Labels
	httpClientConfig := rsc.conf.HTTPClientConfig

	var proxyURL string
	if httpClientConfig.ProxyURL.URL != nil {
		proxyURL = httpClientConfig.ProxyURL.String()
	}

	var bearerTokenFile string
	auth := httpClientConfig.Authorization
	if auth != nil && auth.Type == "Bearer" {
		bearerTokenFile = auth.CredentialsFile
	}

	commonOpts := &discover.CommonOptions{
		MonitorMeta:            monitorMeta,
		Name:                   monitorMeta.ID(),
		DataID:                 dataID,
		Relabels:               rsc.conf.RelabelConfigs,
		Path:                   rsc.conf.MetricsPath,
		Scheme:                 rsc.conf.Scheme,
		BearerTokenFile:        bearerTokenFile,
		ProxyURL:               proxyURL,
		Period:                 castDuration(rsc.conf.ScrapeInterval),
		Timeout:                castDuration(rsc.conf.ScrapeTimeout),
		DisableCustomTimestamp: !ifHonorTimestamps(&rsc.conf.HonorTimestamps),
		UrlValues:              rsc.conf.Params,
		ExtraLabels:            specLabels,
		MetricRelabelConfigs:   metricRelabelings,
	}

	var dis discover.Discover
	switch kind {
	case monitorKindHttpSd:
		dis = httpsd.New(c.ctx, c.objectsController.NodeNameExists, &httpsd.Options{
			CommonOptions:    commonOpts,
			SDConfig:         sdConfig.(*promhttpsd.SDConfig),
			HTTPClientConfig: httpClientConfig,
		})
	case monitorKindPolarisSd:
		dis = polarissd.New(c.ctx, c.objectsController.NodeNameExists, &polarissd.Options{
			CommonOptions:    commonOpts,
			SDConfig:         sdConfig.(*polarissd.SDConfig),
			HTTPClientConfig: httpClientConfig,
		})
	default:
		return nil, fmt.Errorf("unsupported kind '%s'", kind)
	}

	logger.Infof("create %s discover: %s", kind, dis.Name())
	return dis, nil
}

func (c *Operator) createKubernetesSdDiscover(rsc resourceScrapConfig, sdConfig *promk8ssd.SDConfig, index int) (discover.Discover, error) {
	metricRelabelings := make([]yaml.MapSlice, 0)
	if len(rsc.conf.MetricRelabelConfigs) != 0 {
		for _, cfg := range rsc.conf.MetricRelabelConfigs {
			relabeling := generatePromRelabelConfig(cfg)
			metricRelabelings = append(metricRelabelings, relabeling)
		}
	}

	monitorMeta := define.MonitorMeta{
		Name:      fmt.Sprintf("%s/%s", rsc.resource, rsc.conf.JobName),
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
	httpClientConfig := rsc.conf.HTTPClientConfig

	var proxyURL string
	if httpClientConfig.ProxyURL.URL != nil {
		proxyURL = httpClientConfig.ProxyURL.String()
	}

	var bearerTokenFile string
	auth := httpClientConfig.Authorization
	if auth != nil && auth.Type == "Bearer" {
		bearerTokenFile = auth.CredentialsFile
	}

	var username, password string
	basicAuth := rsc.conf.HTTPClientConfig.BasicAuth
	if basicAuth != nil {
		username = basicAuth.Username
		password = string(basicAuth.Password)
	}

	dis := kubernetesd.New(c.ctx, string(sdConfig.Role), c.objectsController.NodeNameExists, &kubernetesd.Options{
		CommonOptions: &discover.CommonOptions{
			MonitorMeta:            monitorMeta,
			Name:                   monitorMeta.ID(),
			DataID:                 dataID,
			Relabels:               rsc.conf.RelabelConfigs,
			Path:                   rsc.conf.MetricsPath,
			Scheme:                 rsc.conf.Scheme,
			BearerTokenFile:        bearerTokenFile,
			ProxyURL:               proxyURL,
			Period:                 castDuration(rsc.conf.ScrapeInterval),
			Timeout:                castDuration(rsc.conf.ScrapeTimeout),
			DisableCustomTimestamp: !ifHonorTimestamps(&rsc.conf.HonorTimestamps),
			UrlValues:              rsc.conf.Params,
			ExtraLabels:            specLabels,
			MetricRelabelConfigs:   metricRelabelings,
		},
		KubeConfig: configs.G().KubeConfig,
		Namespaces: sdConfig.NamespaceDiscovery.Names,
		Client:     c.client,
		BasicAuthRaw: kubernetesd.BasicAuthRaw{
			Username: username,
			Password: password,
		},
		TLSConfig: &promv1.TLSConfig{
			CAFile:   rsc.conf.HTTPClientConfig.TLSConfig.CAFile,
			CertFile: rsc.conf.HTTPClientConfig.TLSConfig.CertFile,
			KeyFile:  rsc.conf.HTTPClientConfig.TLSConfig.KeyFile,
		},
		UseEndpointSlice: useEndpointslice,
	})
	logger.Infof("create kubernetes_sd discover: %s", dis.Name())

	return dis, nil
}

const (
	opAddOrUpdate = "addOrUpdate"
	opRemove      = "remove"
)

func (c *Operator) reloadPromScrapeConfigDiscovers() {
	resourceScrapeConfigs, ok := c.getPromScrapeConfigs()
	if !ok {
		return
	}

	newRound := make(map[string]resourceScrapConfig)
	for _, sc := range resourceScrapeConfigs {
		uid := fmt.Sprintf("%s/%s", sc.resource, sc.conf.JobName)
		_, ok := newRound[uid]
		if ok {
			logger.Errorf("found duplicate scrapeConfig: '%s'", uid)
			continue
		}
		newRound[uid] = sc
	}

	var addOrUpdateScrapeConfigs []resourceScrapConfig
	var removeScrapeConfigs []resourceScrapConfig
	for _, sc := range resourceScrapeConfigs {
		uid := fmt.Sprintf("%s/%s", sc.resource, sc.conf.JobName)
		v, ok := c.prevScrapeConfigs[uid]
		if !ok {
			addOrUpdateScrapeConfigs = append(addOrUpdateScrapeConfigs, sc) // 新增
			continue
		}
		if !reflect.DeepEqual(v, sc) {
			addOrUpdateScrapeConfigs = append(addOrUpdateScrapeConfigs, sc) // 修改
		}
	}

	for uid := range c.prevScrapeConfigs {
		v, ok := newRound[uid]
		if !ok {
			removeScrapeConfigs = append(removeScrapeConfigs, v) // 删除
		}
	}

	if len(addOrUpdateScrapeConfigs) > 0 {
		logger.Infof("promsd add or update %d scrapeConfigs", len(addOrUpdateScrapeConfigs))
		c.handlePromScrapeConfigDiscovers(addOrUpdateScrapeConfigs, opAddOrUpdate)
	}
	if len(removeScrapeConfigs) > 0 {
		logger.Infof("promsd remove %d scrapeConfigs", len(removeScrapeConfigs))
		c.handlePromScrapeConfigDiscovers(removeScrapeConfigs, opRemove)
	}
	c.prevScrapeConfigs = newRound
}

func (c *Operator) handlePromScrapeConfigDiscovers(resourceScrapeConfigs []resourceScrapConfig, op string) {
	var discovers []discover.Discover
	kinds := configs.G().PromSDKinds
	for i := 0; i < len(resourceScrapeConfigs); i++ {
		rsc := resourceScrapeConfigs[i]
		for idx, rc := range rsc.conf.ServiceDiscoveryConfigs {
			switch obj := rc.(type) {
			case *promhttpsd.SDConfig:
				if !kinds.Allow(monitorKindHttpSd) {
					continue
				}

				sd, err := c.createHttpLikeSdDiscover(rsc, obj, monitorKindHttpSd, idx)
				if err != nil {
					logger.Errorf("failed to create http_sd discover: %v", err)
					continue
				}
				discovers = append(discovers, sd)

			case *polarissd.SDConfig:
				if !kinds.Allow(monitorKindPolarisSd) {
					continue
				}

				sd, err := c.createHttpLikeSdDiscover(rsc, obj, monitorKindPolarisSd, idx)
				if err != nil {
					logger.Errorf("failed to create polaris_sd discover: %v", err)
					continue
				}
				discovers = append(discovers, sd)

			case *promk8ssd.SDConfig:
				if !kinds.Allow(monitorKindKubernetesSd) {
					continue
				}

				sd, err := c.createKubernetesSdDiscover(rsc, obj, idx)
				if err != nil {
					logger.Errorf("failed to create kubernetes_sd discover: %v", err)
					continue
				}
				discovers = append(discovers, sd)
			}
		}
	}

	switch op {
	case opAddOrUpdate:
		for _, dis := range discovers {
			if err := c.addOrUpdateDiscover(dis); err != nil {
				logger.Errorf("add or update prom scrapeConfigs discover %s failed: %s", dis, err)
			}
		}
	case opDelete:
		for _, dis := range discovers {
			c.deleteDiscoverByName(dis.Name())
		}
	}
}

func (c *Operator) loopHandlePromSdConfigs() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	c.reloadPromScrapeConfigDiscovers() // 启动即执行

	for {
		select {
		case <-c.ctx.Done():
			return

		case <-ticker.C:
			c.reloadPromScrapeConfigDiscovers()
		}
	}
}
