// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processmonitor

import (
	"sync"

	"github.com/mitchellh/hashstructure/v2"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref/ring"
)

type Event struct {
	Action    objectsref.Action
	Name      string
	Namespace string
	Spec      *bkv1beta1.ProcessMonitorSpec
}

type Object struct {
	ID   objectsref.ObjectID
	Spec bkv1beta1.ProcessMonitorSpec
}

func (o Object) Hash() uint64 {
	h, _ := hashstructure.Hash(o, hashstructure.FormatV2, nil)
	return h
}

type Map struct {
	mut    sync.RWMutex
	objs   map[string]Object
	ring   *ring.Ring
	lastRv ring.ResourceVersion
}

func NewMap() *Map {
	return &Map{
		objs: make(map[string]Object),
		ring: ring.New(10240),
	}
}

func (m *Map) Set(obj Object) {
	m.mut.Lock()
	defer m.mut.Unlock()

	prev, ok := m.objs[obj.ID.String()]
	m.objs[obj.ID.String()] = obj

	// 更新事件去重
	// 如果之前存在过且 Event 内容均相等则不触发 ring 写入
	// 可以减少 ring 事件 因为对于 resync 时资源对象即使没有任何变更也会重新进入流程
	if ok && prev.Hash() == obj.Hash() && prev.ID == obj.ID {
		return
	}

	m.lastRv = m.ring.Put(Event{
		Action:    objectsref.ActionCreateOrUpdate,
		Name:      obj.ID.Name,
		Namespace: obj.ID.Namespace,
		Spec:      &obj.Spec,
	})
}

func (m *Map) Del(oid objectsref.ObjectID) {
	m.mut.Lock()
	defer m.mut.Unlock()

	delete(m.objs, oid.String())
	// 删除时 Spec 置空
	m.lastRv = m.ring.Put(Event{
		Action:    objectsref.ActionDelete,
		Name:      oid.Name,
		Namespace: oid.Namespace,
	})
}

func (m *Map) FetchEvents(rv int) ([]Event, int) {
	m.mut.RLock()
	defer m.mut.RUnlock()

	var events []Event
	// fetch 所有的 processmonitor 以事件的形式
	if rv <= 0 || rv < int(m.ring.MinResourceVersion()) {
		for _, obj := range m.objs {
			cloned := obj
			events = append(events, Event{
				Action:    objectsref.ActionCreateOrUpdate,
				Name:      cloned.ID.Name,
				Namespace: cloned.ID.Namespace,
				Spec:      &cloned.Spec,
			})
		}
		return events, int(m.lastRv)
	}

	objs := m.ring.ReadGt(ring.ResourceVersion(rv))
	for _, obj := range objs {
		events = append(events, obj.(Event))
	}
	return events, int(m.lastRv)
}

func (m *Map) GetByNamespace(namespace string) []Object {
	m.mut.RLock()
	defer m.mut.RUnlock()

	var ret []Object
	for _, obj := range m.objs {
		if obj.ID.Namespace == namespace {
			ret = append(ret, obj)
		}
	}
	return ret
}

func (m *Map) GetAll() []Object {
	m.mut.RLock()
	defer m.mut.RUnlock()

	ret := make([]Object, 0, len(m.objs))
	for _, obj := range m.objs {
		ret = append(ret, obj)
	}
	return ret
}
