// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataidwatcher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"k8s.io/client-go/tools/cache"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/crd/v1beta1"
	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	bkinformers "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/informers/externalversions"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultSystemDataIDKey = "__default_system__"
	defaultCommonDataIDKey = "__default_common__"

	// dataid 有两种用途 事件以及指标
	usageEvent  = "event"
	usageMetric = "metric"
)

var (
	bus               = notifier.NewDefaultRateBus()
	ErrDataIDNotFound = errors.New("dataid not found")
)

// Publish 发布信号
func Publish() {
	bus.Publish()
}

// Notify 通知信号
func Notify() <-chan struct{} {
	return bus.Subscribe()
}

// Watcher 为 dataid 监视器抽象
type Watcher interface {
	// Start 启动监视器
	Start() error

	// Stop 关闭监视器
	Stop()

	// DataIDs 返回所有的 dataid
	DataIDs() []*bkv1beta1.DataID

	// MatchMetricDataID 匹配 metric 类型 dataid
	MatchMetricDataID(meta define.MonitorMeta, system bool) (*bkv1beta1.DataID, error)

	// MatchEventDataID 匹配 event 类型的 dataid
	MatchEventDataID(meta define.MonitorMeta, system bool) (*bkv1beta1.DataID, error)

	// GetClusterInfo 获取 cluster 信息
	GetClusterInfo() (*define.ClusterInfo, error)
}

type dataIDWatcher struct {
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	informer cache.SharedIndexInformer
	mm       *metricMonitor

	mut           sync.Mutex
	metricDataIDs map[string]*bkv1beta1.DataID
	eventDataIDs  map[string]*bkv1beta1.DataID
}

func New(ctx context.Context, client bkversioned.Interface) Watcher {
	ctx, cancel := context.WithCancel(ctx)
	factory := bkinformers.NewSharedInformerFactory(client, define.ReSyncPeriod)

	return &dataIDWatcher{
		ctx:           ctx,
		cancel:        cancel,
		mm:            newMetricMonitor(),
		metricDataIDs: make(map[string]*bkv1beta1.DataID),
		eventDataIDs:  make(map[string]*bkv1beta1.DataID),
		informer:      factory.Monitoring().V1beta1().DataIDs().Informer(),
	}
}

func (w *dataIDWatcher) DataIDs() []*bkv1beta1.DataID {
	w.mut.Lock()
	defer w.mut.Unlock()

	items := make([]*bkv1beta1.DataID, 0)
	for _, v := range w.metricDataIDs {
		items = append(items, v.DeepCopy())
	}
	for _, v := range w.eventDataIDs {
		items = append(items, v.DeepCopy())
	}
	return items
}

func (w *dataIDWatcher) uniqueKey(kind, namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s", strings.ToLower(kind), namespace, name)
}

func (w *dataIDWatcher) matchDataID(meta define.MonitorMeta, systemResource bool, dataIDs map[string]*bkv1beta1.DataID) (*bkv1beta1.DataID, error) {
	w.mut.Lock()
	defer w.mut.Unlock()

	// 匹配优先级
	//
	// 1) 三元组精准匹配
	uk := w.uniqueKey(meta.Kind, meta.Namespace, meta.Name)
	if dataID, ok := dataIDs[uk]; ok {
		return dataID, nil
	}

	// 2) 内置 dataid 匹配
	if systemResource {
		if dataID, ok := dataIDs[defaultSystemDataIDKey]; ok {
			return dataID, nil
		}
		return nil, ErrDataIDNotFound
	}

	// 3) 自定义匹配（namespace 匹配）要求 name 为空且 namespace 不为空
	for _, dataID := range dataIDs {
		resource := dataID.Spec.MonitorResource
		if resource.Name == "" && resource.NameSpace != "" {
			if strings.ToLower(resource.Kind) == strings.ToLower(meta.Kind) && resource.MatchSplitNamespace(meta.Namespace) {
				return dataID, nil
			}
		}
	}

	// 4) 通用兜底 dataid 匹配
	if dataID, ok := dataIDs[defaultCommonDataIDKey]; ok {
		return dataID, nil
	}

	return nil, ErrDataIDNotFound
}

