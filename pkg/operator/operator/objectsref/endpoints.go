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

type endpointsEntity struct {
	name      string
	namespace string
	addresses []string
}

type endpointsEntities map[string]endpointsEntity

type EndpointsMap struct {
	mut       sync.Mutex
	endpoints map[string]endpointsEntities
}

func NewEndpointsMap() *EndpointsMap {
	return &EndpointsMap{
		endpoints: map[string]endpointsEntities{},
	}
}

func (m *EndpointsMap) Count() int {
	m.mut.Lock()
	defer m.mut.Unlock()

	return len(m.endpoints)
}

func (m *EndpointsMap) Set(endpoints *corev1.Endpoints) {
	m.mut.Lock()
	defer m.mut.Unlock()

	if _, ok := m.endpoints[endpoints.Namespace]; !ok {
		m.endpoints[endpoints.Namespace] = make(endpointsEntities)
	}

	set := make(map[string]struct{})
	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.Addresses {
			set[addr.IP] = struct{}{}
		}
		for _, addr := range subset.NotReadyAddresses {
			set[addr.IP] = struct{}{}
		}
	}

	addresses := make([]string, 0, len(set))
	for k := range set {
		addresses = append(addresses, k)
	}

	m.endpoints[endpoints.Namespace][endpoints.Name] = endpointsEntity{
		name:      endpoints.Name,
		namespace: endpoints.Namespace,
		addresses: addresses,
	}
}

func (m *EndpointsMap) Del(endpoints *corev1.Endpoints) {
	m.mut.Lock()
	defer m.mut.Unlock()

	if objs, ok := m.endpoints[endpoints.Namespace]; ok {
		delete(objs, endpoints.Name)
	}
}

func (m *EndpointsMap) getEndpoints(namespace, name string) (endpointsEntity, bool) {
	m.mut.Lock()
	defer m.mut.Unlock()

	eps, ok := m.endpoints[namespace]
	if !ok {
		return endpointsEntity{}, false
	}

	v, ok := eps[name]
	return v, ok
}

func newEndpointsObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*EndpointsMap, error) {
	objs := NewEndpointsMap()

	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceEndpoints))
	if err != nil {
		return nil, err
	}

	informer := genericInformer.Informer()
	err = informer.SetTransform(func(obj any) (any, error) {
		endpoints, ok := obj.(*corev1.Endpoints)
		if !ok {
			logger.Errorf("excepted Endpoints type, got %T", obj)
			return obj, nil
		}

		endpoints.Annotations = nil
		endpoints.Labels = nil
		endpoints.ManagedFields = nil
		endpoints.Finalizers = nil
		return endpoints, nil
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			endpoints, ok := obj.(*corev1.Endpoints)
			if !ok {
				logger.Errorf("excepted Endpoints type, got %T", obj)
				return
			}
			objs.Set(endpoints)
		},
		UpdateFunc: func(_, newObj any) {
			endpoints, ok := newObj.(*corev1.Endpoints)
			if !ok {
				logger.Errorf("excepted Endpoints type, got %T", newObj)
				return
			}
			objs.Set(endpoints)
		},
		DeleteFunc: func(obj any) {
			endpoints, ok := obj.(*corev1.Endpoints)
			if !ok {
				logger.Errorf("excepted Endpoints type, got %T", obj)
				return
			}
			objs.Del(endpoints)
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindEndpoints, informer)
	if !synced {
		return nil, errors.New("failed to sync Endpoints caches")
	}
	return objs, nil
}
