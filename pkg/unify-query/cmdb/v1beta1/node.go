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

// StringDict 字符串字典压缩器
// 用于将字符串压缩为 uint64 ID，节省内存空间
// 支持双向映射：字符串 -> ID 和 ID -> 字符串
type StringDict struct {
	lock    sync.RWMutex      // 读写锁，保证并发安全
	strToID map[string]uint64 // 字符串到ID的映射
	idToStr map[uint64]string // ID到字符串的映射
	nextID  uint64            // 下一个可用的ID，从1开始递增
}

// NewStringDict 创建新的字符串字典
// 返回: 新创建的 StringDict 指针
// 注意: ID 从 1 开始，0 保留为无效值
func NewStringDict() *StringDict {
	return &StringDict{
		strToID: make(map[string]uint64),
		idToStr: make(map[uint64]string),
		nextID:  1, // 从1开始，0保留为无效值
	}
}

// GetID 获取字符串对应的压缩ID
// 如果字符串不存在，则创建新的ID并建立映射关系
// 参数:
//   - s: 要压缩的字符串，如果为空字符串则返回 0
//
// 返回: 字符串对应的 uint64 ID
// 注意: 使用双重检查锁定模式，确保并发安全
func (d *StringDict) GetID(s string) uint64 {
	if s == "" {
		return 0
	}

	d.lock.RLock()
	if id, ok := d.strToID[s]; ok {
		d.lock.RUnlock()
		return id
	}
	d.lock.RUnlock()

	d.lock.Lock()
	defer d.lock.Unlock()

	// 双重检查，避免并发重复添加
	if id, ok := d.strToID[s]; ok {
		return id
	}

	id := d.nextID
	d.strToID[s] = id
	d.idToStr[id] = s
	d.nextID++

	// 使用uint64提供更大的ID空间，避免溢出风险
	// 移除了危险的循环使用逻辑，确保ID唯一性

	return id
}

// GetString 根据压缩ID获取原始字符串
// 参数:
//   - id: 压缩ID，如果为 0 则返回空字符串
//
// 返回: 对应的原始字符串，如果ID不存在则返回空字符串
func (d *StringDict) GetString(id uint64) string {
	if id == 0 {
		return ""
	}

	d.lock.RLock()
	defer d.lock.RUnlock()

	if s, ok := d.idToStr[id]; ok {
		return s
	}

	return ""
}

// GetStats 获取字典统计信息
// 返回: 包含统计信息的映射表
//   - total_strings: 当前存储的字符串总数
//   - next_id: 下一个可用的ID
//   - overflow_warned: 是否接近溢出警告阈值
//   - max_safe_id: 最大安全ID值
func (d *StringDict) GetStats() map[string]any {
	d.lock.RLock()
	defer d.lock.RUnlock()

	return map[string]any{
		"total_strings":   len(d.strToID),
		"next_id":         d.nextID,
		"overflow_warned": d.nextID >= (1<<63 - 1000), // 接近uint64最大值时警告
		"max_safe_id":     uint64(1<<63 - 1),
	}
}

// globalStringDict 全局字符串字典，用于压缩常用字符串
// 当 NodeBuilder 未指定 StringDict 时使用此全局字典
var globalStringDict = NewStringDict()

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

// NewNodeBuilder 创建新的节点构建器
// 参数:
//   - stringDict: 字符串字典，如果为 nil 则使用全局字典（向后兼容）
//
// 返回: 新创建的 NodeBuilder 指针
func NewNodeBuilder(stringDict *StringDict) *NodeBuilder {
	if stringDict == nil {
		// 如果未提供StringDict，使用全局字典（向后兼容）
		stringDict = globalStringDict
	}

	return &NodeBuilder{
		index:          1,
		resource:       newResourceType(),
		data:           make(map[uint64]uint64),
		reData:         make(map[uint64]uint64),
		info:           make(map[uint64]cmdb.Matcher),
		compressedInfo: make(map[uint64]map[uint64]uint64),
		stringDict:     stringDict,
	}
}

