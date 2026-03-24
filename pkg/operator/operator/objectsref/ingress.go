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
	"sync"

	"github.com/pkg/errors"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	networkingv1 "k8s.io/api/networking/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ingressEntity struct {
	namespace string
	name      string
	services  []string
}

type ingressEntities map[string]ingressEntity

type IngressMap struct {
	mut       sync.Mutex
	ingresses map[string]ingressEntities
}

func NewIngressMap() *IngressMap {
	return &IngressMap{
		ingresses: map[string]ingressEntities{},
	}
}

func (m *IngressMap) Count() int {
	m.mut.Lock()
	defer m.mut.Unlock()

	return len(m.ingresses)
}

func (m *IngressMap) Set(ingress ingressEntity) {
	m.mut.Lock()
	defer m.mut.Unlock()

	if _, ok := m.ingresses[ingress.namespace]; !ok {
		m.ingresses[ingress.namespace] = make(ingressEntities)
	}

	m.ingresses[ingress.namespace][ingress.name] = ingress
}

func (m *IngressMap) Del(namespace, name string) {
	m.mut.Lock()
	defer m.mut.Unlock()

	if objs, ok := m.ingresses[namespace]; ok {
		delete(objs, name)
	}
}

func (m *IngressMap) Range(namespace string, visitFunc func(name string, ingress ingressEntity)) {
	m.mut.Lock()
	defer m.mut.Unlock()

	ingresses, ok := m.ingresses[namespace]
	if !ok {
		return
	}

	for name, ingress := range ingresses {
		visitFunc(name, ingress)
	}
}

func newIngressObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory, resources map[GVRK]struct{}) (*IngressMap, error) {
	if _, ok := resources[GVRK{
		Group:    "networking.k8s.io",
		Version:  "v1",
		Resource: "ingresses",
		Kind:     "Ingress",
	}]; ok {
		return newIngressV1Objects(ctx, sharedInformer)
	}

	if _, ok := resources[GVRK{
		Group:    "extensions",
		Version:  "v1beta1",
		Resource: "ingresses",
		Kind:     "Ingress",
	}]; ok {
		return newIngressV1Beta1ExtensionsObjects(ctx, sharedInformer)
	}

	return newIngressV1Beta1Objects(ctx, sharedInformer)
}

func newIngressV1Objects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*IngressMap, error) {
	objs := NewIngressMap()

	genericInformer, err := sharedInformer.ForResource(networkingv1.SchemeGroupVersion.WithResource(resourceIngresses))
	if err != nil {
		return nil, err
	}

	makeIngress := func(namespace, name string, rules []networkingv1.IngressRule) ingressEntity {
		set := make(map[string]struct{})
		for _, rule := range rules {
			if rule.HTTP == nil {
				continue
			}

			for _, path := range rule.HTTP.Paths {
				svc := path.Backend.Service
				if svc != nil {
					set[svc.Name] = struct{}{}
				}
			}
		}

		services := make([]string, 0, len(set))
		for k := range set {
			services = append(services, k)
		}

		return ingressEntity{
			namespace: namespace,
			name:      name,
			services:  services,
		}
	}

	informer := genericInformer.Informer()
	err = informer.SetTransform(func(obj any) (any, error) {
		ingress, ok := obj.(*networkingv1.Ingress)
		if !ok {
			logger.Errorf("excepted Ingress type, got %T", obj)
			return obj, nil
		}

		ingress.Annotations = nil
		ingress.Labels = nil
		ingress.ManagedFields = nil
		ingress.Finalizers = nil
		return ingress, nil
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			ingress, ok := obj.(*networkingv1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		UpdateFunc: func(_, newObj any) {
			ingress, ok := newObj.(*networkingv1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", newObj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		DeleteFunc: func(obj any) {
			ingress, ok := obj.(*networkingv1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Del(ingress.Namespace, ingress.Name)
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindIngress, informer)
	if !synced {
		return nil, errors.New("failed to sync Ingress caches")
	}
	return objs, nil
}

func newIngressV1Beta1Objects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*IngressMap, error) {
	objs := NewIngressMap()

	genericInformer, err := sharedInformer.ForResource(networkingv1beta1.SchemeGroupVersion.WithResource(resourceIngresses))
	if err != nil {
		return nil, err
	}

	makeIngress := func(namespace, name string, rules []networkingv1beta1.IngressRule) ingressEntity {
		set := make(map[string]struct{})
		for _, rule := range rules {
			if rule.HTTP == nil {
				continue
			}

			for _, path := range rule.HTTP.Paths {
				set[path.Backend.ServiceName] = struct{}{}
			}
		}

		services := make([]string, 0, len(set))
		for k := range set {
			services = append(services, k)
		}

		return ingressEntity{
			namespace: namespace,
			name:      name,
			services:  services,
		}
	}

	informer := genericInformer.Informer()
	err = informer.SetTransform(func(obj any) (any, error) {
		ingress, ok := obj.(*networkingv1beta1.Ingress)
		if !ok {
			logger.Errorf("excepted Ingress type, got %T", obj)
			return obj, nil
		}

		ingress.Annotations = nil
		ingress.Labels = nil
		ingress.ManagedFields = nil
		ingress.Finalizers = nil
		return ingress, nil
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			ingress, ok := obj.(*networkingv1beta1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		UpdateFunc: func(_, newObj any) {
			ingress, ok := newObj.(*networkingv1beta1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", newObj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		DeleteFunc: func(obj any) {
			ingress, ok := obj.(*networkingv1beta1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Del(ingress.Namespace, ingress.Name)
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindIngress, informer)
	if !synced {
		return nil, errors.New("failed to sync Ingress caches")
	}
	return objs, nil
}

func newIngressV1Beta1ExtensionsObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*IngressMap, error) {
	objs := NewIngressMap()

	genericInformer, err := sharedInformer.ForResource(extensionsv1beta1.SchemeGroupVersion.WithResource(resourceIngresses))
	if err != nil {
		return nil, err
	}

	makeIngress := func(namespace, name string, rules []extensionsv1beta1.IngressRule) ingressEntity {
		set := make(map[string]struct{})
		for _, rule := range rules {
			if rule.HTTP == nil {
				continue
			}

			for _, path := range rule.HTTP.Paths {
				set[path.Backend.ServiceName] = struct{}{}
			}
		}

		services := make([]string, 0, len(set))
		for k := range set {
			services = append(services, k)
		}

		return ingressEntity{
			namespace: namespace,
			name:      name,
			services:  services,
		}
	}

	informer := genericInformer.Informer()
	err = informer.SetTransform(func(obj any) (any, error) {
		ingress, ok := obj.(*extensionsv1beta1.Ingress)
		if !ok {
			logger.Errorf("excepted Ingress type, got %T", obj)
			return obj, nil
		}

		ingress.Annotations = nil
		ingress.Labels = nil
		ingress.ManagedFields = nil
		ingress.Finalizers = nil
		return ingress, nil
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			ingress, ok := obj.(*extensionsv1beta1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		UpdateFunc: func(_, newObj any) {
			ingress, ok := newObj.(*extensionsv1beta1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", newObj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		DeleteFunc: func(obj any) {
			ingress, ok := obj.(*extensionsv1beta1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Del(ingress.Namespace, ingress.Name)
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindIngress, informer)
	if !synced {
		return nil, errors.New("failed to sync Ingress caches")
	}
	return objs, nil
}