func (w *dataIDWatcher) MatchMetricDataID(meta define.MonitorMeta, systemResource bool) (*bkv1beta1.DataID, error) {
	return w.matchDataID(meta, systemResource, w.metricDataIDs)
}

func (w *dataIDWatcher) GetClusterInfo() (*define.ClusterInfo, error) {
	dataID, err := w.MatchMetricDataID(define.MonitorMeta{}, true)
	if err != nil {
		return nil, err
	}

	info := new(define.ClusterInfo)
	clusterID := dataID.Spec.Labels["bcs_cluster_id"]
	// 集群 id 不能为空
	if clusterID == "" {
		return nil, errors.New("unknown bcs_cluster_id")
	}
	info.BcsClusterID = clusterID

	bizID := dataID.Spec.Labels["bk_biz_id"]
	// 业务 id 不能为空
	if bizID == "" {
		return nil, errors.New("unknown bk_biz_id")
	}
	info.BizID = bizID

	info.BkEnv = feature.BkEnv(dataID.Labels)
	return info, nil
}

func (w *dataIDWatcher) MatchEventDataID(meta define.MonitorMeta, systemResource bool) (*bkv1beta1.DataID, error) {
	return w.matchDataID(meta, systemResource, w.eventDataIDs)
}

func (w *dataIDWatcher) updateDataID(dataID *bkv1beta1.DataID) {
	usage := feature.DataIDUsage(dataID.Labels)
	switch usage {
	case usageEvent:
		w.updateEventDataID(dataID.DeepCopy())
	case usageMetric:
		w.updateMetricDataID(dataID.DeepCopy())
	default:
		return
	}

	w.mm.SetDataIDInfo(
		dataID.Spec.DataID,
		dataID.Name,
		usage,
		feature.IfSystemResource(dataID.Labels),
		feature.IfCommonResource(dataID.Labels),
	)

	logger.Infof("add DataID, name=%v, id=%v, labels=%v", dataID.Name, dataID.Spec.DataID, dataID.Labels)
	Publish()
}

func (w *dataIDWatcher) updateMetricDataID(dataID *bkv1beta1.DataID) {
	w.mut.Lock()
	defer w.mut.Unlock()

	if feature.IfSystemResource(dataID.Labels) {
		w.metricDataIDs[defaultSystemDataIDKey] = dataID
		return
	}
	if feature.IfCommonResource(dataID.Labels) {
		w.metricDataIDs[defaultCommonDataIDKey] = dataID
		return
	}

	resource := dataID.Spec.MonitorResource
	uk := w.uniqueKey(resource.Kind, resource.NameSpace, resource.Name)
	w.metricDataIDs[uk] = dataID
}

func (w *dataIDWatcher) updateEventDataID(dataID *bkv1beta1.DataID) {
	w.mut.Lock()
	defer w.mut.Unlock()

	if feature.IfSystemResource(dataID.Labels) {
		w.eventDataIDs[defaultSystemDataIDKey] = dataID
		return
	}
	if feature.IfCommonResource(dataID.Labels) {
		w.eventDataIDs[defaultCommonDataIDKey] = dataID
		return
	}

	resource := dataID.Spec.MonitorResource
	uk := w.uniqueKey(resource.Kind, resource.NameSpace, resource.Name)
	w.eventDataIDs[uk] = dataID
}

func (w *dataIDWatcher) deleteDataID(dataID *bkv1beta1.DataID) {
	switch feature.DataIDUsage(dataID.Labels) {
	case usageEvent:
		w.deleteEventDataID(dataID.DeepCopy())
	case usageMetric:
		w.deleteMetricDataID(dataID.DeepCopy())
	default:
		return
	}

	Publish() // 发布信号
}

