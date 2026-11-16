// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"encoding/binary"
	"fmt"
	"maps"
	"sync"

	xxhash "github.com/cespare/xxhash/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// 字节缓冲区池，预分配256字节的缓冲区
var bufPool = sync.Pool{
	New: func() any {
		return make([]byte, 0, 256)
	},
}

// Matcher对象池，预分配8个键值对的容量
var matcherPool = sync.Pool{
	New: func() any {
		return make(cmdb.Matcher, 8)
	},
}

// resourceType 资源类型映射器
// 用于将资源类型名称映射为 uint16 ID，节省内存
type resourceType struct {
	lock sync.RWMutex // 读写锁，保证并发安全

	index uint16 // 下一个可用的资源类型ID

	data   map[cmdb.Resource]uint16 // 资源名称到ID的映射
	reData map[uint16]cmdb.Resource // ID到资源名称的映射
}

// newResourceType 创建新的资源类型映射器
// 返回: 新创建的 resourceType 指针
func newResourceType() *resourceType {
	return &resourceType{
		index:  1,
		data:   make(map[cmdb.Resource]uint16),
		reData: make(map[uint16]cmdb.Resource),
	}
}

// name 根据资源类型ID获取资源名称
// 参数:
//   - id: 资源类型ID
//
// 返回: 资源名称，如果ID不存在则返回空字符串
func (r *resourceType) name(id uint16) cmdb.Resource {
	r.lock.RLock()
	defer r.lock.RUnlock()

	if name, ok := r.reData[id]; ok {
		return name
	}

	return ""
}

// id 获取资源类型对应的ID，如果不存在则创建新的ID
// 参数:
//   - resource: 资源类型名称
//
// 返回: 资源类型对应的 uint16 ID
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

func newNodeBuilder(cfg *TimeGraphConfig) *NodeBuilder {
	return &NodeBuilder{
		index:    1,
		resource: newResourceType(),
		data:     make(map[uint64]uint64),
		info:     make(map[uint64]cmdb.Matcher),
		config:   resourceConfigSnapshot(cfg),
	}
}

// NodeBuilder 节点构建器
// 负责创建和管理唯一的节点ID，将复杂的资源匹配器压缩为 uint64 ID
type NodeBuilder struct {
	lock sync.RWMutex // 读写锁，保证并发安全

	resource *resourceType // 资源类型映射器

	index uint64            // 下一个可用的节点ID
	data  map[uint64]uint64 // hashID到nodeID的映射

	info map[uint64]cmdb.Matcher // nodeID到原始匹配器的映射

	// config 是模型级配置快照。TimeGraph 查询不能读取进程级资源配置，因为
	// 不同 namespace 可能会并发服务。
	config map[cmdb.Resource]TimeGraphResourceConfig
}

func resourceConfigSnapshot(cfg *TimeGraphConfig) map[cmdb.Resource]TimeGraphResourceConfig {
	if cfg == nil {
		return nil
	}
	result := make(map[cmdb.Resource]TimeGraphResourceConfig, len(cfg.Resource))
	for _, resource := range cfg.Resource {
		result[resource.Name] = resource
	}
	return result
}

// resourceIndexView 仅供内部只读访问；resourceIndexes 仍返回自有切片。
func (n *NodeBuilder) resourceIndexView(resourceType cmdb.Resource) cmdb.Index {
	return n.config[resourceType].Index
}

func (n *NodeBuilder) resourceIndexes(resourceTypes ...cmdb.Resource) cmdb.Index {
	var result cmdb.Index
	for _, resourceType := range resourceTypes {
		result = append(result, n.config[resourceType].Index...)
	}
	return result
}

func (n *NodeBuilder) resourceInfoFields(resourceType cmdb.Resource) cmdb.Index {
	return n.config[resourceType].Info
}

// Clean 清理节点构建器的所有数据
// 将所有 Matcher 对象归还到对象池中，重置所有映射关系
func (n *NodeBuilder) Clean() {
	n.lock.Lock()
	defer n.lock.Unlock()

	// 归还所有Matcher对象到池中
	for _, matcher := range n.info {
		// Keep reusable map capacity without retaining this request's labels.
		clear(matcher)
		matcherPool.Put(matcher)
	}

	n.index = 1
	n.data = make(map[uint64]uint64)
	n.info = make(map[uint64]cmdb.Matcher)
}

// Length 获取当前节点总数
// 返回: 已创建的节点数量
func (n *NodeBuilder) Length() int {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return len(n.data)
}

// resourceName 直接读取实例类型映射，不复制节点属性。
func (n *NodeBuilder) resourceName(id uint64) cmdb.Resource {
	return n.resource.name(uint16(id >> 48))
}

// Info 根据节点ID获取资源类型和匹配器信息
// 参数:
//   - id: 节点ID（uint64，前16位为资源类型ID）
//
// 返回:
//   - cmdb.Resource: 资源类型名称
//   - cmdb.Matcher: 资源匹配器，如果节点不存在则返回 nil
func (n *NodeBuilder) Info(id uint64) (cmdb.Resource, cmdb.Matcher) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	// 提取前16位作为资源类型ID
	resourceTypeID := uint16(id >> 48)
	resourceName := n.resource.name(resourceTypeID)

	if info, ok := n.info[id]; ok {
		return resourceName, cloneMatcher(info)
	}

	return resourceName, nil
}

// matchingInfoView 只借用存储中不可变的 matcher，供图存储使用。
// GetID 更新时会替换旧 map；只有所属 TimeGraph 持锁期间可以借用，
// 因而 Clean 不会在读图过程中回收该 map。
func (n *NodeBuilder) matchingInfoView(id uint64, incoming cmdb.Matcher) (cmdb.Matcher, bool) {
	n.lock.RLock()
	defer n.lock.RUnlock()
	stored, exists := n.info[id]
	if !exists || (stored == nil) != (incoming == nil) || !maps.Equal(stored, incoming) {
		return nil, false
	}
	return stored, true
}