// NodeBuilder 节点构建器
// 负责创建和管理唯一的节点ID，将复杂的资源匹配器压缩为 uint64 ID
// 使用字符串字典压缩节点信息中的字符串，进一步节省内存
type NodeBuilder struct {
	lock sync.RWMutex // 读写锁，保证并发安全

	resource *resourceType // 资源类型映射器

	index  uint64            // 下一个可用的节点ID
	data   map[uint64]uint64 // hashID到nodeID的映射
	reData map[uint64]uint64 // nodeID到hashID的映射

	info map[uint64]cmdb.Matcher // nodeID到原始匹配器的映射

	// 压缩存储：使用压缩ID替代完整字符串
	// 结构: nodeID -> (keyID -> valueID)
	// keyID 和 valueID 都是通过 StringDict 压缩得到的
	compressedInfo map[uint64]map[uint64]uint64

	// 字符串字典引用，支持使用TimeGraph的局部字典
	stringDict *StringDict
}

// Clean 清理节点构建器的所有数据
// 将所有 Matcher 对象归还到对象池中，重置所有映射关系
func (n *NodeBuilder) Clean() {
	n.lock.Lock()
	defer n.lock.Unlock()

	// 归还所有Matcher对象到池中
	for _, matcher := range n.info {
		matcherPool.Put(matcher)
	}

	n.index = 1
	n.data = make(map[uint64]uint64)
	n.reData = make(map[uint64]uint64)
	n.info = make(map[uint64]cmdb.Matcher)
	n.compressedInfo = make(map[uint64]map[uint64]uint64)
}

// Length 获取当前节点总数
// 返回: 已创建的节点数量
func (n *NodeBuilder) Length() int {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return len(n.data)
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
		return resourceName, info
	}

	return resourceName, nil
}

// ResourceNodeInfo 获取指定资源类型下的所有节点信息
// 参数:
//   - resourceType: 资源类型名称
//
// 返回: 该资源类型下所有节点的匹配器列表
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

// GetCompressedInfo 获取压缩后的节点信息
// 参数:
//   - nodeID: 节点ID
//
// 返回: 压缩后的信息映射表（keyID -> valueID），如果节点不存在则返回 nil
func (n *NodeBuilder) GetCompressedInfo(nodeID uint64) map[uint64]uint64 {
	n.lock.RLock()
	defer n.lock.RUnlock()

	if compressed, ok := n.compressedInfo[nodeID]; ok {
		return compressed
	}
	return nil
}

// GetOriginalInfoFromCompressed 从压缩信息还原原始匹配器信息
// 参数:
//   - compressed: 压缩后的信息映射表（keyID -> valueID）
//
// 返回: 还原后的匹配器，如果 compressed 为 nil 则返回 nil
// 注意: 如果 keyID 或 valueID 在字符串字典中不存在，对应的键值对会被跳过
func (n *NodeBuilder) GetOriginalInfoFromCompressed(compressed map[uint64]uint64) cmdb.Matcher {
	if compressed == nil {
		return nil
	}

	matcher := make(cmdb.Matcher, len(compressed))
	for keyID, valueID := range compressed {
		key := n.stringDict.GetString(keyID)
		value := n.stringDict.GetString(valueID)
		if key != "" && value != "" {
			matcher[key] = value
		}
	}

	return matcher
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

	indexes := ResourcesIndex(resourceType)
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

	// 使用对象池获取Matcher对象
	matcher := matcherPool.Get().(cmdb.Matcher)
	// 清空现有内容但保留容量
	for k := range matcher {
		delete(matcher, k)
	}

	// 创建压缩存储映射
	compressedMap := make(map[uint64]uint64, len(indexes))

	// 填充索引字段，同时创建压缩映射
	for _, k := range indexes {
		matcher[k] = info[k]
		// 使用局部字符串字典压缩键名和值
		keyID := n.stringDict.GetID(k)
		valueID := n.stringDict.GetID(info[k])
		compressedMap[keyID] = valueID
	}

	// 同时存储信息字段
	infoFields := ResourcesInfo(resourceType)
	for _, k := range infoFields {
		if v, ok := info[k]; ok {
			matcher[k] = v
			// 使用局部字符串字典压缩键名和值
			keyID := n.stringDict.GetID(k)
			valueID := n.stringDict.GetID(v)
			compressedMap[keyID] = valueID
		}
	}

	n.data[hashID] = finaID
	n.reData[finaID] = hashID
	n.info[finaID] = matcher
	n.compressedInfo[finaID] = compressedMap
	n.index++

	return finaID, nil
}