func (w *dataIDWatcher) deleteMetricDataID(dataID *bkv1beta1.DataID) {
	w.mut.Lock()
	defer w.mut.Unlock()

	var uk string
	if feature.IfSystemResource(dataID.Labels) {
		uk = defaultSystemDataIDKey
	} else if feature.IfCommonResource(dataID.Labels) {
		uk = defaultCommonDataIDKey
	}

	if uk == "" {
		resource := dataID.Spec.MonitorResource
		uk = w.uniqueKey(resource.Kind, resource.NameSpace, resource.Name)
	}
	delete(w.metricDataIDs, uk)
}

func (w *dataIDWatcher) deleteEventDataID(dataID *bkv1beta1.DataID) {
	w.mut.Lock()
	defer w.mut.Unlock()

	var uk string
	if feature.IfSystemResource(dataID.Labels) {
		uk = defaultSystemDataIDKey
	} else if feature.IfCommonResource(dataID.Labels) {
		uk = defaultCommonDataIDKey
	}

	if uk == "" {
		resource := dataID.Spec.MonitorResource
		uk = w.uniqueKey(resource.Kind, resource.NameSpace, resource.Name)
	}
	delete(w.eventDataIDs, uk)
}

func (w *dataIDWatcher) Start() error {
	w.informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    w.handleDataIDAdd,
			DeleteFunc: w.handleDataIDDelete,
			UpdateFunc: w.handleDataIDUpdate,
		},
	)
	go w.informer.Run(w.ctx.Done())

	if !k8sutils.WaitForNamedCacheSync(w.ctx, "DataID", w.informer) {
		return errors.New("failed to sync DataID caches")
	}

	return nil
}

func (w *dataIDWatcher) Stop() {
	w.cancel()
	w.wg.Wait()
}

func (w *dataIDWatcher) handleDataIDAdd(obj interface{}) {
	dataID, ok := obj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("unexpected DataID type, got %T", obj)
		return
	}
	env := feature.BkEnv(dataID.Labels)
	if env != ConfBkEnv {
		logger.Warnf("want bkenv '%s', but got '%s'", ConfBkEnv, env)
		return
	}

	w.mm.IncHandledCounter(define.ActionAdd)
	w.updateDataID(dataID)
}

func (w *dataIDWatcher) handleDataIDDelete(obj interface{}) {
	dataID, ok := obj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("unexpected DataID type, got %T", obj)
		return
	}
	env := feature.BkEnv(dataID.Labels)
	if env != ConfBkEnv {
		logger.Warnf("want bkenv '%s', but got '%s'", ConfBkEnv, env)
		return
	}

	w.mm.IncHandledCounter(define.ActionDelete)
	w.deleteDataID(dataID)
}

func (w *dataIDWatcher) handleDataIDUpdate(oldObj interface{}, newObj interface{}) {
	old, ok := oldObj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("unexpected DataID type, got %T", oldObj)
		return
	}
	cur, ok := newObj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("unexpected DataID type got %T", newObj)
		return
	}

	if old.ResourceVersion == cur.ResourceVersion {
		w.mm.IncHandledCounter(define.ActionSkip)
		return
	}

	w.mm.IncHandledCounter(define.ActionUpdate)
	// 删除旧 dataid
	if feature.BkEnv(old.Labels) == ConfBkEnv {
		w.deleteDataID(old)
		logger.Infof("delete DataID, name=%v, id=%v, labels=%v", old.Name, old.Spec.DataID, old.Labels)
	}
	// 添加新 dataid
	if feature.BkEnv(cur.Labels) == ConfBkEnv {
		w.updateDataID(cur)
		logger.Infof("update DataID, name=%v, id=%v, labels=%v", cur.Name, cur.Spec.DataID, cur.Labels)
	}
}
