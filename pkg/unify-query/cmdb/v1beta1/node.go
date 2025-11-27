// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"fmt"
	"sync"

	xxhash "github.com/cespare/xxhash/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type resourceType struct {
	lock sync.RWMutex

	index uint16

	data   map[cmdb.Resource]uint16
	reData map[uint16]cmdb.Resource
}

func newResourceType() *resourceType {
	return &resourceType{
		index:  1,
		data:   make(map[cmdb.Resource]uint16),
		reData: make(map[uint16]cmdb.Resource),
	}
}

func (r *resourceType) name(id uint16) cmdb.Resource {
	r.lock.RLock()
	defer r.lock.RUnlock()

	if name, ok := r.reData[id]; ok {
		return name
	}

	return ""
}

func (r *resourceType) id(resource cmdb.Resource) uint16 {
	r.lock.Lock()
	defer r.lock.Unlock()

	if id, ok := r.data[resource]; ok {
		return id
	}

	defer func() {
		r.index++
	}()
	r.data[resource] = r.index
	r.reData[r.index] = resource
	return r.index
}

func NewNodeBuilder() *NodeBuilder {
	return &NodeBuilder{
		index:    1,
		resource: newResourceType(),
		data:     make(map[uint64]uint64),
		reData:   make(map[uint64]uint64),
		info:     make(map[uint64]cmdb.Matcher),
	}
}

type NodeBuilder struct {
	lock sync.RWMutex

	resource *resourceType

	index  uint64
	data   map[uint64]uint64
	reData map[uint64]uint64

	info map[uint64]cmdb.Matcher
}

func (n *NodeBuilder) Clean() {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.index = 1
	n.data = make(map[uint64]uint64)
	n.reData = make(map[uint64]uint64)
	n.info = make(map[uint64]cmdb.Matcher)
}

func (n *NodeBuilder) Length() int {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return len(n.data)
}

func (n *NodeBuilder) Info(id uint64) (cmdb.Resource, cmdb.Matcher) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	// 提取前16位作为资源类型ID
	resourceTypeID := uint16(id >> 48)
	resourceName := n.resource.name(resourceTypeID)

	if info, ok := n.info[id]; ok {
		return resourceName, info
	}

	return resourceName, nil
}

func (n *NodeBuilder) ResourceNodeInfo(resourceType cmdb.Resource) []cmdb.Matcher {
	n.lock.RLock()
	defer n.lock.RUnlock()

	resourceID := n.resource.id(resourceType)

	infos := make([]cmdb.Matcher, 0)
	for id, info := range n.info {
		resourceTypeID := uint16(id >> 48)
		if resourceTypeID == resourceID {
			infos = append(infos, info)
		}
	}
	return infos
}

func (n *NodeBuilder) GetID(resourceType cmdb.Resource, info cmdb.Matcher) (uint64, error) {
	if resourceType == "" {
		return 0, errors.New(ErrEmptyResource)
	}
	if info == nil {
		return 0, errors.New(ErrEmptyMatcher)
	}

	indexes := ResourcesIndex(resourceType)
	if len(indexes) == 0 {
		return 0, fmt.Errorf(ErrIndexNotMatchIndex, resourceType)
	}

	// 预分配固定大小的字节切片，避免重复分配
	const maxKeyValueSize = 256
	buf := make([]byte, 0, maxKeyValueSize*len(indexes))

	for _, k := range indexes {
		if _, ok := info[k]; !ok {
			return 0, fmt.Errorf(ErrIndexNotMatchIndex, k)
		}

		// 直接写入字节切片，避免字符串转换
		buf = append(buf, k...)
		buf = append(buf, '=')
		buf = append(buf, info[k]...)
		buf = append(buf, '|')
	}

	// 使用更高效的哈希计算
	hashID := xxhash.Sum64(buf)

	n.lock.Lock()
	defer n.lock.Unlock()

	// 检查缓存
	if id, ok := n.data[hashID]; ok {
		return id, nil
	}

	// 创建新的节点ID
	rtID := n.resource.id(resourceType)
	finaID := (uint64(rtID) << 48) | (n.index & 0xFFFFFFFFFFFF)

	// 创建matcher时复用info，避免复制
	matcher := make(cmdb.Matcher, len(indexes))
	for _, k := range indexes {
		matcher[k] = info[k]
	}

	n.data[hashID] = finaID
	n.reData[finaID] = hashID
	n.info[finaID] = matcher
	n.index++

	return finaID, nil
}
