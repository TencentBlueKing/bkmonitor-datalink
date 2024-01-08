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

func (m *IngressMap) rangeIngress(namespace string, visitFunc func(name string, ingress ingressEntity)) {
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
