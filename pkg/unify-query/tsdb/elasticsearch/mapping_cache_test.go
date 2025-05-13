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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMappingCache(t *testing.T) {
	ttl := 10 * time.Minute
	cache := NewMappingCache(ttl)

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.cache)
	assert.Equal(t, ttl, cache.ttl)
}

func TestMappingCache_SetTTL(t *testing.T) {
	cache := NewMappingCache(5 * time.Minute)
	assert.Equal(t, 5*time.Minute, cache.GetTTL())

	cache.SetTTL(10 * time.Minute)
	assert.Equal(t, 10*time.Minute, cache.GetTTL())
}

func TestMappingCache_AppendFieldTypesCache(t *testing.T) {
	cache := NewMappingCache(5 * time.Minute)

	tableID1 := "table1"
	tableID2 := "table2"

	mapping1 := map[string]string{
		"field1": "keyword",
		"field2": "text",
	}

	mapping2 := map[string]string{
		"field3": "integer",
		"field4": "float",
	}

	// Test first append
	cache.AppendFieldTypesCache(tableID1, mapping1)

	fieldType1, ok1 := cache.GetFieldType(tableID1, "field1")
	assert.True(t, ok1)
	assert.Equal(t, "keyword", fieldType1)

	fieldType2, ok2 := cache.GetFieldType(tableID1, "field2")
	assert.True(t, ok2)
	assert.Equal(t, "text", fieldType2)

	// Test second append to different table
	cache.AppendFieldTypesCache(tableID2, mapping2)

	fieldType3, ok3 := cache.GetFieldType(tableID2, "field3")
	assert.True(t, ok3)
	assert.Equal(t, "integer", fieldType3)

	fieldType4, ok4 := cache.GetFieldType(tableID2, "field4")
	assert.True(t, ok4)
	assert.Equal(t, "float", fieldType4)

	// Test append to existing table (update)
	updatedMapping := map[string]string{
		"field1": "date",
		"field5": "boolean",
	}

	cache.AppendFieldTypesCache(tableID1, updatedMapping)

	fieldType1Updated, ok1Updated := cache.GetFieldType(tableID1, "field1")
	assert.True(t, ok1Updated)
	assert.Equal(t, "date", fieldType1Updated)

	fieldType5, ok5 := cache.GetFieldType(tableID1, "field5")
	assert.True(t, ok5)
	assert.Equal(t, "boolean", fieldType5)

	// Original field2 should still be there
	fieldType2Again, ok2Again := cache.GetFieldType(tableID1, "field2")
	assert.True(t, ok2Again)
	assert.Equal(t, "text", fieldType2Again)
}

func TestMappingCache_GetFieldType(t *testing.T) {
	cache := NewMappingCache(5 * time.Minute)

	tableID := "table1"
	mapping := map[string]string{
		"field1": "keyword",
		"field2": "text",
	}

	// Test get on empty cache
	fieldType, ok := cache.GetFieldType(tableID, "field1")
	assert.False(t, ok)
	assert.Empty(t, fieldType)

	// Add data to cache
	cache.AppendFieldTypesCache(tableID, mapping)

	// Test get on existing field
	fieldType1, ok1 := cache.GetFieldType(tableID, "field1")
	assert.True(t, ok1)
	assert.Equal(t, "keyword", fieldType1)

	// Test get on non-existing field
	fieldTypeNonExist, okNonExist := cache.GetFieldType(tableID, "nonexistfield")
	assert.False(t, okNonExist)
	assert.Empty(t, fieldTypeNonExist)

	// Test get on non-existing table
	fieldTypeNonExistTable, okNonExistTable := cache.GetFieldType("nonexisttable", "field1")
	assert.False(t, okNonExistTable)
	assert.Empty(t, fieldTypeNonExistTable)
}

func TestMappingCache_Expiration(t *testing.T) {
	shortTTL := 10 * time.Millisecond
	cache := NewMappingCache(shortTTL)

	tableID := "table1"
	mapping := map[string]string{
		"field1": "keyword",
	}

	cache.AppendFieldTypesCache(tableID, mapping)

	// Should be in cache initially
	fieldType1, ok1 := cache.GetFieldType(tableID, "field1")
	assert.True(t, ok1)
	assert.Equal(t, "keyword", fieldType1)

	// Wait for entry to expire
	time.Sleep(20 * time.Millisecond)

	// Should be expired now
	fieldTypeExpired, okExpired := cache.GetFieldType(tableID, "field1")
	assert.False(t, okExpired)
	assert.Empty(t, fieldTypeExpired)
}

