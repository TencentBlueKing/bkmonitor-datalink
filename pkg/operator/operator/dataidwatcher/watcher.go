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

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	bkcli "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	bkinfs "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/informers/externalversions"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/action"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultSystemDataIDKey = "__default_system__"
	defaultCommonDataIDKey = "__default_common__"

	// dataid 有两种用途 事件以及指标
	usageEvent  = "event"
	usageMetric = "metric"
)

var bus = notifier.NewDefaultRateBus()

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

func New(ctx context.Context, client bkcli.Interface) Watcher {
	ctx, cancel := context.WithCancel(ctx)
	factory := bkinfs.NewSharedInformerFactory(client, define.ReSyncPeriod)

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
		return nil, errors.New("system dataid not found")
	}

	// 3) 自定义匹配
	// 此处实现了 namespace/name 的类正则匹配 支持【|】分隔符 但要求 namespace/name 是全匹配
	//
	// TODO(mando): 目前的实现并不合理 此处会有语义上的歧义 实际上变成了笛卡尔积的匹配
	//  - 后续如果升级了 DataID 资源版本 则应该使用更合适的字段
	for _, dataID := range dataIDs {
		resource := dataID.Spec.MonitorResource

		// 要求资源类型一定要匹配
		if !resource.MatchSplitKind(meta.Kind) {
			continue
		}

		// 如果 name 为空 但 namespace 不为空 则命中选中的 namespaces 下的所有 monitor 资源
		if resource.Name == "" && resource.NameSpace != "" {
			if resource.MatchSplitNamespace(meta.Namespace) {
				return dataID, nil
			}
		}
		// 如果 name 不为空 但 namespace 为空 则命中所有 namespaces 下所有 name 匹配的资源
		if resource.Name != "" && resource.NameSpace == "" {
			if resource.MatchSplitName(meta.Name) {
				return dataID, nil
			}
		}
		// 如果 namespace、name 均不为空 则两者按照类正则的形式匹配
		// TODO(mando): 此处会有语义上的歧义 实际上变成了笛卡尔积的匹配 后续如果升级了 DataID 资源版本 则应该使用更合适的字段
		if resource.Name != "" && resource.NameSpace != "" {
			if resource.MatchSplitName(meta.Name) && resource.MatchSplitNamespace(meta.Namespace) {
				return dataID, nil
			}
		}
	}

	// 4) 通用兜底 dataid 匹配
	if dataID, ok := dataIDs[defaultCommonDataIDKey]; ok {
		return dataID, nil
	}

	return nil, errors.New("common dataid not found")
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
		return nil, errors.New("bcs_cluster_id not found")
	}
	info.BcsClusterID = clusterID

	bizID := dataID.Spec.Labels["bk_biz_id"]
	// 业务 id 不能为空
	if bizID == "" {
		return nil, errors.New("bk_biz_id not found")
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

func (w *dataIDWatcher) handleDataIDAdd(obj any) {
	dataID, ok := obj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("expected DataID type, got %T", obj)
		return
	}
	env := feature.BkEnv(dataID.Labels)
	if env != configs.G().BkEnv {
		logger.Warnf("want bkenv '%s', but got '%s'", configs.G().BkEnv, env)
		return
	}

	w.mm.IncHandledCounter(action.Add)
	w.updateDataID(dataID)
}

func (w *dataIDWatcher) handleDataIDDelete(obj any) {
	dataID, ok := obj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("expected DataID type, got %T", obj)
		return
	}
	env := feature.BkEnv(dataID.Labels)
	if env != configs.G().BkEnv {
		logger.Warnf("want bkenv '%s', but got '%s'", configs.G().BkEnv, env)
		return
	}

	w.mm.IncHandledCounter(action.Delete)
	w.deleteDataID(dataID)
}

func (w *dataIDWatcher) handleDataIDUpdate(oldObj any, newObj any) {
	old, ok := oldObj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("expected DataID type, got %T", oldObj)
		return
	}
	cur, ok := newObj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("expected DataID type got %T", newObj)
		return
	}

	if old.ResourceVersion == cur.ResourceVersion {
		w.mm.IncHandledCounter(action.Skip)
		return
	}

	w.mm.IncHandledCounter(action.Update)
	// 删除旧 dataid
	if feature.BkEnv(old.Labels) == configs.G().BkEnv {
		w.deleteDataID(old)
		logger.Infof("delete DataID, name=%v, id=%v, labels=%v", old.Name, old.Spec.DataID, old.Labels)
	}
	// 添加新 dataid
	if feature.BkEnv(cur.Labels) == configs.G().BkEnv {
		w.updateDataID(cur)
		logger.Infof("update DataID, name=%v, id=%v, labels=%v", cur.Name, cur.Spec.DataID, cur.Labels)
	}
}
