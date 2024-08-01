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
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	tkexversiond "github.com/Tencent/bk-bcs/bcs-scenarios/kourse/pkg/client/clientset/versioned"
	"github.com/blang/semver/v4"
	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promversioned "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	prominformers "github.com/prometheus-operator/prometheus-operator/pkg/informers"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/dataidwatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/promsli"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	monitorKindServiceMonitor = "ServiceMonitor"
	monitorKindPodMonitor     = "PodMonitor"
)

var (
	kubernetesVersion      string
	endpointSliceSupported bool
)

// Operator 负责部署和调度任务
type Operator struct {
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	recorder  *Recorder
	mm        *metricMonitor
	buildInfo BuildInfo

	client     kubernetes.Interface
	promclient promversioned.Interface
	bkclient   bkversioned.Interface
	tkexclient tkexversiond.Interface
	srv        *http.Server

	serviceMonitorInformer *prominformers.ForResource
	podMonitorInformer     *prominformers.ForResource

	promRuleInformer  *prominformers.ForResource
	promsliController *promsli.Controller

	statefulSetWorkerScaled time.Time
	statefulSetWorker       int
	statefulSetSecretMap    map[string]struct{}
	statefulSetSecretMut    sync.Mutex

	dw           dataidwatcher.Watcher
	discoversMut sync.RWMutex
	discovers    map[string]discover.Discover

	objectsController *objectsref.ObjectsController

	daemonSetTaskCache   map[string]map[string]struct{}
	statefulSetTaskCache map[int]map[string]struct{}
	eventTaskCache       string
	scrapeUpdated        time.Time
}

func NewOperator(ctx context.Context, buildInfo BuildInfo) (*Operator, error) {
	var (
		operator = new(Operator)
		err      error
	)

	operator.buildInfo = buildInfo
	operator.ctx, operator.cancel = context.WithCancel(ctx)
	if err = os.Setenv("KUBECONFIG", ConfKubeConfig); err != nil {
		return nil, err
	}

	operator.client, err = k8sutils.NewK8SClient(ConfAPIServerHost, ConfTLSConfig)
	if err != nil {
		return nil, err
	}

	operator.promclient, err = k8sutils.NewPromClient(ConfAPIServerHost, ConfTLSConfig)
	if err != nil {
		return nil, err
	}

	operator.bkclient, err = k8sutils.NewBKClient(ConfAPIServerHost, ConfTLSConfig)
	if err != nil {
		return nil, err
	}

	operator.tkexclient, err = k8sutils.NewTkexClient(ConfAPIServerHost, ConfTLSConfig)
	if err != nil {
		return nil, err
	}

	operator.discovers = make(map[string]discover.Discover)
	allNamespaces := map[string]struct{}{}
	if len(ConfTargetNamespaces) == 0 {
		allNamespaces = map[string]struct{}{corev1.NamespaceAll: {}}
	} else {
		for _, namespace := range ConfTargetNamespaces {
			allNamespaces[namespace] = struct{}{}
		}
	}

	denyTargetNamespaces := make(map[string]struct{})
	for _, namespace := range ConfDenyTargetNamespaces {
		denyTargetNamespaces[namespace] = struct{}{}
	}

	if ConfEnableServiceMonitor {
		operator.serviceMonitorInformer, err = prominformers.NewInformersForResource(
			prominformers.NewMonitoringInformerFactories(
				allNamespaces,
				denyTargetNamespaces,
				operator.promclient,
				define.ReSyncPeriod,
				func(options *metav1.ListOptions) {
					options.LabelSelector = ConfTargetLabelsSelector
				},
			),
			promv1.SchemeGroupVersion.WithResource(promv1.ServiceMonitorName),
		)
		if err != nil {
			return nil, errors.Wrap(err, "create ServiceMonitor informer failed")
		}
	}

	if ConfEnablePodMonitor {
		operator.podMonitorInformer, err = prominformers.NewInformersForResource(
			prominformers.NewMonitoringInformerFactories(
				allNamespaces,
				denyTargetNamespaces,
				operator.promclient,
				define.ReSyncPeriod,
				func(options *metav1.ListOptions) {
					options.LabelSelector = ConfTargetLabelsSelector
				},
			),
			promv1.SchemeGroupVersion.WithResource(promv1.PodMonitorName),
		)
		if err != nil {
			return nil, errors.Wrap(err, "create PodMonitor informer failed")
		}
	}

	if ConfEnablePromRule {
		operator.promRuleInformer, err = prominformers.NewInformersForResource(
			prominformers.NewMonitoringInformerFactories(
				map[string]struct{}{corev1.NamespaceAll: {}},
				map[string]struct{}{},
				operator.promclient,
				resyncPeriod,
				nil,
			),
			promv1.SchemeGroupVersion.WithResource(promv1.PrometheusRuleName),
		)
		if err != nil {
			return nil, errors.Wrap(err, "create PrometheusRule informer failed")
		}
		operator.promsliController = promsli.NewController(operator.ctx, operator.client)
	}

	operator.objectsController, err = objectsref.NewController(operator.ctx, operator.client, operator.tkexclient)
	if err != nil {
		return nil, errors.Wrap(err, "create objectsController failed")
	}

	operator.recorder = NewRecorder()
	operator.dw = dataidwatcher.New(operator.ctx, operator.bkclient)
	operator.mm = newMetricMonitor()
	operator.statefulSetSecretMap = map[string]struct{}{}

	version, err := operator.client.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	kubernetesVersion = version.String()
	operator.mm.SetKubernetesVersion(kubernetesVersion)

	parsedVersion, err := semver.ParseTolerant(kubernetesVersion)
	if err != nil {
		parsedVersion = semver.MustParse("1.16.0")
		logger.Errorf("parse kubernetes version failed, instead of '%v', err: %v", parsedVersion, err)
	}

	// 1.21.0 开始 endpointslice 正式成为 v1
	endpointSliceSupported = parsedVersion.GTE(semver.MustParse("1.21.0"))
	logger.Infof("kubernetesVersion=%s, endpointSliceSupported=%v", kubernetesVersion, endpointSliceSupported)

	return operator, nil
}

