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
	"reflect"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promcli "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	prominfs "github.com/prometheus-operator/prometheus-operator/pkg/informers"
	corev1 "k8s.io/api/core/v1"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	bkcli "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/dataidwatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/shareddiscovery"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/helmcharts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/processmonitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/qcloudmonitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	monitorKindServiceMonitor = "ServiceMonitor"
	monitorKindPodMonitor     = "PodMonitor"
	monitorKindHttpSd         = "HttpSd"
	monitorKindPolarisSd      = "PolarisSd"
	monitorKindEtcdSd         = "EtcdSd"
	monitorKindKubernetesSd   = "KubernetesSd"
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

	client  kubernetes.Interface
	mdCli   metadata.Interface
	promCli promcli.Interface
	bkCli   bkcli.Interface
	srv     *http.Server

	qmopr *qcloudmonitor.Operator
	pmopr *processmonitor.Operator

	serviceMonitorInformer *prominfs.ForResource
	podMonitorInformer     *prominfs.ForResource
	helmchartsController   *helmcharts.Controller

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

	promSdConfigsBytes        map[SecretKey][]byte           // 无并发读写
	prevResourceScrapeConfigs map[string]resourceScrapConfig // 无并发读写
}

func New(ctx context.Context, buildInfo BuildInfo) (*Operator, error) {
	var (
		operator = new(Operator)
		err      error
	)

	operator.buildInfo = buildInfo
	operator.ctx, operator.cancel = context.WithCancel(ctx)
	if err = os.Setenv("KUBECONFIG", configs.G().KubeConfig); err != nil {
		return nil, err
	}

	apiHost := configs.G().APIServerHost
	operator.client, err = k8sutils.NewK8SClient(apiHost, configs.G().GetTLS())
	if err != nil {
		return nil, err
	}

	operator.mdCli, err = k8sutils.NewMetadataClient(apiHost, configs.G().GetTLS())
	if err != nil {
		return nil, err
	}

	operator.promCli, err = k8sutils.NewPromClient(apiHost, configs.G().GetTLS())
	if err != nil {
		return nil, err
	}

	operator.bkCli, err = k8sutils.NewBKClient(apiHost, configs.G().GetTLS())
	if err != nil {
		return nil, err
	}

	operator.discovers = make(map[string]discover.Discover)
	allNamespaces := map[string]struct{}{}
	if len(configs.G().TargetNamespaces) == 0 {
		allNamespaces = map[string]struct{}{corev1.NamespaceAll: {}}
	} else {
		for _, namespace := range configs.G().TargetNamespaces {
			allNamespaces[namespace] = struct{}{}
		}
	}

	denyTargetNamespaces := make(map[string]struct{})
	for _, namespace := range configs.G().DenyTargetNamespaces {
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
	useEndpointslice = parsedVersion.GTE(semver.MustParse("1.21.0")) && configs.G().EnableEndpointSlice
	if useEndpointslice {
		logger.Info("use 'endpointslice' instead of 'endpoint'")
	}

	if configs.G().EnableServiceMonitor {
		operator.serviceMonitorInformer, err = prominfs.NewInformersForResource(
			prominfs.NewMonitoringInformerFactories(
				allNamespaces,
				denyTargetNamespaces,
				operator.promCli,
				define.ReSyncPeriod,
				nil,
			),
			schema.GroupVersionResource(promv1.SchemeGroupVersion.WithResource(promv1.ServiceMonitorName)),
		)
		if err != nil {
			return nil, errors.Wrap(err, "create ServiceMonitor informer failed")
		}
	}

	if configs.G().EnablePodMonitor {
		operator.podMonitorInformer, err = prominfs.NewInformersForResource(
			prominfs.NewMonitoringInformerFactories(
				allNamespaces,
				denyTargetNamespaces,
				operator.promCli,
				define.ReSyncPeriod,
				nil,
			),
			schema.GroupVersionResource(promv1.SchemeGroupVersion.WithResource(promv1.PodMonitorName)),
		)
		if err != nil {
			return nil, errors.Wrap(err, "create PodMonitor informer failed")
		}
	}

	if configs.G().QCloudMonitor.Enabled {
		operator.qmopr, err = qcloudmonitor.New(ctx, qcloudmonitor.ClientSet{
			Client: operator.client,
			BK:     operator.bkCli,
			Prom:   operator.promCli,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create QCloudMonitor operator failed")
		}
	}

	if configs.G().ProcessMonitor.Enabled {
		operator.pmopr, err = processmonitor.New(ctx, processmonitor.ClientSet{
			Client: operator.client,
			BK:     operator.bkCli,
			Prom:   operator.promCli,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create ProcessMonitor operator failed")
		}
	}

	operator.helmchartsController, err = helmcharts.NewController(operator.ctx, operator.client)
	if err != nil {
		return nil, errors.Wrap(err, "create helmchartsController failed")
	}

	operator.objectsController, err = objectsref.NewController(operator.ctx, operator.client, operator.mdCli, operator.bkCli)
	if err != nil {
		return nil, errors.Wrap(err, "create objectsController failed")
	}

	operator.recorder = newRecorder()
	operator.dw = dataidwatcher.New(operator.ctx, operator.bkCli)
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

func (c *Operator) getDiscoverCount() map[string]int {
	c.discoversMut.Lock()
	defer c.discoversMut.Unlock()

	count := make(map[string]int)
	for _, dis := range c.discovers {
		count[dis.Type()]++
	}
	return count
}

func equalDataID(a, b *v1beta1.DataID) bool {
	// 当且仅当 DataID 实例存在且非空才可能相等
	if a == nil || b == nil {
		return false
	}

	// 仅比对关键字段 dataid reload 是一个`比较重`的操作 尽量减少其影响
	if a.Name != b.Name {
		return false
	}
	if !reflect.DeepEqual(a.Spec, b.Spec) {
		return false
	}
	if !reflect.DeepEqual(a.Labels, b.Labels) {
		return false
	}

	return true
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
		if equalDataID(dis.DataID(), newDataID) {
			continue
		}

		dis.SetDataID(newDataID)
		if err := dis.Reload(); err != nil {
			logger.Errorf("discover %s reload failed: %s", name, err)
		}
	}
}

func (c *Operator) recordMetrics() {
	c.wg.Add(1)
	defer c.wg.Done()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mm.UpdateUptime(15)
			c.mm.SetAppBuildInfo(c.buildInfo)
			c.updateNodeConfigMetrics()
			c.updateMonitorEndpointMetrics()
			c.updateResourceMetrics()
			c.updateSharedDiscoveryMetrics()
			c.helmchartsController.UpdateMetrics()

		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Operator) updateSharedDiscoveryMetrics() {
	c.mm.SetSharedDiscoveryCount(len(shareddiscovery.AllDiscovery()))
	for typ, count := range c.getDiscoverCount() {
		c.mm.SetDiscoverCount(typ, count)
	}
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
	endpoints := c.recorder.getEndpoints(false)
	for name, count := range endpoints {
		c.mm.SetMonitorEndpointCount(name, count)
	}
}

func (c *Operator) updateResourceMetrics() {
	resources := objectsref.GetResourceCount()
	for resource, count := range resources {
		c.mm.SetResourceCount(resource, count)
	}
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

	if configs.G().EnableServiceMonitor {
		c.serviceMonitorInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handleServiceMonitorAdd,
			UpdateFunc: c.handleServiceMonitorUpdate,
			DeleteFunc: c.handleServiceMonitorDelete,
		})
		c.serviceMonitorInformer.Start(c.ctx.Done())
	}

	if configs.G().EnablePodMonitor {
		c.podMonitorInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handlePodMonitorAdd,
			UpdateFunc: c.handlePodMonitorUpdate,
			DeleteFunc: c.handlePodMonitorDelete,
		})
		c.podMonitorInformer.Start(c.ctx.Done())
	}

	if err := c.waitForCacheSync(c.ctx); err != nil {
		return err
	}

	if configs.G().QCloudMonitor.Enabled {
		if err := c.qmopr.Start(); err != nil {
			return err
		}
	}
	if configs.G().ProcessMonitor.Enabled {
		if err := c.pmopr.Start(); err != nil {
			return err
		}
	}

	// 如果启动了 StatefulSetWorker 则需要监听 statefulset secrets 的变化以及 statefulset 本身的变化
	// 该资源只存在于 ConfMonitorNamespace namespace
	if configs.G().EnableStatefulSetWorker {
		if err := c.listWatchStatefulSetWorker(); err != nil {
			return err
		}
		if err := c.listWatchStatefulSetSecrets(); err != nil {
			return err
		}
	}

	if configs.G().Kubelet.Enable {
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
	c.helmchartsController.Stop()
	c.objectsController.Stop()
	shareddiscovery.Deactivate()
}

// waitForCacheSync waits for the informers' caches to be synced.
func (c *Operator) waitForCacheSync(ctx context.Context) error {
	ok := true

	for _, infs := range []struct {
		name                 string
		informersForResource *prominfs.ForResource
	}{
		{"ServiceMonitor", c.serviceMonitorInformer},
		{"PodMonitor", c.podMonitorInformer},
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

	last := time.Now().Unix() + configs.G().DispatchInterval // 第一次调度时多等待一个周期 避免触发太多 secrets 变更
	dispatch := func(trigger string) {
		now := time.Now()
		c.mm.IncDispatchedTaskCounter(trigger)
		c.dispatchTasks()
		c.mm.ObserveDispatchedTaskDuration(trigger, now)
		logger.Infof("trigger %s dispatch take: %v", trigger, time.Since(now))
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
			if time.Now().Unix()-last <= configs.G().DispatchInterval {
				timer.Reset(time.Second * time.Duration(configs.G().DispatchInterval)) // 保证信号不被丢弃
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
