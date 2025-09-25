// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	loggingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/logging/v1alpha1"
	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	bkinformers "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/informers/externalversions"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	logConfigTypeStd       = "std_log_config"
	logConfigTypeContainer = "container_log_config"
	logConfigTypeNode      = "node_log_config"
)

type bkLogConfigEntity struct {
	Obj *loggingv1alpha1.BkLogConfig
}

func newBkLogConfigEntity(obj *loggingv1alpha1.BkLogConfig) *bkLogConfigEntity {
	entity := &bkLogConfigEntity{
		Obj: obj,
	}

	// 需要过滤 logbkenv 归属
	env := feature.BkEnv(obj.Labels)
	if env != configs.G().LogBkEnv {
		logger.Warnf("want bkenv '%s', but got '%s', object (%s)", configs.G().LogBkEnv, env, entity.UUID())
		return nil
	}
	return entity
}

func (e *bkLogConfigEntity) UUID() string {
	if e.Obj == nil {
		return ""
	}

	return fmt.Sprintf("%s/%s", e.Obj.Namespace, e.Obj.Name)
}

func (e *bkLogConfigEntity) isVCluster(matcherLabel map[string]string) bool {
	vClusterLabelKey := configs.G().VCluster.LabelKey
	_, ok := matcherLabel[vClusterLabelKey]
	return ok
}

func (e *bkLogConfigEntity) getWorkloadName(name string, kind string) string {
	if utils.LowerEq(kind, kindReplicaSet) {
		index := strings.LastIndex(name, "-")
		return name[:index]
	}
	return name
}

func (e *bkLogConfigEntity) MatchWorkload(labels, annotations map[string]string, ownerRefs []OwnerRef) bool {
	return e.matchWorkloadType(labels, annotations, ownerRefs) && e.matchWorkloadType(labels, annotations, ownerRefs)
}

func (e *bkLogConfigEntity) matchWorkloadName(labels, annotations map[string]string, ownerRefs []OwnerRef) bool {
	if e.Obj.Spec.WorkloadName == "" {
		return true
	}

	r, err := regexp.Compile(e.Obj.Spec.WorkloadName)
	if err != nil {
		return false
	}

	var names []string
	if e.isVCluster(labels) {
		name := annotations[configs.G().VCluster.WorkloadNameAnnotationKey]
		kind := annotations[configs.G().VCluster.WorkloadTypeAnnotationKey]
		names = append(names, e.getWorkloadName(name, kind))
	} else {
		for _, ownerReference := range ownerRefs {
			names = append(names, e.getWorkloadName(ownerReference.Name, ownerReference.Kind))
		}
	}

	for _, name := range names {
		if r.MatchString(name) {
			return true
		}
		if utils.LowerEq(name, e.Obj.Spec.WorkloadName) {
			return true
		}
	}
	return false
}

func (e *bkLogConfigEntity) matchWorkloadType(labels, annotations map[string]string, ownerRefs []OwnerRef) bool {
	if e.Obj.Spec.WorkloadType == "" {
		return true
	}

	var kinds []string
	if e.isVCluster(labels) {
		kinds = append(kinds, annotations[configs.G().VCluster.WorkloadTypeAnnotationKey])
	} else {
		for _, ownerReference := range ownerRefs {
			kinds = append(kinds, ownerReference.Kind)
		}
	}

	for _, kind := range kinds {
		if utils.LowerEq(kind, kindReplicaSet) {
			if utils.LowerEq(e.Obj.Spec.WorkloadType, kindDeployment) {
				return true
			}
		}
		if utils.LowerEq(e.Obj.Spec.WorkloadType, kind) {
			return true
		}
	}
	return false
}

func (e *bkLogConfigEntity) MatchContainerName(containerName string) bool {
	// containerNameMatch empty return true because do not match containerName
	if len(e.Obj.Spec.ContainerNameExclude) != 0 {
		for _, excludeName := range e.Obj.Spec.ContainerNameExclude {
			if excludeName == containerName {
				// containerName is in containerNameExclude, return false
				return false
			}
		}
	}
	if len(e.Obj.Spec.ContainerNameMatch) == 0 {
		return true
	}
	for _, matchContainerName := range e.Obj.Spec.ContainerNameMatch {
		if matchContainerName == containerName {
			return true
		}
	}
	return false
}

func (e *bkLogConfigEntity) MatchAnnotation(matchAnnotations map[string]string) bool {
	selector, err := metav1.LabelSelectorAsSelector(&e.Obj.Spec.AnnotationSelector)
	if err != nil {
		return false
	}
	return selector.Matches(labels.Set(matchAnnotations))
}

