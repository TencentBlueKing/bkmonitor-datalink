// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestGetFeatureFlagsPath 测试获取特性开关路径
func TestGetFeatureFlagsPath(t *testing.T) {
	log.InitTestLogger()

	// 设置基础路径
	basePath = "bkmonitorv3/unify-query"
	dataPath = "data"

	path := GetFeatureFlagsPath()
	expected := "bkmonitorv3/unify-query/data/feature_flag"
	assert.Equal(t, expected, path)
}

// TestGetFeatureFlags 测试从 Consul 获取特性开关配置
func TestGetFeatureFlags(t *testing.T) {
	log.InitTestLogger()

	// 初始化 Consul 实例
	_ = SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	// 测试用例 1: 正常获取配置
	t.Run("正常获取配置", func(t *testing.T) {
		featureFlagConfig := `{
			"test-flag": {
				"variations": {
					"true": true,
					"false": false
				},
				"defaultRule": {
					"variation": "false"
				}
			}
		}`

		stubs := gostub.StubFunc(&GetKVData, []byte(featureFlagConfig), nil)
		defer stubs.Reset()

		data, err := GetFeatureFlags()
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, featureFlagConfig, string(data))
	})

	// 测试用例 2: 配置不存在
	t.Run("配置不存在", func(t *testing.T) {
		stubs := gostub.StubFunc(&GetKVData, nil, nil)
		defer stubs.Reset()

		data, err := GetFeatureFlags()
		assert.Nil(t, err)
		// GetKVData 返回 nil 时，data 是 []byte(nil)，len() 对于 nil slice 返回 0
		assert.Equal(t, 0, len(data), "data should be nil or empty")
	})

	// 测试用例 3: 获取配置失败
	t.Run("获取配置失败", func(t *testing.T) {
		stubs := gostub.StubFunc(&GetKVData, nil, errors.New("consul get error"))
		defer stubs.Reset()

		data, err := GetFeatureFlags()
		assert.NotNil(t, err)
		assert.Equal(t, "consul get error", err.Error())
		// GetKVData 返回错误时，data 是 nil，len() 对于 nil slice 返回 0
		assert.Equal(t, 0, len(data), "data should be nil or empty when error occurs")
	})

	// 测试用例 4: 空配置
	t.Run("空配置", func(t *testing.T) {
		stubs := gostub.StubFunc(&GetKVData, []byte("{}"), nil)
		defer stubs.Reset()

		data, err := GetFeatureFlags()
		assert.Nil(t, err)
		assert.NotNil(t, data)
		assert.Equal(t, "{}", string(data))
	})
}

