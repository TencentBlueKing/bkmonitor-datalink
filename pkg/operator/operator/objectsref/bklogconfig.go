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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	loggingV1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/logging/v1alpha1"
	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	bkinformers "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/informers/externalversions"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	StdLogConfig       = "std_log_config"
	ContainerLogConfig = "container_log_config"
	NodeLogConfig      = "node_log_config"
)

const (
	kindBkLogConfig      = "BkLogConfig"
	resourceBkLogConfigs = "bklogconfigs"
)

type bkLogConfigEntity struct {
	Obj *loggingV1alpha1.BkLogConfig
}

func newBkLogConfigEntity(obj any) *bkLogConfigEntity {
	bkLogConfig, ok := obj.(*loggingV1alpha1.BkLogConfig)
	if !ok {
		logger.Errorf("unexpected BkLogConfig type, got %T", obj)
	}
	return &bkLogConfigEntity{
		Obj: bkLogConfig,
	}
}

func (e *bkLogConfigEntity) UUID() string {
	if e.Obj == nil {
		return ""
	}

	return fmt.Sprintf("%s/%s/%s", e.Obj.Kind, e.Obj.Namespace, e.Obj.Name)
}

func (e *bkLogConfigEntity) isVCluster(matcherLabel map[string]string) bool {
	vClusterLabelKey := configs.G().VCluster.LabelKey
	_, ok := matcherLabel[vClusterLabelKey]
	return ok
}

func (e *bkLogConfigEntity) getValues(matcherLabel map[string]string, key string, defaultValue string) string {
	if v, ok := matcherLabel[key]; ok {
		return v
	}
	return defaultValue
}

func (e *bkLogConfigEntity) ToLowerEq(a, b string) bool {
	return strings.ToLower(a) == strings.ToLower(b)
}

func (e *bkLogConfigEntity) getWorkloadName(name string, kind string) string {
	if e.ToLowerEq(kind, kindReplicaSet) {
		index := strings.LastIndex(name, "-")
		return name[:index]
	}
	return name
}

func (e *bkLogConfigEntity) MatchWorkloadName(matcherLabels, matcherAnnotations map[string]string, ownerRefs []OwnerRef) bool {
	if e.Obj.Spec.WorkloadName == "" {
		return true
	}

	r, err := regexp.Compile(e.Obj.Spec.WorkloadName)
	if err != nil {
		return false
	}

	var names []string

	if e.isVCluster(matcherLabels) {
		name := e.getValues(matcherAnnotations, configs.G().VCluster.WorkloadNameAnnotationKey, "")
		kind := e.getValues(matcherAnnotations, configs.G().VCluster.WorkloadTypeAnnotationKey, "")
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
		if e.ToLowerEq(name, e.Obj.Spec.WorkloadName) {
			return true
		}
	}
	return false
}

