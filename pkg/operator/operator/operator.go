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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/dataidwatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/shareddiscovery"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/promsli"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	monitorKindServiceMonitor = "ServiceMonitor"
	monitorKindPodMonitor     = "PodMonitor"
	monitorKindHttpSd         = "HttpSd"
)

var (
	kubernetesVersion string
	useEndpointslice  bool
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

	promSdConfigsBytes map[string][]byte // 无并发读写
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

	version, err := operator.client.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	kubernetesVersion = version.String()
	operator.mm.SetKubernetesVersion(kubernetesVersion)

	parsedVersion, err := semver.ParseTolerant(kubernetesVersion)
	if err != nil {
		parsedVersion = semver.MustParse("1.12.0") // 最低支持的 k8s 版本
		logger.Errorf("parse kubernetes version failed, instead of '%v', err: %v", parsedVersion, err)
	}

	// 1.21.0 开始 endpointslice 正式成为 v1
	useEndpointslice = parsedVersion.GTE(semver.MustParse("1.21.0")) && ConfEnableEndpointslice
	if useEndpointslice {
		logger.Info("use 'endpointslice' instead of 'endpoint'")
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
		operator.promsliController = promsli.NewController(operator.ctx, operator.client, useEndpointslice)
	}

	operator.objectsController, err = objectsref.NewController(operator.ctx, operator.client, operator.tkexclient)
	if err != nil {
		return nil, errors.Wrap(err, "create objectsController failed")
	}

	operator.recorder = NewRecorder()
	operator.dw = dataidwatcher.New(operator.ctx, operator.bkclient)
	operator.mm = newMetricMonitor()
	operator.statefulSetSecretMap = map[string]struct{}{}

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
	c.mm.SetSharedDiscoveryCount(len(shareddiscovery.AllDiscovery()))
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
	shareddiscovery.Activate()
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

	go c.loopHandlePromSdConfigs()
	c.cleanupInvalidSecrets()
	return nil
}

func (c *Operator) Stop() {
	c.cancel()
	if err := c.srv.Shutdown(context.Background()); err != nil {
		logger.Errorf("shouting down srv error: %v", err)
	}
	c.wg.Wait()

	c.dw.Stop()
	c.objectsController.Stop()
	shareddiscovery.Deactivate()
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

func ifHonorTimestamps(b *bool) bool {
	if b == nil {
		return true
	}
	return *b
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
