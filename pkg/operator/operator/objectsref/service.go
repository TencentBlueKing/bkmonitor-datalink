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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type serviceEntity struct {
	name            string
	namespace       string
	kind            string
	externalName    string
	loadBalancerIPs []string
	externalIPs     []string
	selector        map[string]string
}

type serviceEntities map[string]serviceEntity

type ServiceMap struct {
	mut      sync.Mutex
	services map[string]serviceEntities
}

func NewServiceMap() *ServiceMap {
	return &ServiceMap{
		services: map[string]serviceEntities{},
	}
}

func (m *ServiceMap) Count() int {
	m.mut.Lock()
	defer m.mut.Unlock()

	return len(m.services)
}

func (m *ServiceMap) Set(service *corev1.Service) {
	m.mut.Lock()
	defer m.mut.Unlock()

	if _, ok := m.services[service.Namespace]; !ok {
		m.services[service.Namespace] = make(serviceEntities)
	}

	mergeLbIPs := func(ip string, status corev1.LoadBalancerStatus) []string {
		set := make(map[string]struct{})
		if len(ip) > 0 {
			set[ip] = struct{}{}
		}
		for _, ingress := range status.Ingress {
			set[ingress.IP] = struct{}{}
		}

		dst := make([]string, 0, len(set))
		for k := range set {
			dst = append(dst, k)
		}
		return dst
	}

	m.services[service.Namespace][service.Name] = serviceEntity{
		name:            service.Name,
		namespace:       service.Namespace,
		kind:            string(service.Spec.Type),
		loadBalancerIPs: mergeLbIPs(service.Spec.LoadBalancerIP, service.Status.LoadBalancer),
		externalIPs:     service.Spec.ExternalIPs,
		externalName:    service.Spec.ExternalName,
		selector:        service.Spec.Selector,
	}
}

func (m *ServiceMap) Del(service *corev1.Service) {
	m.mut.Lock()
	defer m.mut.Unlock()

	if objs, ok := m.services[service.Namespace]; ok {
		delete(objs, service.Name)
	}
}

func (m *ServiceMap) Range(visitFunc func(namespace string, services serviceEntities)) {
	m.mut.Lock()
	defer m.mut.Unlock()

	for k, v := range m.services {
		visitFunc(k, v)
	}
}

func newServiceObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*ServiceMap, error) {
	objs := NewServiceMap()

	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceServices))
	if err != nil {
		return nil, err
	}

	informer := genericInformer.Informer()
	err = informer.SetTransform(func(obj any) (any, error) {
		service, ok := obj.(*corev1.Service)
		if !ok {
			logger.Errorf("excepted Service type, got %T", obj)
			return obj, nil
		}

		service.Annotations = nil
		service.Labels = nil
		service.ManagedFields = nil
		service.Finalizers = nil
		return service, nil
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			service, ok := obj.(*corev1.Service)
			if !ok {
				logger.Errorf("excepted Service type, got %T", obj)
				return
			}
			objs.Set(service)
		},
		UpdateFunc: func(_, newObj any) {
			service, ok := newObj.(*corev1.Service)
			if !ok {
				logger.Errorf("excepted Service type, got %T", newObj)
				return
			}
			objs.Set(service)
		},
		DeleteFunc: func(obj any) {
			service, ok := obj.(*corev1.Service)
			if !ok {
				logger.Errorf("excepted Service type, got %T", obj)
				return
			}
			objs.Del(service)
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindService, informer)
	if !synced {
		return nil, errors.New("failed to sync Service caches")
	}
	return objs, nil
}
