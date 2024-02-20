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
	"sync"

	corev1 "k8s.io/api/core/v1"
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

func (m *ServiceMap) rangeServices(visitFunc func(namespace string, services serviceEntities)) {
	m.mut.Lock()
	defer m.mut.Unlock()

	for k, v := range m.services {
		visitFunc(k, v)
	}
}

func matchLabels(subset, set map[string]string) bool {
	for k, v := range subset {
		val, ok := set[k]
		if !ok || val != v {
			return false
		}
	}
	return true
}
