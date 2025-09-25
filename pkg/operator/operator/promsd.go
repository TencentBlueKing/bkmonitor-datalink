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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/etcdsd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/httpsd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/kubernetesd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/polarissd"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type resourceScrapConfig struct {
	Namespace string
	Resource  string
	Config    config.ScrapeConfig
}

func (c *Operator) getPromResourceScrapeConfigs() ([]resourceScrapConfig, bool) {
	if len(configs.G().PromSDSecrets) == 0 {
		return nil, false
	}

	var rscs []resourceScrapConfig
	newRound := make(map[SecretKey][]byte) // 本轮获取到的数据
	for _, secret := range configs.G().PromSDSecrets {
		secData, err := c.getPromSDSecretData(secret)
		if err != nil {
			logger.Errorf("get secrets sesource failed, config=(%+v): %v", secret, err)
			continue
		}

		for resource, data := range secData {
			sdc, err := unmarshalPromSdConfigs(data)
			if err != nil {
				logger.Errorf("unmarshal prom sdconfigs failed, resource=(%+v): %v", resource, err)
				continue
			}

			newRound[resource] = data
			for i := 0; i < len(sdc); i++ {
				rscs = append(rscs, resourceScrapConfig{
					Namespace: resource.Namespace,
					Resource:  resource.Key(),
					Config:    sdc[i],
				})
			}
		}
	}

	eq := reflect.DeepEqual(c.promSdConfigsBytes, newRound) // 对比是否需要更新操作
	c.promSdConfigsBytes = newRound
	return rscs, !eq // changed
}

