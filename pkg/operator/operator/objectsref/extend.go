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
	"errors"

	tkexv1alpha1 "github.com/Tencent/bk-bcs/bcs-scenarios/kourse/pkg/apis/tkex/v1alpha1"
	tkexversiond "github.com/Tencent/bk-bcs/bcs-scenarios/kourse/pkg/client/clientset/versioned"
	tkexinformers "github.com/Tencent/bk-bcs/bcs-scenarios/kourse/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type GVRK struct {
	Group    string
	Version  string
	Resource string
	Kind     string
}

func listServerPreferredResources(discoveryClient discovery.DiscoveryInterface) map[GVRK]struct{} {
	gvrks := make(map[GVRK]struct{})
	resources, _ := discoveryClient.ServerPreferredResources()
	for _, resource := range resources {
		gv, err := schema.ParseGroupVersion(resource.GroupVersion)
		if err != nil {
			continue
		}

		for _, r := range resource.APIResources {
			gvrk := GVRK{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: r.Name,
				Kind:     r.Kind,
			}
			gvrks[gvrk] = struct{}{}
		}
	}
	return gvrks
}

var (
	GameStatefulSetGVRK = GVRK{
		Group:    "tkex.tencent.com",
		Version:  "v1alpha1",
		Resource: resourceGameStatefulSets,
		Kind:     kindGameStatefulSet,
	}
	GameDeploymentGVRK = GVRK{
		Group:    "tkex.tencent.com",
		Version:  "v1alpha1",
		Resource: resourceGameDeployments,
		Kind:     kindGameDeployment,
	}

	KubernetesServerVersion string
)

type tkexObjects struct {
	gamestatefulset *Objects
	gamedeployment  *Objects
}

func newTkexObjects(ctx context.Context, client tkexversiond.Interface, resources map[GVRK]struct{}) (*tkexObjects, error) {
	sharedInformer := tkexinformers.NewSharedInformerFactoryWithOptions(client, define.ReSyncPeriod, tkexinformers.WithNamespace(metav1.NamespaceAll))

	var err error
	tkexObjs := &tkexObjects{}

	if _, ok := resources[GameStatefulSetGVRK]; ok {
		logger.Infof("found extend workload: %+v", GameStatefulSetGVRK)
		tkexObjs.gamestatefulset, err = newGameStatefulObjects(ctx, sharedInformer)
		if err != nil {
			return tkexObjs, err
		}
	}

	if _, ok := resources[GameDeploymentGVRK]; ok {
		logger.Infof("found extend workload: %+v", GameDeploymentGVRK)
		tkexObjs.gamedeployment, err = newGameDeploymentObjects(ctx, sharedInformer)
		if err != nil {
			return tkexObjs, err
		}
	}
	return tkexObjs, nil
}

func newGameStatefulObjects(ctx context.Context, sharedInformer tkexinformers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(tkexv1alpha1.SchemeGroupVersion.WithResource(resourceGameStatefulSets))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindGameStatefulSet)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			gamestatefulset, ok := obj.(*tkexv1alpha1.GameStatefulSet)
			if !ok {
				logger.Errorf("excepted GameStatefulSet type, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      gamestatefulset.Name,
					Namespace: gamestatefulset.Namespace,
				},
				OwnerRefs: toRefs(gamestatefulset.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			gamestatefulset, ok := newObj.(*tkexv1alpha1.GameStatefulSet)
			if !ok {
				logger.Errorf("excepted GameStatefulSet type, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      gamestatefulset.Name,
					Namespace: gamestatefulset.Namespace,
				},
				OwnerRefs: toRefs(gamestatefulset.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			gamestatefulset, ok := obj.(*tkexv1alpha1.GameStatefulSet)
			if !ok {
				logger.Errorf("excepted GameStatefulSet type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      gamestatefulset.Name,
				Namespace: gamestatefulset.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindGameStatefulSet, informer)
	if !synced {
		return nil, errors.New("failed to sync GameStatefulSet caches")
	}
	return objs, nil
}

func newGameDeploymentObjects(ctx context.Context, sharedInformer tkexinformers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(tkexv1alpha1.SchemeGroupVersion.WithResource(resourceGameDeployments))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindGameDeployment)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			gamedeployment, ok := obj.(*tkexv1alpha1.GameDeployment)
			if !ok {
				logger.Errorf("excepted GameDeployment type, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      gamedeployment.Name,
					Namespace: gamedeployment.Namespace,
				},
				OwnerRefs: toRefs(gamedeployment.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			gamedeployment, ok := newObj.(*tkexv1alpha1.GameDeployment)
			if !ok {
				logger.Errorf("excepted GameDeployment type, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      gamedeployment.Name,
					Namespace: gamedeployment.Namespace,
				},
				OwnerRefs: toRefs(gamedeployment.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			gamedeployment, ok := obj.(*tkexv1alpha1.GameDeployment)
			if !ok {
				logger.Errorf("excepted GameDeployment type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      gamedeployment.Name,
				Namespace: gamedeployment.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindGameDeployment, informer)
	if !synced {
		return nil, errors.New("failed to sync GameDeployment caches")
	}
	return objs, nil
}
