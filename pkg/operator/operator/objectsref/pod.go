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

type PodObject struct {
	ID          ObjectID
	OwnerRefs   []OwnerRef
	NodeName    string
	PodIP       string
	Labels      map[string]string
	Annotations map[string]string
	Containers  []ContainerKey
}

type PodMap struct {
	mut  sync.Mutex
	objs map[string]PodObject
}

func NewPodMap() *PodMap {
	return &PodMap{
		objs: make(map[string]PodObject),
	}
}

func (m *PodMap) Set(obj PodObject) {
	m.mut.Lock()
	defer m.mut.Unlock()

	m.objs[obj.ID.String()] = obj
}

func (m *PodMap) Del(oid ObjectID) {
	m.mut.Lock()
	defer m.mut.Unlock()

	delete(m.objs, oid.String())
}

func (m *PodMap) Counter() map[string]int {
	m.mut.Lock()
	defer m.mut.Unlock()

	ret := make(map[string]int)
	for _, obj := range m.objs {
		ret[obj.ID.Namespace]++
	}
	return ret
}

func (m *PodMap) GetByNodeName(nodeName string) []PodObject {
	m.mut.Lock()
	defer m.mut.Unlock()

	var ret []PodObject
	for _, obj := range m.objs {
		if obj.NodeName == nodeName {
			ret = append(ret, obj)
		}
	}
	return ret
}

func (m *PodMap) GetByNamespace(namespace string) []PodObject {
	m.mut.Lock()
	defer m.mut.Unlock()

	var ret []PodObject
	for _, obj := range m.objs {
		if obj.ID.Namespace == namespace {
			ret = append(ret, obj)
		}
	}
	return ret
}

func (m *PodMap) GetAll() []PodObject {
	m.mut.Lock()
	defer m.mut.Unlock()

	ret := make([]PodObject, 0, len(m.objs))
	for _, obj := range m.objs {
		ret = append(ret, obj)
	}
	return ret
}

func (m *PodMap) GetRefs(oid ObjectID) ([]OwnerRef, bool) {
	m.mut.Lock()
	defer m.mut.Unlock()

	obj, ok := m.objs[oid.String()]
	return obj.OwnerRefs, ok
}
