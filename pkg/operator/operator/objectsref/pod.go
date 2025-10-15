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
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref/ring"
)

type PodEvent struct {
	Action    Action
	IP        string
	Name      string
	Namespace string
}

type ContainerKey struct {
	Name    string
	ID      string
	ImageID string
	Tag     string
}

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
	mut    sync.RWMutex
	objs   map[string]PodObject
	ring   *ring.Ring
	lastRv ring.ResourceVersion
}

func NewPodMap() *PodMap {
	return &PodMap{
		objs: make(map[string]PodObject),
		ring: ring.New(10240),
	}
}

func (m *PodMap) Set(obj PodObject) {
	m.mut.Lock()
	defer m.mut.Unlock()

	prev, ok := m.objs[obj.ID.String()]
	m.objs[obj.ID.String()] = obj

	// PodIP 为空则表示该 pod 是未就绪状态 无法提供服务
	if obj.PodIP == "" {
		return
	}

	// 更新事件去重
	// 如果之前存在过且 PodEvent 内容均相等则不触发 ring 写入
	// 可以减少 ring 事件 因为对于 resync 时资源对象即使没有任何变更也会重新进入流程
	if ok && prev.PodIP == obj.PodIP && prev.ID == obj.ID {
		return
	}

	m.lastRv = m.ring.Put(PodEvent{
		Action:    ActionCreateOrUpdate,
		IP:        obj.PodIP,
		Name:      obj.ID.Name,
		Namespace: obj.ID.Namespace,
	})
}

func (m *PodMap) Del(oid ObjectID) {
	m.mut.Lock()
	defer m.mut.Unlock()

	var podIP string
	if v, ok := m.objs[oid.String()]; ok {
		podIP = v.PodIP
	}

	delete(m.objs, oid.String())
	m.lastRv = m.ring.Put(PodEvent{
		Action:    ActionDelete,
		IP:        podIP,
		Name:      oid.Name,
		Namespace: oid.Namespace,
	})
}

func (m *PodMap) FetchEvents(rv int) ([]PodEvent, int) {
	m.mut.RLock()
	defer m.mut.RUnlock()

	var events []PodEvent
	// fetch 所有的 pods 以事件的形式
	if rv <= 0 || rv < int(m.ring.MinResourceVersion()) {
		for _, obj := range m.objs {
			events = append(events, PodEvent{
				Action:    ActionCreateOrUpdate,
				IP:        obj.PodIP,
				Name:      obj.ID.Name,
				Namespace: obj.ID.Namespace,
			})
		}
		return events, int(m.lastRv)
	}

	objs := m.ring.ReadGt(ring.ResourceVersion(rv))
	for _, obj := range objs {
		events = append(events, obj.(PodEvent))
	}
	return events, int(m.lastRv)
}

func (m *PodMap) Counter() map[string]int {
	m.mut.RLock()
	defer m.mut.RUnlock()

	ret := make(map[string]int)
	for _, obj := range m.objs {
		ret[obj.ID.Namespace]++
	}
	return ret
}

func (m *PodMap) GetByNodeName(nodeName string) []PodObject {
	m.mut.RLock()
	defer m.mut.RUnlock()

	var ret []PodObject
	for _, obj := range m.objs {
		if obj.NodeName == nodeName {
			ret = append(ret, obj)
		}
	}
	return ret
}

func (m *PodMap) GetByNamespace(namespace string) []PodObject {
	m.mut.RLock()
	defer m.mut.RUnlock()

	var ret []PodObject
	for _, obj := range m.objs {
		if obj.ID.Namespace == namespace {
			ret = append(ret, obj)
		}
	}
	return ret
}

func (m *PodMap) CheckIP(s string) bool {
	m.mut.RLock()
	defer m.mut.RUnlock()

	for _, obj := range m.objs {
		if obj.PodIP == s {
			return true
		}
	}
	return false
}

func (m *PodMap) GetAll() []PodObject {
	m.mut.RLock()
	defer m.mut.RUnlock()

	ret := make([]PodObject, 0, len(m.objs))
	for _, obj := range m.objs {
		ret = append(ret, obj)
	}
	return ret
}

func (m *PodMap) GetRefs(oid ObjectID) ([]OwnerRef, bool) {
	m.mut.RLock()
	defer m.mut.RUnlock()

	obj, ok := m.objs[oid.String()]
	return obj.OwnerRefs, ok
}

func toContainerKey(pod *corev1.Pod) []ContainerKey {
	var containers []ContainerKey
	for _, sc := range pod.Status.ContainerStatuses {
		ck := ContainerKey{
			Name:    sc.Name,
			ID:      sc.ContainerID,
			ImageID: sc.ImageID,
		}

		img := strings.Split(sc.Image, ":")
		if len(img) > 0 {
			ck.Tag = img[len(img)-1]
		}
		containers = append(containers, ck)
	}
	return containers
}