// ResourceNodeInfo returns owned matchers for the resource query builder.
func (n *NodeBuilder) ResourceNodeInfo(resourceType cmdb.Resource) []cmdb.Matcher {
	n.lock.RLock()
	defer n.lock.RUnlock()

	resourceID := n.resource.id(resourceType)

	infos := make([]cmdb.Matcher, 0)
	for id, info := range n.info {
		resourceTypeID := uint16(id >> 48)
		if resourceTypeID == resourceID {
			infos = append(infos, cloneMatcher(info))
		}
	}
	return infos
}

// cloneMatcher 防止调用方持有节点构建器复用池中的 matcher。
// 尤其是 Clean 会把内部 map 归还对象池；如果不复制，下一次查询可能改写
// 已经返回给调用方的结果。
func cloneMatcher(matcher cmdb.Matcher) cmdb.Matcher {
	if matcher == nil {
		return nil
	}
	result := make(cmdb.Matcher, len(matcher))
	for key, value := range matcher {
		result[key] = value
	}
	return result
}

func mergeMatcher(base, extra cmdb.Matcher) cmdb.Matcher {
	result := cloneMatcher(base)
	if result == nil {
		result = make(cmdb.Matcher, len(extra))
	}
	for key, value := range extra {
		// 同一时间点可能有多条 VM series 描述同一个节点。属性冲突时取字典序较大的
		// 值作为确定性的裁决规则，不依赖 VM 返回顺序。
		if current, ok := result[key]; !ok || value > current {
			result[key] = value
		}
	}
	return result
}

func appendLengthPrefixed(buf []byte, value string) []byte {
	var length [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(length[:], uint64(len(value)))
	buf = append(buf, length[:n]...)
	return append(buf, value...)
}

// GetID 根据资源类型和匹配器信息获取或创建节点ID
// 相同资源类型和匹配器信息的组合会返回相同的节点ID（节点去重）
// 参数:
//   - resourceType: 资源类型名称，如 "pod", "container" 等
//   - info: 资源匹配器，必须包含该资源类型所需的所有索引字段
//
// 返回:
//   - uint64: 节点ID，前16位为资源类型ID，后48位为节点序号
//   - error: 错误信息，如果资源类型为空、匹配器为空或缺少必需的索引字段则返回错误
//
// 节点ID结构:
//   - 前16位（bit 48-63）: 资源类型ID
//   - 后48位（bit 0-47）: 节点序号
//
// 注意:
//   - 使用 xxhash 计算匹配器信息的哈希值，用于节点去重
//   - 如果匹配器缺少资源类型要求的索引字段，会返回错误
//   - 使用对象池复用字节缓冲区，提高性能
func (n *NodeBuilder) GetID(resourceType cmdb.Resource, info cmdb.Matcher) (uint64, error) {
	if resourceType == "" {
		return 0, errors.New(ErrEmptyResource)
	}
	if info == nil {
		return 0, errors.New(ErrEmptyMatcher)
	}

	indexes := n.resourceIndexView(resourceType)
	if len(indexes) == 0 {
		return 0, fmt.Errorf(ErrIndexNotMatchIndex, resourceType)
	}

	// 使用对象池获取字节缓冲区
	buf := bufPool.Get().([]byte)
	buf = buf[:0] // 重置长度，保留容量

	defer func() {
		// 如果缓冲区容量过大，不归还池中
		if cap(buf) <= 1024 {
			bufPool.Put(buf)
		}
	}()

	// 身份中的每一部分都使用长度前缀编码。仅靠分隔符拼接会在主键值包含分隔符
	// 时产生歧义，把不同的主键元组合并成同一个节点。
	buf = appendLengthPrefixed(buf, string(resourceType))
	for _, k := range indexes {
		if _, ok := info[k]; !ok {
			return 0, fmt.Errorf(ErrIndexNotMatchIndex, k)
		}
		buf = appendLengthPrefixed(buf, k)
		buf = appendLengthPrefixed(buf, info[k])
	}

	// 使用更高效的哈希计算
	hashID := xxhash.Sum64(buf)

	n.lock.Lock()
	defer n.lock.Unlock()

	// 检查缓存
	if id, ok := n.data[hashID]; ok {
		infoFields := n.resourceInfoFields(resourceType)
		var candidate cmdb.Matcher
		for _, key := range infoFields {
			if value, exists := info[key]; exists {
				current, present := n.info[id][key]
				if present && value <= current {
					continue
				}
				if candidate == nil {
					candidate = make(cmdb.Matcher, len(infoFields))
				}
				candidate[key] = value
			}
		}
		if len(candidate) > 0 {
			n.info[id] = mergeMatcher(n.info[id], candidate)
		}
		return id, nil
	}

	// 创建新的节点ID
	rtID := n.resource.id(resourceType)
	finaID := (uint64(rtID) << 48) | (n.index & 0xFFFFFFFFFFFF)

	// 使用对象池获取Matcher对象
	matcher := matcherPool.Get().(cmdb.Matcher)
	// 清空现有内容但保留容量
	for k := range matcher {
		delete(matcher, k)
	}

	for _, k := range indexes {
		matcher[k] = info[k]
	}

	// 同时存储信息字段
	infoFields := n.resourceInfoFields(resourceType)
	for _, k := range infoFields {
		if _, present := matcher[k]; present {
			continue // Indexes and repeated info fields were already populated.
		}
		if v, ok := info[k]; ok {
			matcher[k] = v
		}
	}

	n.data[hashID] = finaID
	n.info[finaID] = matcher
	n.index++

	return finaID, nil
}