// TestWatchFeatureFlags 测试监听特性开关变更
func TestWatchFeatureFlags(t *testing.T) {
	log.InitTestLogger()

	// 初始化 Consul 实例
	_ = SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 测试用例 1: 正常监听
	t.Run("正常监听", func(t *testing.T) {
		// 创建一个测试 channel
		testChan := make(chan any, 1)
		testChan <- "test notification"

		stubs := gostub.StubFunc(&WatchChange, testChan, nil)
		defer stubs.Reset()

		ch, err := WatchFeatureFlags(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 验证能接收到消息
		select {
		case msg := <-ch:
			assert.Equal(t, "test notification", msg)
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for message")
		}
	})

	// 测试用例 2: 监听失败
	t.Run("监听失败", func(t *testing.T) {
		stubs := gostub.StubFunc(&WatchChange, nil, errors.New("watch error"))
		defer stubs.Reset()

		ch, err := WatchFeatureFlags(ctx)
		assert.NotNil(t, err)
		assert.Equal(t, "watch error", err.Error())
		// channel 为 nil 时，使用 == nil 检查
		assert.True(t, ch == nil, "channel should be nil when error occurs")
	})

	// 测试用例 3: 上下文取消
	t.Run("上下文取消", func(t *testing.T) {
		cancelCtx, cancelFunc := context.WithCancel(context.Background())

		// 创建一个不会立即关闭的 channel
		testChan := make(chan any)

		stubs := gostub.StubFunc(&WatchChange, testChan, nil)
		defer stubs.Reset()

		ch, err := WatchFeatureFlags(cancelCtx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 取消上下文
		cancelFunc()

		// 等待一下确保 goroutine 处理了取消
		time.Sleep(100 * time.Millisecond)
	})
}

// TestFeatureFlagsIntegration 集成测试：完整的特性开关流程
func TestFeatureFlagsIntegration(t *testing.T) {
	log.InitTestLogger()

	// 初始化 Consul 实例
	_ = SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 测试配置
	featureFlagConfig := `{
		"enable-new-feature": {
			"variations": {
				"true": true,
				"false": false
			},
			"defaultRule": {
				"variation": "false"
			},
			"rules": [
				{
					"name": "enable for user 123",
					"variation": "true",
					"query": "user_id == \"123\""
				}
			]
		},
		"feature-version": {
			"variations": {
				"A": "version-a",
				"B": "version-b"
			},
			"defaultRule": {
				"variation": "A"
			}
		}
	}`

	// Mock GetKVData 返回配置
	stubs := gostub.StubFunc(&GetKVData, []byte(featureFlagConfig), nil)
	defer stubs.Reset()

	// 测试获取配置
	data, err := GetFeatureFlags()
	assert.Nil(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, featureFlagConfig, string(data))

	// 测试路径
	path := GetFeatureFlagsPath()
	assert.Equal(t, "bkmonitorv3/unify-query/data/feature_flag", path)

	// 测试监听（使用一个简单的 channel）
	watchChan := make(chan any, 1)
	watchStubs := gostub.StubFunc(&WatchChange, watchChan, nil)
	defer watchStubs.Reset()

	ch, err := WatchFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, ch)

	// 模拟配置变更通知
	go func() {
		time.Sleep(50 * time.Millisecond)
		watchChan <- "config changed"
	}()

	// 验证能接收到变更通知
	select {
	case msg := <-ch:
		assert.Equal(t, "config changed", msg)
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for watch notification")
	}
}

// TestGetFeatureFlagsWithMultipleFlags 测试多个特性开关配置
func TestGetFeatureFlagsWithMultipleFlags(t *testing.T) {
	log.InitTestLogger()

	_ = SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	complexConfig := `{
		"flag-1": {
			"variations": {
				"true": true,
				"false": false
			},
			"defaultRule": {
				"variation": "false"
			}
		},
		"flag-2": {
			"variations": {
				"A": "value-a",
				"B": "value-b",
				"C": "value-c"
			},
			"defaultRule": {
				"variation": "A"
			},
			"rules": [
				{
					"name": "rule for space",
					"variation": "B",
					"query": "spaceUid == \"bkcc__2\""
				}
			]
		},
		"flag-3": {
			"variations": {
				"0": 0,
				"10": 10,
				"100": 100
			},
			"defaultRule": {
				"variation": "0"
			}
		}
	}`

	stubs := gostub.StubFunc(&GetKVData, []byte(complexConfig), nil)
	defer stubs.Reset()

	data, err := GetFeatureFlags()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// 验证配置包含所有特性开关
	configStr := string(data)
	assert.Contains(t, configStr, "flag-1")
	assert.Contains(t, configStr, "flag-2")
	assert.Contains(t, configStr, "flag-3")
}

// TestGetFeatureFlagsPathWithCustomBasePath 测试自定义基础路径
func TestGetFeatureFlagsPathWithCustomBasePath(t *testing.T) {
	log.InitTestLogger()

	// 保存原始值
	originalBasePath := basePath
	originalDataPath := dataPath

	// 设置自定义路径
	basePath = "custom/base/path"
	dataPath = "custom_data"

	path := GetFeatureFlagsPath()
	expected := "custom/base/path/custom_data/feature_flag"
	assert.Equal(t, expected, path)

	// 恢复原始值
	basePath = originalBasePath
	dataPath = originalDataPath
}

// TestWatchFeatureFlagsMultipleNotifications 测试多次通知
func TestWatchFeatureFlagsMultipleNotifications(t *testing.T) {
	log.InitTestLogger()

	_ = SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchChan := make(chan any, 3)
	watchChan <- "notification-1"
	watchChan <- "notification-2"
	watchChan <- "notification-3"

	stubs := gostub.StubFunc(&WatchChange, watchChan, nil)
	defer stubs.Reset()

	ch, err := WatchFeatureFlags(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, ch)

	// 验证能接收到多次通知
	expectedNotifications := []string{"notification-1", "notification-2", "notification-3"}
	for i, expected := range expectedNotifications {
		select {
		case msg := <-ch:
			assert.Equal(t, expected, msg)
		case <-time.After(1 * time.Second):
			t.Errorf("timeout waiting for notification %d", i+1)
		}
	}
}