func TestMappingCache_Delete(t *testing.T) {
	cache := NewMappingCache(5 * time.Minute)

	tableID := "table1"
	mapping := map[string]string{
		"field1": "keyword",
		"field2": "text",
		"field3": "integer",
	}

	cache.AppendFieldTypesCache(tableID, mapping)

	// Verify initial state
	fieldType1, ok1 := cache.GetFieldType(tableID, "field1")
	assert.True(t, ok1)
	assert.Equal(t, "keyword", fieldType1)

	// Test delete specific field
	cache.Delete(tableID, "field1")

	// field1 should be gone
	fieldType1After, ok1After := cache.GetFieldType(tableID, "field1")
	assert.False(t, ok1After)
	assert.Empty(t, fieldType1After)

	// field2 should still be there
	fieldType2, ok2 := cache.GetFieldType(tableID, "field2")
	assert.True(t, ok2)
	assert.Equal(t, "text", fieldType2)
}

func TestMappingCache_Clear(t *testing.T) {
	cache := NewMappingCache(5 * time.Minute)

	tableID1 := "table1"
	tableID2 := "table2"

	mapping1 := map[string]string{
		"field1": "keyword",
	}

	mapping2 := map[string]string{
		"field2": "text",
	}

	cache.AppendFieldTypesCache(tableID1, mapping1)
	cache.AppendFieldTypesCache(tableID2, mapping2)

	// Verify initial state
	_, ok1 := cache.GetFieldType(tableID1, "field1")
	_, ok2 := cache.GetFieldType(tableID2, "field2")
	assert.True(t, ok1)
	assert.True(t, ok2)

	// Clear the cache
	cache.Clear()

	// All entries should be gone
	_, ok1After := cache.GetFieldType(tableID1, "field1")
	_, ok2After := cache.GetFieldType(tableID2, "field2")
	assert.False(t, ok1After)
	assert.False(t, ok2After)
}