func (e *bkLogConfigEntity) MatchLabel(matchLabels map[string]string) bool {
	selector, err := metav1.LabelSelectorAsSelector(&e.Obj.Spec.LabelSelector)
	if err != nil {
		return false
	}
	return selector.Matches(labels.Set(matchLabels))
}

// MatchNamespace 判断 namespace 是否匹配上
func (e *bkLogConfigEntity) MatchNamespace(namespace string) bool {
	if e.Obj.Spec.NamespaceSelector.Any {
		return true
	}

	if len(e.Obj.Spec.NamespaceSelector.ExcludeNames) != 0 {
		// 全部不匹配 true，否则为 false
		for _, ns := range e.Obj.Spec.NamespaceSelector.ExcludeNames {
			if ns == namespace {
				return false
			}
		}
		return true
	} else if len(e.Obj.Spec.NamespaceSelector.MatchNames) != 0 {
		// 优先使用 NamespaceSelector 配置，列表中任意一个满足即可
		// 有一个匹配上则为 true，否则直接 false
		for _, ns := range e.Obj.Spec.NamespaceSelector.MatchNames {
			if ns == namespace {
				return true
			}
		}
		return false
	} else {
		// 其次，使用 Namespace 配置，直接名字匹配
		if e.Obj.Spec.Namespace != "" {
			if e.Obj.Spec.Namespace != namespace {
				return false
			}
			return true
		}
		// 未配置则返回 true
		return true
	}
}

type BkLogConfigMap struct {
	lock sync.RWMutex

	entitiesMap map[string]*bkLogConfigEntity
}

func (m *BkLogConfigMap) Count() int {
	m.lock.Lock()
	defer m.lock.Unlock()

	return len(m.entitiesMap)
}

func (m *BkLogConfigMap) Del(e *bkLogConfigEntity) {
	m.lock.Lock()
	defer m.lock.Unlock()

	logger.Infof("delete BkLogConfig object (%s)", e.UUID())
	delete(m.entitiesMap, e.UUID())
}

func (m *BkLogConfigMap) Set(e *bkLogConfigEntity) {
	m.lock.Lock()
	defer m.lock.Unlock()

	logger.Infof("update BkLogConfig object (%s)", e.UUID())
	m.entitiesMap[e.UUID()] = e
}

func (m *BkLogConfigMap) Range(visitFunc func(e *bkLogConfigEntity)) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	for _, e := range m.entitiesMap {
		if visitFunc != nil {
			visitFunc(e)
		}
	}
}

func newBklogConfigObjects(ctx context.Context, client bkversioned.Interface, resources map[GVRK]struct{}) (*BkLogConfigMap, error) {
	objsMap := &BkLogConfigMap{
		entitiesMap: make(map[string]*bkLogConfigEntity),
	}

	gvrk := GVRK{
		Group:    "bk.tencent.com",
		Version:  "v1alpha1",
		Resource: resourceBkLogConfigs,
		Kind:     kindBkLogConfig,
	}
	if _, ok := resources[gvrk]; !ok {
		logger.Infof("no resource %#v found", gvrk)
		return objsMap, nil
	}

	factory := bkinformers.NewSharedInformerFactory(client, define.ReSyncPeriod)
	informer := factory.Bk().V1alpha1().BkLogConfigs().Informer()

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			bklogconfig, ok := obj.(*loggingv1alpha1.BkLogConfig)
			if !ok {
				logger.Errorf("expected BkLogConfig type, got %T", obj)
				return
			}

			entity := newBkLogConfigEntity(bklogconfig)
			if entity != nil {
				objsMap.Set(entity)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			old, ok := oldObj.(*loggingv1alpha1.BkLogConfig)
			if !ok {
				logger.Errorf("expected BkLogConfig type, got %T", oldObj)
				return
			}
			cur, ok := newObj.(*loggingv1alpha1.BkLogConfig)
			if !ok {
				logger.Errorf("expected BkLogConfig type, got %T", newObj)
				return
			}
			if old.ResourceVersion == cur.ResourceVersion {
				return
			}

			entity := newBkLogConfigEntity(cur)
			if entity != nil {
				objsMap.Set(entity)
			}
		},
		DeleteFunc: func(obj any) {
			bklogconfig, ok := obj.(*loggingv1alpha1.BkLogConfig)
			if !ok {
				logger.Errorf("expected BkLogConfig type, got %T", obj)
				return
			}

			entity := newBkLogConfigEntity(bklogconfig)
			if entity != nil {
				objsMap.Del(entity)
			}
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindBkLogConfig, informer)
	if !synced {
		return nil, errors.New("failed to sync BkLogConfig caches")
	}

	return objsMap, nil
}