func (c *Operator) getAllDiscover() []define.MonitorMeta {
	var ret []define.MonitorMeta
	c.discoversMut.Lock()
	for _, dis := range c.discovers {
		ret = append(ret, dis.MonitorMeta())
	}
	c.discoversMut.Unlock()
	return ret
}

func (c *Operator) reloadAllDiscovers() {
	c.discoversMut.Lock()
	defer c.discoversMut.Unlock()

	for name, dis := range c.discovers {
		meta := dis.MonitorMeta()
		newDataID, err := c.dw.MatchMetricDataID(meta, dis.IsSystem())
		if err != nil {
			logger.Errorf("no dataid found, meta=%+v, discover=%s", meta, dis)
			continue
		}
		if dis.DataID() == newDataID {
			continue
		}

		dis.SetDataID(newDataID)
		if err := dis.Reload(); err != nil {
			logger.Errorf("discover %s reload failed, err: %s", name, err)
		}
	}
}

func (c *Operator) recordMetrics() {
	c.wg.Add(1)
	defer c.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mm.UpdateUptime(5)
			c.mm.SetAppBuildInfo(c.buildInfo)
			c.updateNodeConfigMetrics()
			c.updateMonitorEndpointMetrics()
			c.updateWorkloadMetrics()
			c.updateNodeMetrics()
			c.updateSharedDiscoveryMetrics()

		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Operator) updateSharedDiscoveryMetrics() {
	c.mm.SetSharedDiscoveryCount(discover.GetSharedDiscoveryCount())
	c.mm.SetDiscoverCount(len(c.getAllDiscover()))
}

func (c *Operator) updateNodeConfigMetrics() {
	cfgs := c.recorder.getActiveConfigFiles()
	set := make(map[string]int)
	for _, cfg := range cfgs {
		set[cfg.Node]++
	}

	for k, v := range set {
		c.mm.SetNodeConfigCount(k, v)
	}
}

func (c *Operator) updateMonitorEndpointMetrics() {
	endpoints := c.recorder.getActiveEndpoints()
	for name, count := range endpoints {
		c.mm.SetMonitorEndpointCount(name, count)
	}
}

func (c *Operator) updateWorkloadMetrics() {
	workloads, _ := objectsref.GetWorkloadInfo()
	for resource, count := range workloads {
		c.mm.SetWorkloadCount(resource, count)
	}
}

func (c *Operator) updateNodeMetrics() {
	nodes, _ := objectsref.GetClusterNodeInfo()
	c.mm.SetNodeCount(nodes)
}