func TestMappingCache_ConcurrentAccess(t *testing.T) {
	cache := NewMappingCache(5 * time.Minute)

	// Constants for the test
	const (
		numGoRoutines = 10
		numOperations = 100
		tablePrefix   = "table"
		fieldPrefix   = "field"
	)

	var wg sync.WaitGroup
	wg.Add(numGoRoutines * 2) // For both readers and writers

	// Launch writer goroutines
	for i := 0; i < numGoRoutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			tableID := tablePrefix + string(rune('0'+routineID))

			for j := 0; j < numOperations; j++ {
				fieldName := fieldPrefix + string(rune('0'+j%10))
				fieldType := "type" + string(rune('0'+j%5))

				mapping := map[string]string{
					fieldName: fieldType,
				}

				// Perform various operations
				switch j % 4 {
				case 0:
					// Append to cache
					cache.AppendFieldTypesCache(tableID, mapping)
				case 1:
					// Get from cache
					cache.GetFieldType(tableID, fieldName)
				case 2:
					// Delete field
					if j > 0 && j%10 == 0 {
						cache.Delete(tableID, fieldName)
					}
				case 3:
					// Set TTL
					cache.SetTTL(time.Duration(5+j%5) * time.Minute)
				}
			}
		}(i)
	}

	// Launch reader goroutines
	for i := 0; i < numGoRoutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				tableID := tablePrefix + string(rune('0'+(j%numGoRoutines)))
				fieldName := fieldPrefix + string(rune('0'+(j%10)))

				// Perform various read operations
				switch j % 3 {
				case 0:
					// Get field type
					cache.GetFieldType(tableID, fieldName)
				case 1:
					// Get TTL
					cache.GetTTL()
				case 2:
					// Occasionally clear if j is divisible by a large number
					if j > 0 && j%50 == 0 {
						cache.Clear()
					}
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestFieldTypesCache_GlobalVariable(t *testing.T) {
	// The global variable should be initialized by init()
	assert.NotNil(t, fieldTypesCache)
	assert.Equal(t, DefaultMappingCacheTTL, fieldTypesCache.GetTTL())

	// Test basic operations on the global cache
	tableID := "globalTable"
	mapping := map[string]string{
		"globalField": "globalType",
	}

	// Add to global cache
	fieldTypesCache.AppendFieldTypesCache(tableID, mapping)

	// Get from global cache
	fieldType, ok := fieldTypesCache.GetFieldType(tableID, "globalField")
	assert.True(t, ok)
	assert.Equal(t, "globalType", fieldType)

	// Clean up after test
	fieldTypesCache.Delete(tableID, "globalField")
}

func TestFieldTypeCache_MultiQueryScenario(t *testing.T) {
	// set a short TTL for testing
	ttl := 10 * time.Millisecond
	cache := NewMappingCache(ttl)

	// 第一个场景：首次查询，缓存不存在
	t.Run("First Query - Cache Miss", func(t *testing.T) {
		tableID := "rt_1"
		fieldName := "field_1"

		fieldType, exists := cache.GetFieldType(tableID, fieldName)
		assert.False(t, exists, "first query should return not exists")
		assert.Empty(t, fieldType, "first query should return empty")

		cache.AppendFieldTypesCache(tableID, map[string]string{
			fieldName: "string",
		})

		fieldType, exists = cache.GetFieldType(tableID, fieldName)
		assert.True(t, exists, "after adding, should return exists")
		assert.Equal(t, "string", fieldType, "after adding, should return correct type")
	})

	// 第二个场景：重复查询，验证缓存命中
	t.Run("Second Query - Cache Hit", func(t *testing.T) {
		tableID := "rt_1"
		fieldName := "field_1"

		for i := 0; i < 3; i++ {
			fieldType, exists := cache.GetFieldType(tableID, fieldName)
			assert.True(t, exists, "second query should return exists")
			assert.Equal(t, "string", fieldType, "second query should return correct type")
		}
	})

	// 第三个场景：测试缓存过期
	t.Run("Third Query - Cache Expiration", func(t *testing.T) {
		tableID := "rt_2"
		fieldName := "field_2"

		cache.AppendFieldTypesCache(tableID, map[string]string{
			fieldName: "integer",
		})

		fieldType, exists := cache.GetFieldType(tableID, fieldName)
		assert.True(t, exists, "add to cache should return exists")
		assert.Equal(t, "integer", fieldType, "should return correct type")

		// wait for expiration
		time.Sleep(ttl * 2)

		fieldType, exists = cache.GetFieldType(tableID, fieldName)
		assert.False(t, exists, "after expiration, should return not exists")
		assert.Empty(t, fieldType, "after expiration, should return empty")
	})

	// 第四个场景：测试映射更新
	t.Run("Fourth Query - Mapping Update", func(t *testing.T) {
		tableID := "rt_3"
		fieldName := "field_3"

		cache.AppendFieldTypesCache(tableID, map[string]string{
			fieldName: "text",
		})

		fieldType, exists := cache.GetFieldType(tableID, fieldName)
		assert.True(t, exists)
		assert.Equal(t, "text", fieldType)

		// update mapping
		cache.AppendFieldTypesCache(tableID, map[string]string{
			fieldName: "keyword",
		})

		fieldType, exists = cache.GetFieldType(tableID, fieldName)
		assert.True(t, exists)
		assert.Equal(t, "keyword", fieldType, "should return correct type")
	})
}

func Test_createCacheKey(t *testing.T) {
	tests := []struct {
		tableID  string
		field    string
		expected string
	}{
		{
			tableID:  "table1",
			field:    "field1",
			expected: "table1:field1",
		},
		{
			tableID:  "table2",
			field:    "field2",
			expected: "table2:field2",
		},
		{
			tableID:  "",
			field:    "field3",
			expected: ":field3",
		},
		{
			tableID:  "table4",
			field:    "",
			expected: "table4:",
		},
	}

	for _, tc := range tests {
		name := fmt.Sprintf("tableID=%s,field=%s", tc.tableID, tc.field)
		t.Run(name, func(t *testing.T) {
			result := createCacheKey(tc.tableID, tc.field)
			assert.Equal(t, tc.expected, result)
		})
	}
}