func (c *Operator) getPromSDSecretDataByName(sdSecret configs.PromSDSecret) (map[SecretKey][]byte, error) {
	secretClient := c.client.CoreV1().Secrets(sdSecret.Namespace)
	obj, err := secretClient.Get(c.ctx, sdSecret.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	secData := make(map[SecretKey][]byte)
	for file, data := range obj.Data {
		secData[SecretKey{
			Namespace: sdSecret.Namespace,
			Name:      sdSecret.Name,
			File:      file,
		}] = data
	}
	return secData, nil
}

func (c *Operator) getPromSDSecretDataBySelector(sdSecret configs.PromSDSecret) (map[SecretKey][]byte, error) {
	secretClient := c.client.CoreV1().Secrets(sdSecret.Namespace)
	objList, err := secretClient.List(c.ctx, metav1.ListOptions{
		LabelSelector: sdSecret.Selector,
	})
	if err != nil {
		return nil, err
	}

	secData := make(map[SecretKey][]byte)
	for _, obj := range objList.Items {
		for file, data := range obj.Data {
			secData[SecretKey{
				Namespace: obj.Namespace,
				Name:      obj.Name,
				File:      file,
			}] = data
		}
	}
	return secData, nil
}

func (c *Operator) getPromSDSecretData(sdSecret configs.PromSDSecret) (map[SecretKey][]byte, error) {
	if !sdSecret.Validate() {
		return nil, fmt.Errorf("invalid sdconfig (%#v)", sdSecret)
	}

	if len(sdSecret.Name) > 0 {
		return c.getPromSDSecretDataByName(sdSecret)
	}
	return c.getPromSDSecretDataBySelector(sdSecret)
}

type SecretKey struct {
	Namespace string
	Name      string
	File      string
}

func (sk SecretKey) Key() string {
	return fmt.Sprintf("%s/%s", sk.Name, sk.File)
}

func unmarshalPromSdConfigs(b []byte) ([]config.ScrapeConfig, error) {
	var objs []any
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

func (c *Operator) createHttpLikeSdDiscover(rsc resourceScrapConfig, sdConfig any, kind string, index int) (discover.Discover, error) {
	metricRelabelings := make([]yaml.MapSlice, 0)
	if len(rsc.Config.MetricRelabelConfigs) != 0 {
		for _, cfg := range rsc.Config.MetricRelabelConfigs {
			relabeling := generatePromRelabelConfig(cfg)
			metricRelabelings = append(metricRelabelings, relabeling)
		}
	}

	monitorMeta := define.MonitorMeta{
		Name:      fmt.Sprintf("%s/%s", rsc.Resource, rsc.Config.JobName),
		Kind:      kind,
		Namespace: rsc.Namespace,
		Index:     index,
	}
	// 默认使用 custommetric dataid
	dataID, err := c.dw.MatchMetricDataID(monitorMeta, false)
	if err != nil {
		return nil, err
	}

	specLabels := dataID.Spec.Labels
	httpClientConfig := rsc.Config.HTTPClientConfig

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
		Relabels:               rsc.Config.RelabelConfigs,
		Path:                   rsc.Config.MetricsPath,
		Scheme:                 rsc.Config.Scheme,
		BearerTokenFile:        bearerTokenFile,
		ProxyURL:               proxyURL,
		Period:                 castDuration(rsc.Config.ScrapeInterval),
		Timeout:                castDuration(rsc.Config.ScrapeTimeout),
		DisableCustomTimestamp: !ifHonorTimestamps(&rsc.Config.HonorTimestamps),
		UrlValues:              rsc.Config.Params,
		ExtraLabels:            specLabels,
		MetricRelabelConfigs:   metricRelabelings,
		CheckNodeNameFunc:      c.objectsController.CheckNodeName,
		NodeLabelsFunc:         c.objectsController.NodeLabels,
	}

	var dis discover.Discover
	switch kind {
	case monitorKindHttpSd:
		dis = httpsd.New(c.ctx, &httpsd.Options{
			CommonOptions:    commonOpts,
			SDConfig:         sdConfig.(*promhttpsd.SDConfig),
			HTTPClientConfig: httpClientConfig,
		})
	case monitorKindPolarisSd:
		dis = polarissd.New(c.ctx, &polarissd.Options{
			CommonOptions:    commonOpts,
			SDConfig:         sdConfig.(*polarissd.SDConfig),
			HTTPClientConfig: httpClientConfig,
		})
	case monitorKindEtcdSd:
		sdc := sdConfig.(*etcdsd.SDConfig)
		sdc.IPFilter = func(s string) bool {
			return c.objectsController.CheckPodIP(s) || c.objectsController.CheckNodeIP(s)
		}
		dis = etcdsd.New(c.ctx, &etcdsd.Options{
			CommonOptions:    commonOpts,
			SDConfig:         sdc,
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
	if len(rsc.Config.MetricRelabelConfigs) != 0 {
		for _, cfg := range rsc.Config.MetricRelabelConfigs {
			relabeling := generatePromRelabelConfig(cfg)
			metricRelabelings = append(metricRelabelings, relabeling)
		}
	}

	monitorMeta := define.MonitorMeta{
		Name:      fmt.Sprintf("%s/%s", rsc.Resource, rsc.Config.JobName),
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
	httpClientConfig := rsc.Config.HTTPClientConfig

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
	basicAuth := rsc.Config.HTTPClientConfig.BasicAuth
	if basicAuth != nil {
		username = basicAuth.Username
		password = string(basicAuth.Password)
	}

	dis := kubernetesd.New(c.ctx, string(sdConfig.Role), &kubernetesd.Options{
		CommonOptions: &discover.CommonOptions{
			MonitorMeta:            monitorMeta,
			Name:                   monitorMeta.ID(),
			DataID:                 dataID,
			Relabels:               rsc.Config.RelabelConfigs,
			Path:                   rsc.Config.MetricsPath,
			Scheme:                 rsc.Config.Scheme,
			BearerTokenFile:        bearerTokenFile,
			ProxyURL:               proxyURL,
			Period:                 castDuration(rsc.Config.ScrapeInterval),
			Timeout:                castDuration(rsc.Config.ScrapeTimeout),
			DisableCustomTimestamp: !ifHonorTimestamps(&rsc.Config.HonorTimestamps),
			UrlValues:              rsc.Config.Params,
			ExtraLabels:            specLabels,
			MetricRelabelConfigs:   metricRelabelings,
			CheckNodeNameFunc:      c.objectsController.CheckNodeName,
			NodeLabelsFunc:         c.objectsController.NodeLabels,
		},
		KubeConfig: configs.G().KubeConfig,
		Namespaces: sdConfig.NamespaceDiscovery.Names,
		Client:     c.client,
		BasicAuthRaw: kubernetesd.BasicAuthRaw{
			Username: username,
			Password: password,
		},
		TLSConfig: &promv1.TLSConfig{
			CAFile:   rsc.Config.HTTPClientConfig.TLSConfig.CAFile,
			CertFile: rsc.Config.HTTPClientConfig.TLSConfig.CertFile,
			KeyFile:  rsc.Config.HTTPClientConfig.TLSConfig.KeyFile,
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
	resourceScrapeConfigs, ok := c.getPromResourceScrapeConfigs()
	if !ok {
		return
	}

	newRound := make(map[string]resourceScrapConfig)
	for _, sc := range resourceScrapeConfigs {
		uid := fmt.Sprintf("%s/%s", sc.Resource, sc.Config.JobName)
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
		uid := fmt.Sprintf("%s/%s", sc.Resource, sc.Config.JobName)
		v, ok := c.prevResourceScrapeConfigs[uid]
		if !ok {
			addOrUpdateScrapeConfigs = append(addOrUpdateScrapeConfigs, sc) // 新增
			logger.Infof("promsd add (%s) scrapeConfig", uid)
			continue
		}
		if !reflect.DeepEqual(v, sc) {
			addOrUpdateScrapeConfigs = append(addOrUpdateScrapeConfigs, sc) // 修改
			logger.Infof("promsd update (%s) scrapeConfig", uid)
		}
	}

	for uid, sc := range c.prevResourceScrapeConfigs {
		_, ok := newRound[uid]
		if !ok {
			removeScrapeConfigs = append(removeScrapeConfigs, sc) // 删除
			logger.Infof("promsd remove (%s) scrapeConfig", uid)
		}
	}

	// 模拟 monitor 资源监听变化
	if len(addOrUpdateScrapeConfigs) > 0 {
		c.handlePromScrapeConfigDiscovers(addOrUpdateScrapeConfigs, opAddOrUpdate)
	}
	if len(removeScrapeConfigs) > 0 {
		c.handlePromScrapeConfigDiscovers(removeScrapeConfigs, opRemove)
	}
	c.prevResourceScrapeConfigs = newRound
}

func (c *Operator) handlePromScrapeConfigDiscovers(resourceScrapeConfigs []resourceScrapConfig, op string) {
	var discovers []discover.Discover
	kinds := configs.G().PromSDKinds
	for i := 0; i < len(resourceScrapeConfigs); i++ {
		rsc := resourceScrapeConfigs[i]
		for idx, rc := range rsc.Config.ServiceDiscoveryConfigs {
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

			case *etcdsd.SDConfig:
				if !kinds.Allow(monitorKindEtcdSd) {
					continue
				}
				sd, err := c.createHttpLikeSdDiscover(rsc, obj, monitorKindEtcdSd, idx)
				if err != nil {
					logger.Errorf("failed to create etcd_sd discover: %v", err)
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
	case opRemove:
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