func (c *Operator) Run() error {
	discover.Activate()
	errChan := make(chan error, 2)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		_, err := c.client.Discovery().ServerVersion()
		if err != nil {
			errChan <- errors.Wrap(err, "communicating with server failed")
			return
		}
		errChan <- nil
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.ListenAndServe(); err != nil {
			errChan <- err
		}
	}()

	go c.recordMetrics()

	// 与 apiserver 进行一次通信，如果通信失败，则判断为启动失败
	select {
	case err := <-errChan:
		if err != nil {
			return err
		}
	case <-c.ctx.Done():
		return nil
	}

	if err := c.dw.Start(); err != nil {
		return err
	}

	// 等待 dataid watcher 初始化结束，否则后续触发 discover 更新可能会得到错误的 dataid
	<-dataidwatcher.Notify()

	go c.handleDiscoverNotify()
	go c.handleDataIDNotify()

	if ConfEnableServiceMonitor {
		c.serviceMonitorInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handleServiceMonitorAdd,
			UpdateFunc: c.handleServiceMonitorUpdate,
			DeleteFunc: c.handleServiceMonitorDelete,
		})
		c.serviceMonitorInformer.Start(c.ctx.Done())
	}

	if ConfEnablePodMonitor {
		c.podMonitorInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handlePodMonitorAdd,
			UpdateFunc: c.handlePodMonitorUpdate,
			DeleteFunc: c.handlePodMonitorDelete,
		})
		c.podMonitorInformer.Start(c.ctx.Done())
	}

	if ConfEnablePromRule {
		c.promRuleInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handlePrometheusRuleAdd,
			UpdateFunc: c.handlePrometheusRuleUpdate,
			DeleteFunc: c.handlePrometheusRuleDelete,
		})
		c.promRuleInformer.Start(c.ctx.Done())
	}

	if err := c.waitForCacheSync(c.ctx); err != nil {
		return err
	}

	// 如果启动了 StatefulSetWorker 则需要监听 statefulset secrets 的变化以及 statefulset 本身的变化
	// 该资源只存在于 ConfMonitorNamespace namespace
	if ConfEnableStatefulSetWorker {
		if err := c.listWatchStatefulSetWorker(); err != nil {
			return err
		}
		if err := c.listWatchStatefulSetSecrets(); err != nil {
			return err
		}
	}

	if ConfKubeletEnable {
		go c.reconcileNodeEndpoints(c.ctx)
	}

	c.cleanupInvalidSecrets()
	return nil
}

func (c *Operator) cleanupInvalidSecrets() {
	secretClient := c.client.CoreV1().Secrets(ConfMonitorNamespace)
	secrets, err := secretClient.List(c.ctx, metav1.ListOptions{
		LabelSelector: "createdBy=bkmonitor-operator",
	})
	if err != nil {
		logger.Errorf("failed to list secrets, err: %v", err)
		return
	}

	// 清理不合法的 secrets
	for _, secret := range secrets.Items {
		if _, ok := secret.Labels[tasks.LabelTaskType]; !ok {
			if err := secretClient.Delete(c.ctx, secret.Name, metav1.DeleteOptions{}); err != nil {
				c.mm.IncHandledSecretFailedCounter(secret.Name, define.ActionDelete)
				logger.Errorf("failed to delete secret %s, err: %v", secret.Name, err)
				continue
			}
			c.mm.IncHandledSecretSuccessCounter(secret.Name, define.ActionDelete)
			logger.Infof("remove invalid secret %s", secret.Name)
		}
	}
}

func (c *Operator) Stop() {
	c.cancel()
	if err := c.srv.Shutdown(context.Background()); err != nil {
		logger.Errorf("shouting down srv error: %v", err)
	}
	c.wg.Wait()

	c.dw.Stop()
	c.objectsController.Stop()
	discover.Deactivate()
}

// waitForCacheSync waits for the informers' caches to be synced.
func (c *Operator) waitForCacheSync(ctx context.Context) error {
	ok := true

	for _, infs := range []struct {
		name                 string
		informersForResource *prominformers.ForResource
	}{
		{"ServiceMonitor", c.serviceMonitorInformer},
		{"PodMonitor", c.podMonitorInformer},
		{"PrometheusRule", c.promRuleInformer},
	} {
		// 跳过没有初始化的 informers
		if infs.informersForResource == nil {
			continue
		}
		for _, inf := range infs.informersForResource.GetInformers() {
			if !k8sutils.WaitForNamedCacheSync(ctx, infs.name, inf.Informer()) {
				ok = false
			}
		}
	}

	if !ok {
		return errors.New("failed to sync Monitor caches")
	}

	return nil
}