func (e *bkLogConfigEntity) MatchWorkloadType(matcherLabels, matcherAnnotations map[string]string, ownerRefs []OwnerRef) bool {
	var kinds []string

	if e.Obj.Spec.WorkloadType == "" {
		return true
	}

	if e.isVCluster(matcherLabels) {
		kinds = append(kinds, e.getValues(matcherAnnotations, configs.G().VCluster.WorkloadTypeAnnotationKey, ""))
	} else {
		for _, ownerReference := range ownerRefs {
			kinds = append(kinds, ownerReference.Kind)
		}
	}

	for _, kind := range kinds {
		if e.ToLowerEq(kind, kindReplicaSet) {
			if e.ToLowerEq(e.Obj.Spec.WorkloadType, kindDeployment) {
				return true
			}
		}
		if e.ToLowerEq(e.Obj.Spec.WorkloadType, kind) {
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

	labelSet := labels.Set(matchAnnotations)
	if !selector.Matches(labelSet) {
		return false
	}

	return true
}

func (e *bkLogConfigEntity) MatchLabel(matchLabels map[string]string) bool {
	selector, err := metav1.LabelSelectorAsSelector(&e.Obj.Spec.LabelSelector)
	if err != nil {
		return false
	}

	labelSet := labels.Set(matchLabels)
	if !selector.Matches(labelSet) {
		return false
	}

	return true
}

// MatchNamespace 判断 namespace 是否匹配上
func (e *bkLogConfigEntity) MatchNamespace(namespace string) bool {
	if e.Obj.Spec.NamespaceSelector.Any {
		return true
	}

	if len(e.Obj.Spec.NamespaceSelector.ExcludeNames) != 0 {
		// 全部不匹配true，否则为false
		for _, ns := range e.Obj.Spec.NamespaceSelector.ExcludeNames {
			if ns == namespace {
				return false
			}
		}
		return true
	} else if len(e.Obj.Spec.NamespaceSelector.MatchNames) != 0 {
		// 优先使用NamespaceSelector配置，列表中任意一个满足即可
		// 有一个匹配上则为true，否则直接false
		for _, ns := range e.Obj.Spec.NamespaceSelector.MatchNames {
			if ns == namespace {
				return true
			}
		}
		return false
	} else {
		// 其次，使用Namespace配置，直接名字匹配
		if e.Obj.Spec.Namespace != "" {
			if e.Obj.Spec.Namespace != namespace {
				return false
			}
			return true
		}
		// 未配置则返回true
		return true
	}
}

type BkLogConfigMap struct {
	lock sync.RWMutex

	entitiesMap map[string]*bkLogConfigEntity
}

func (o *BkLogConfigMap) deleteEntity(e *bkLogConfigEntity) {
	if e == nil {
		return
	}

	o.lock.Lock()
	defer o.lock.Unlock()
	delete(o.entitiesMap, e.UUID())
}

func (o *BkLogConfigMap) setEntity(e *bkLogConfigEntity) {
	o.lock.Lock()
	defer o.lock.Unlock()
	o.entitiesMap[e.UUID()] = e
}

func (o *BkLogConfigMap) addFunc(obj any) {
	bkLogConfig := newBkLogConfigEntity(obj)

	env := feature.BkEnv(bkLogConfig.Obj.Labels)
	if env != configs.G().BkEnv {
		logger.Warnf("want bkenv '%s', but got '%s'", configs.G().BkEnv, env)
		return
	}

	o.setEntity(bkLogConfig)
}

func (o *BkLogConfigMap) updateFunc(_, obj any) {
	o.addFunc(obj)
}

func (o *BkLogConfigMap) deleteFunc(obj any) {
	bkLogConfig := newBkLogConfigEntity(obj)

	env := feature.BkEnv(bkLogConfig.Obj.Labels)
	if env != configs.G().BkEnv {
		logger.Warnf("want bkenv '%s', but got '%s'", configs.G().BkEnv, env)
		return
	}

	o.deleteEntity(bkLogConfig)
}

func (o *BkLogConfigMap) RangeBkLogConfig(visitFunc func(e *bkLogConfigEntity)) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	for _, e := range o.entitiesMap {
		if visitFunc != nil {
			visitFunc(e)
		}
	}
	return
}

func NewObjectsMap(ctx context.Context, client bkversioned.Interface) (*BkLogConfigMap, error) {
	factory := bkinformers.NewSharedInformerFactory(client, define.ReSyncPeriod)
	informer := factory.Bk().V1alpha1().BkLogConfigs().Informer()

	objsMap := &BkLogConfigMap{
		entitiesMap: make(map[string]*bkLogConfigEntity),
	}
	informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    objsMap.addFunc,
			UpdateFunc: objsMap.updateFunc,
			DeleteFunc: objsMap.deleteFunc,
		},
	)
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindBkLogConfig, informer)
	if !synced {
		return nil, fmt.Errorf("failed to sync %s caches", kindBkLogConfig)
	}

	return objsMap, nil
}
