// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScrollGenerateQueryTsKeyWithUsername(t *testing.T) {
	queryTs := map[string]interface{}{
		"space_uid": "test_space",
		"start":     "1h",
		"end":       "now",
		"query_list": []map[string]interface{}{
			{
				"table_id":    "test_table",
				"field_name":  "test_field",
				"data_source": "prometheus",
			},
		},
	}

	testCases := []struct {
		name     string
		queryTs  interface{}
		username string
		expected map[string]interface{}
	}{
		{
			name:     "normal case with username",
			queryTs:  queryTs,
			username: "test_user",
			expected: map[string]interface{}{
				"queryTs":  queryTs,
				"username": "test_user",
			},
		},
		{
			name:     "empty username",
			queryTs:  queryTs,
			username: "",
			expected: map[string]interface{}{
				"queryTs":  queryTs,
				"username": "",
			},
		},
		{
			name:     "different username",
			queryTs:  queryTs,
			username: "another_user",
			expected: map[string]interface{}{
				"queryTs":  queryTs,
				"username": "another_user",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 生成键
			key, err := ScrollGenerateQueryTsKey(tc.queryTs, tc.username)
			assert.NoError(t, err)
			assert.NotEmpty(t, key)

			// 验证键的内容
			var keyMap map[string]interface{}
			err = json.Unmarshal([]byte(key), &keyMap)
			assert.NoError(t, err)

			// 验证包含正确的字段
			assert.Contains(t, keyMap, "queryTs")
			assert.Contains(t, keyMap, "username")
			assert.Equal(t, tc.username, keyMap["username"])

			// 验证queryTs字段不为空
			assert.NotNil(t, keyMap["queryTs"])
		})
	}
}

func TestScrollGenerateQueryTsKeyUniqueness(t *testing.T) {
	queryTs := map[string]interface{}{
		"space_uid": "test_space",
		"start":     "1h",
		"end":       "now",
	}

	// 测试相同queryTs但不同username生成不同的键
	key1, err := ScrollGenerateQueryTsKey(queryTs, "user1")
	assert.NoError(t, err)

	key2, err := ScrollGenerateQueryTsKey(queryTs, "user2")
	assert.NoError(t, err)

	assert.NotEqual(t, key1, key2, "不同用户应该生成不同的键")

	// 测试相同queryTs和username生成相同的键
	key3, err := ScrollGenerateQueryTsKey(queryTs, "user1")
	assert.NoError(t, err)

	assert.Equal(t, key1, key3, "相同用户和查询应该生成相同的键")
}

func TestScrollGenerateQueryTsKeyWithDifferentQueryTs(t *testing.T) {
	username := "test_user"

	queryTs1 := map[string]interface{}{
		"space_uid": "space1",
		"start":     "1h",
		"end":       "now",
	}

	queryTs2 := map[string]interface{}{
		"space_uid": "space2",
		"start":     "1h",
		"end":       "now",
	}

	// 测试不同queryTs生成不同的键
	key1, err := ScrollGenerateQueryTsKey(queryTs1, username)
	assert.NoError(t, err)

	key2, err := ScrollGenerateQueryTsKey(queryTs2, username)
	assert.NoError(t, err)

	assert.NotEqual(t, key1, key2, "不同查询应该生成不同的键")
}

func TestGetSessionKeyAndLockKey(t *testing.T) {
	queryTs := map[string]interface{}{
		"space_uid": "test_space",
		"start":     "1h",
		"end":       "now",
	}
	username := "test_user"

	queryTsKey, err := ScrollGenerateQueryTsKey(queryTs, username)
	assert.NoError(t, err)

	// 测试会话键生成
	sessionKey := GetSessionKey(queryTsKey)
	assert.True(t, len(sessionKey) > len(SessionKeyPrefix))
	assert.Contains(t, sessionKey, SessionKeyPrefix)

	// 测试锁键生成
	lockKey := GetLockKey(queryTsKey)
	assert.True(t, len(lockKey) > len(LockKeyPrefix))
	assert.Contains(t, lockKey, LockKeyPrefix)

	// 验证会话键和锁键不同
	assert.NotEqual(t, sessionKey, lockKey)
}

func TestScrollGenerateQueryTsKeyErrorHandling(t *testing.T) {
	// 测试无法序列化的对象
	invalidQueryTs := make(chan int) // channel无法被JSON序列化
	username := "test_user"

	key, err := ScrollGenerateQueryTsKey(invalidQueryTs, username)
	assert.Error(t, err)
	assert.Empty(t, key)
}