// addOrUpdateDiscover 更新 discover 先停后启
func (c *Operator) addOrUpdateDiscover(discover discover.Discover) error {
	c.discoversMut.Lock()
	defer c.discoversMut.Unlock()

	if oldDiscover, ok := c.discovers[discover.Name()]; ok {
		oldDiscover.Stop()
		delete(c.discovers, discover.Name())
	}

	if err := discover.Start(); err != nil {
		return err
	}
	c.discovers[discover.Name()] = discover
	return nil
}

// deleteDiscoverByName 删除 discover
func (c *Operator) deleteDiscoverByName(name string) {
	c.discoversMut.Lock()
	defer c.discoversMut.Unlock()

	if oldDiscover, ok := c.discovers[name]; ok {
		oldDiscover.Stop()
		delete(c.discovers, name)
		logger.Infof("delete discover %s", name)
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

func ifHonorTimestamps(b *bool) bool {
	if b == nil {
		return true
	}
	return *b
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
	dataID, err := c.dw.MatchMetricDataID(meta, systemResource)
	if err != nil {
		logger.Errorf("meta=%v found no dataid", meta)
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

	logger.Infof("get serviceMonitor, name=%s, namespace=%s", serviceMonitor.Name, serviceMonitor.Namespace)
	for index, endpoint := range serviceMonitor.Spec.Endpoints {
		if endpoint.Path == "" {
			endpoint.Path = "/metrics"
		}
		if endpoint.Scheme == "" {
			endpoint.Scheme = "http"
		}

		relabels := getServiceMonitorRelabels(serviceMonitor, &endpoint)
		resultLabels, err := convertYamlRelabels(relabels)
		if err != nil {
			logger.Errorf("failed to convert relabels, err: %s", err)
			continue
		}

		metricRelabelings := make([]yaml.MapSlice, 0)
		if len(endpoint.MetricRelabelConfigs) != 0 {
			for _, cfg := range endpoint.MetricRelabelConfigs {
				relabeling := generateRelabelConfig(cfg)
				metricRelabelings = append(metricRelabelings, relabeling)
			}
		}
		logger.Debugf("serviceMonitor %s get relabels config: %+v", serviceMonitor.Name, relabels)

		monitorMeta := meta
		monitorMeta.Index = index

		var proxyURL string
		if endpoint.ProxyURL != nil {
			proxyURL = *endpoint.ProxyURL
		}

		endpointDiscover := discover.NewEndpointDiscover(c.ctx, monitorMeta, c.objectsController.NodeNameExists, &discover.EndpointParams{
			BaseParams: &discover.BaseParams{
				Client:                 c.client,
				RelabelRule:            feature.RelabelRule(serviceMonitor.Annotations),
				RelabelIndex:           feature.RelabelIndex(serviceMonitor.Annotations),
				NormalizeMetricName:    feature.IfNormalizeMetricName(serviceMonitor.Annotations),
				AntiAffinity:           feature.IfAntiAffinity(serviceMonitor.Annotations),
				MatchSelector:          feature.MonitorMatchSelector(serviceMonitor.Annotations),
				DropSelector:           feature.MonitorDropSelector(serviceMonitor.Annotations),
				EndpointSliceSupported: endpointSliceSupported,
				Name:                   monitorMeta.ID(),
				DataID:                 dataID,
				KubeConfig:             ConfKubeConfig,
				Namespaces:             namespaces,
				Relabels:               resultLabels,
				Path:                   endpoint.Path,
				Scheme:                 endpoint.Scheme,
				TLSConfig:              endpoint.TLSConfig.DeepCopy(),
				BasicAuth:              endpoint.BasicAuth.DeepCopy(),
				BearerTokenFile:        endpoint.BearerTokenFile,
				BearerTokenSecret:      endpoint.BearerTokenSecret.DeepCopy(),
				Period:                 string(endpoint.Interval),
				ProxyURL:               proxyURL,
				Timeout:                string(endpoint.ScrapeTimeout),
				ExtraLabels:            specLabels,
				ForwardLocalhost:       feature.IfForwardLocalhost(serviceMonitor.Annotations),
				DisableCustomTimestamp: !ifHonorTimestamps(endpoint.HonorTimestamps),
				System:                 systemResource,
				UrlValues:              endpoint.Params,
				MetricRelabelConfigs:   metricRelabelings,
			},
		})

		logger.Infof("get new endpoint discover %s", endpointDiscover)
		discovers = append(discovers, endpointDiscover)
	}
	return discovers
}

func (c *Operator) handleServiceMonitorAdd(obj interface{}) {
	serviceMonitor, ok := obj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", obj)
		return
	}

	if ConfEnablePromRule {
		c.promsliController.UpdateServiceMonitor(serviceMonitor)
	}

	// 新增的 servicemonitor 命中黑名单则流程终止
	if IfRejectServiceMonitor(serviceMonitor) {
		logger.Infof("add action match the blacklist rules, serviceMonitor=%+v", serviceMonitor)
		return
	}

	discovers := c.createServiceMonitorDiscovers(serviceMonitor)
	for _, dis := range discovers {
		if err := c.addOrUpdateDiscover(dis); err != nil {
			logger.Errorf("add or update serviceMonitor discover %s failed, err: %s", dis, err)
		}
	}
}

func (c *Operator) handleServiceMonitorUpdate(oldObj interface{}, newObj interface{}) {
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

	if ConfEnablePromRule {
		c.promsliController.UpdateServiceMonitor(cur)
	}

	if old.ResourceVersion == cur.ResourceVersion {
		logger.Debugf("serviceMonitor %+v does not change", old)
		return
	}

	// 对于更新的 servicemonitor 如果新的 spec 命中黑名单 则需要将原有的 servicemonitor 移除
	if IfRejectServiceMonitor(cur) {
		logger.Infof("update action match the blacklist rules, serviceMonitor=%+v", cur)
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
			logger.Errorf("add or update serviceMonitor discover %s failed, err: %s", dis, err)
		}
	}
}

func (c *Operator) handleServiceMonitorDelete(obj interface{}) {
	serviceMonitor, ok := obj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", obj)
		return
	}

	if ConfEnablePromRule {
		c.promsliController.DeleteServiceMonitor(serviceMonitor)
	}

	for _, name := range c.getServiceMonitorDiscoversName(serviceMonitor) {
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
	dataID, err := c.dw.MatchMetricDataID(meta, systemResource)
	if err != nil {
		logger.Errorf("meta=%v no dataid found", meta)
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

	logger.Infof("get podMonitor, name=%s, namespace=%s", podMonitor.Name, podMonitor.Namespace)
	for index, endpoint := range podMonitor.Spec.PodMetricsEndpoints {
		if endpoint.Path == "" {
			endpoint.Path = "/metrics"
		}
		if endpoint.Scheme == "" {
			endpoint.Scheme = "http"
		}

		relabels := getPodMonitorRelabels(podMonitor, &endpoint)
		resultLabels, err := convertYamlRelabels(relabels)
		if err != nil {
			logger.Errorf("failed to convert relabels, err: %s", err)
			continue
		}

		metricRelabelings := make([]yaml.MapSlice, 0)
		if len(endpoint.MetricRelabelConfigs) != 0 {
			for _, cfg := range endpoint.MetricRelabelConfigs {
				relabeling := generateRelabelConfig(cfg)
				metricRelabelings = append(metricRelabelings, relabeling)
			}
		}

		logger.Debugf("podMonitor %s get relabels: %v", podMonitor.Name, relabels)

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
		podDiscover := discover.NewPodDiscover(c.ctx, monitorMeta, c.objectsController.NodeNameExists, &discover.PodParams{
			BaseParams: &discover.BaseParams{
				Client:                 c.client,
				RelabelRule:            feature.RelabelRule(podMonitor.Annotations),
				RelabelIndex:           feature.RelabelIndex(podMonitor.Annotations),
				NormalizeMetricName:    feature.IfNormalizeMetricName(podMonitor.Annotations),
				AntiAffinity:           feature.IfAntiAffinity(podMonitor.Annotations),
				MatchSelector:          feature.MonitorMatchSelector(podMonitor.Annotations),
				DropSelector:           feature.MonitorDropSelector(podMonitor.Annotations),
				EndpointSliceSupported: endpointSliceSupported,
				Name:                   monitorMeta.ID(),
				DataID:                 dataID,
				KubeConfig:             ConfKubeConfig,
				Namespaces:             namespaces,
				Relabels:               resultLabels,
				Path:                   endpoint.Path,
				Scheme:                 endpoint.Scheme,
				BasicAuth:              endpoint.BasicAuth.DeepCopy(),
				BearerTokenSecret:      endpoint.BearerTokenSecret.DeepCopy(),
				TLSConfig:              &promv1.TLSConfig{SafeTLSConfig: safeTlsConfig},
				Period:                 string(endpoint.Interval),
				Timeout:                string(endpoint.ScrapeTimeout),
				ProxyURL:               proxyURL,
				ExtraLabels:            specLabels,
				ForwardLocalhost:       feature.IfForwardLocalhost(podMonitor.Annotations),
				DisableCustomTimestamp: !ifHonorTimestamps(endpoint.HonorTimestamps),
				System:                 systemResource,
				UrlValues:              endpoint.Params,
				MetricRelabelConfigs:   metricRelabelings,
			},
			TLSConfig: endpoint.TLSConfig,
		})

		logger.Infof("get new pod discover %s", podDiscover)
		discovers = append(discovers, podDiscover)
	}
	return discovers
}

func (c *Operator) handlePrometheusRuleAdd(obj interface{}) {
	promRule, ok := obj.(*promv1.PrometheusRule)
	if !ok {
		logger.Errorf("expected PrometheusRule type, got %T", obj)
		return
	}

	c.promsliController.UpdatePrometheusRule(promRule)
}

func (c *Operator) handlePrometheusRuleUpdate(_ interface{}, obj interface{}) {
	promRule, ok := obj.(*promv1.PrometheusRule)
	if !ok {
		logger.Errorf("expected PrometheusRule type, got %T", obj)
		return
	}

	c.promsliController.UpdatePrometheusRule(promRule)
}

func (c *Operator) handlePrometheusRuleDelete(obj interface{}) {
	promRule, ok := obj.(*promv1.PrometheusRule)
	if !ok {
		logger.Errorf("expected PrometheusRule type, got %T", obj)
		return
	}

	c.promsliController.DeletePrometheusRule(promRule)
}

func (c *Operator) handlePodMonitorAdd(obj interface{}) {
	podMonitor, ok := obj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", obj)
		return
	}

	// 新增的 podmonitor 命中黑名单则流程终止
	if IfRejectPodMonitor(podMonitor) {
		logger.Infof("add action match the blacklist rules, podMonitor=%+v", podMonitor)
		return
	}

	discovers := c.createPodMonitorDiscovers(podMonitor)
	for _, dis := range discovers {
		if err := c.addOrUpdateDiscover(dis); err != nil {
			logger.Errorf("add or update podMonitor discover %s failed, err: %s", dis, err)
		}
	}
}

func (c *Operator) handlePodMonitorUpdate(oldObj interface{}, newObj interface{}) {
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
		logger.Debugf("podMonitor %+v does not change", old)
		return
	}

	// 对于更新的 podmonitor 如果新的 spec 命中黑名单 则需要将原有的 podmonitor 移除
	if IfRejectPodMonitor(cur) {
		logger.Infof("update action match the blacklist rules, podMonitor=%+v", cur)
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
			logger.Errorf("add or update podMonitor discover %s failed, err: %s", dis, err)
		}
	}
}

