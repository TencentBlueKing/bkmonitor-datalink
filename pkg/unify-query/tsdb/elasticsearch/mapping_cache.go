// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"strings"
	"time"
)

// MappingEntry 保存缓存映射数据和最后更新时间
type MappingEntry struct {
	mappings    []map[string]any
	lastUpdated time.Time
}

func (m MappingEntry) IsExpired(ttl time.Duration) bool {
	return time.Now().After(m.lastUpdated.Add(ttl))
}

// MappingCache 保存缓存映射
// 结构: map[tableID]map[fieldsStr]MappingEntry
type MappingCache map[string]map[string]MappingEntry

// Put 添加映射到缓存
func (m *MappingCache) Put(tableID string, fieldsStr string, mappings []map[string]any) {
	if m == nil {
		return
	}

	if *m == nil {
		*m = make(MappingCache)
	}

	if _, ok := (*m)[tableID]; !ok {
		(*m)[tableID] = make(map[string]MappingEntry)
	}

	(*m)[tableID][fieldsStr] = MappingEntry{
		mappings:    mappings,
		lastUpdated: time.Now(),
	}
}

// Get 从缓存获取映射条目，自动处理过期条目
func (m *MappingCache) Get(tableID string, fieldsStr string, ttl time.Duration) (MappingEntry, bool) {
	if m == nil || *m == nil {
		return MappingEntry{}, false
	}

	tableMap, ok := (*m)[tableID]
	if !ok {
		return MappingEntry{}, false
	}

	entry, ok := tableMap[fieldsStr]
	if !ok {
		return MappingEntry{}, false
	}

	if entry.IsExpired(ttl) {
		delete(tableMap, fieldsStr)
		if len(tableMap) == 0 {
			delete(*m, tableID)
		}
		return MappingEntry{}, false
	}

	return entry, true
}

// Delete 从缓存删除映射
func (m *MappingCache) Delete(tableID string, fieldsStr string) {
	if m == nil || *m == nil {
		return
	}

	tableMap, ok := (*m)[tableID]
	if !ok {
		return
	}

	if fieldsStr == "" {
		delete(*m, tableID)
	} else {
		delete(tableMap, fieldsStr)
		if len(tableMap) == 0 {
			delete(*m, tableID)
		}
	}
}

// Clear 清空缓存
func (m *MappingCache) Clear() {
	if m != nil {
		*m = make(MappingCache)
	}
}

// checkIsMappingCached 检查映射是否已缓存
func (i *Instance) checkIsMappingCached(queryIdentifier string) ([]map[string]any, bool) {
	parts := strings.Split(queryIdentifier, "|")
	if len(parts) < 1 {
		return nil, false
	}

	tableID := parts[0]
	fieldsStr := ""
	if len(parts) > 1 {
		fieldsStr = strings.Join(parts[1:], "|")
	}

	i.mappingCacheLock.RLock()
	defer i.mappingCacheLock.RUnlock()

	entry, exist := i.mappingCache.Get(tableID, fieldsStr, i.mappingTTL)
	if !exist {
		return nil, false
	}

	return entry.mappings, true
}

// writeMappings 写入映射到缓存
func (i *Instance) writeMappings(mappings []map[string]any, queryIdentifier string) error {
	if len(mappings) == 0 {
		return fmt.Errorf("cannot cache empty mappings")
	}

	parts := strings.Split(queryIdentifier, "|")
	if len(parts) < 1 {
		return fmt.Errorf("invalid query identifier format: %s", queryIdentifier)
	}

	tableID := parts[0]
	fieldsStr := ""
	if len(parts) > 1 {
		fieldsStr = strings.Join(parts[1:], "|")
	}

	i.mappingCacheLock.Lock()
	defer i.mappingCacheLock.Unlock()

	i.mappingCache.Put(tableID, fieldsStr, mappings)
	return nil
}