func (c *Operator) handlePodMonitorDelete(obj interface{}) {
	podMonitor, ok := obj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", obj)
		return
	}

	for _, name := range c.getPodMonitorDiscoversName(podMonitor) {
		c.deleteDiscoverByName(name)
	}
}

func (c *Operator) handleDiscoverNotify() {
	c.wg.Add(1)
	defer c.wg.Done()

	var last int64
	dispatch := func(trigger string) {
		now := time.Now()
		c.mm.IncDispatchedTaskCounter(trigger)
		c.dispatchTasks()
		c.mm.ObserveDispatchedTaskDuration(trigger, now)
		last = now.Unix() // 更新最近一次调度的时间
	}

	timer := time.NewTimer(time.Hour)
	timer.Stop()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return

		case <-discover.Notify():
			// 1min 内最多只能进行 2 次调度
			if time.Now().Unix()-last <= 30 {
				timer.Reset(time.Second * 30) // 保证信号不被丢弃
				continue
			}
			dispatch("notify")

		case <-ticker.C: // 兜底检查
			dispatch("ticker")

		case <-timer.C: // 信号再收敛
			dispatch("timer")
		}
	}
}

func (c *Operator) handleDataIDNotify() {
	c.wg.Add(1)
	defer c.wg.Done()

	var count int
	for {
		select {
		case <-c.ctx.Done():
			return

		case <-dataidwatcher.Notify():
			start := time.Now()
			count++
			c.reloadAllDiscovers()
			logger.Infof("reload discovers, count=%d, take: %v", count, time.Since(start))
		}
	}
}
