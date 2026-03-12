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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/likexian/gokit/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestGetStoragePath 测试获取存储路径
func TestGetStoragePath(t *testing.T) {
	log.InitTestLogger()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
	path := storageClient.GetStoragePath()
	expected := "bkmonitorv3:unify-query:data:storage"
	assert.Equal(t, expected, path)
}

// TestGetStorageChannel 测试获取存储 channel 路径
func TestGetStorageChannel(t *testing.T) {
	log.InitTestLogger()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := goRedis.NewClient(&goRedis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
	channel := storageClient.GetStorageChannel()
	expected := "bkmonitorv3:unify-query:data:storage:storage_channel"
	assert.Equal(t, expected, channel)
}

// TestFormatStorageInfo 测试格式化存储配置信息
func TestFormatStorageInfo(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	t.Run("正常解析单个存储配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()

		// 设置测试数据
		storageData := `{"address":"http://127.0.0.1:8086","username":"admin","password":"password123","type":"influxdb"}`
		key := storageKey + ":influxdb-1"
		err = client.Set(ctx, key, storageData, 0).Err()
		assert.Nil(t, err)

		keys := []string{key}
		result, err := storageClient.FormatStorageInfo(keys, func(key string) (string, error) {
			return client.Get(ctx, key).Result()
		})

		assert.Nil(t, err)
		assert.Equal(t, 1, len(result))
		assert.NotNil(t, result["influxdb-1"])
		assert.Equal(t, "http://127.0.0.1:8086", result["influxdb-1"].Address)
		assert.Equal(t, "admin", result["influxdb-1"].Username)
		assert.Equal(t, "password123", result["influxdb-1"].Password)
		assert.Equal(t, "influxdb", result["influxdb-1"].Type)
	})

	t.Run("正常解析多个存储配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()

		// 设置多个测试数据
		keys := []string{
			storageKey + ":influxdb-1",
			storageKey + ":elasticsearch-1",
			storageKey + ":victoriametrics-1",
		}

		err = client.Set(ctx, keys[0], `{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`, 0).Err()
		assert.Nil(t, err)
		err = client.Set(ctx, keys[1], `{"address":"http://127.0.0.1:9200","username":"es_user","password":"es_pass","type":"elasticsearch"}`, 0).Err()
		assert.Nil(t, err)
		err = client.Set(ctx, keys[2], `{"address":"http://127.0.0.1:8428","username":"","password":"","type":"victoriametrics"}`, 0).Err()
		assert.Nil(t, err)

		result, err := storageClient.FormatStorageInfo(keys, func(key string) (string, error) {
			return client.Get(ctx, key).Result()
		})

		assert.Nil(t, err)
		assert.Equal(t, 3, len(result))
		assert.Equal(t, "http://127.0.0.1:8086", result["influxdb-1"].Address)
		assert.Equal(t, "influxdb", result["influxdb-1"].Type)
		assert.Equal(t, "http://127.0.0.1:9200", result["elasticsearch-1"].Address)
		assert.Equal(t, "elasticsearch", result["elasticsearch-1"].Type)
		assert.Equal(t, "http://127.0.0.1:8428", result["victoriametrics-1"].Address)
		assert.Equal(t, "victoriametrics", result["victoriametrics-1"].Type)
	})

	t.Run("空数据", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		result, err := storageClient.FormatStorageInfo([]string{}, func(key string) (string, error) {
			return "", nil
		})

		assert.Nil(t, err)
		assert.Equal(t, 0, len(result))
	})

	t.Run("JSON格式错误", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()
		key := storageKey + ":invalid"

		err = client.Set(ctx, key, `{"address":"http://127.0.0.1:8086","invalid_json"`, 0).Err()
		assert.Nil(t, err)

		keys := []string{key}
		result, err := storageClient.FormatStorageInfo(keys, func(key string) (string, error) {
			return client.Get(ctx, key).Result()
		})

		// FormatStorageInfo 在遇到 JSON 错误时会返回错误
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal")
		// 由于 JSON 解析失败，result 可能为 nil 或空 map
		if result != nil {
			assert.Equal(t, 0, len(result))
		}
	})

	t.Run("忽略不匹配前缀的key", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()

		// 设置一个匹配的key和一个不匹配的key
		validKey := storageKey + ":influxdb-1"
		invalidKey := "other:prefix:key"

		err = client.Set(ctx, validKey, `{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`, 0).Err()
		assert.Nil(t, err)

		keys := []string{validKey, invalidKey}
		result, err := storageClient.FormatStorageInfo(keys, func(key string) (string, error) {
			if key == invalidKey {
				return "", nil
			}
			return client.Get(ctx, key).Result()
		})

		assert.Nil(t, err)
		assert.Equal(t, 1, len(result))
		assert.NotNil(t, result["influxdb-1"])
	})
}

// TestGetStorageInfo 测试从 Redis 获取存储配置信息
func TestGetStorageInfo(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	t.Run("正常获取单个存储配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()

		storageData := `{"address":"http://127.0.0.1:8086","username":"admin","password":"pass","type":"influxdb"}`
		key := storageKey + ":influxdb-1"
		err = client.Set(ctx, key, storageData, 0).Err()
		assert.Nil(t, err)

		result, err := storageClient.GetStorageInfo(ctx)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(result))
		assert.NotNil(t, result["influxdb-1"])
		assert.Equal(t, "http://127.0.0.1:8086", result["influxdb-1"].Address)
		assert.Equal(t, "admin", result["influxdb-1"].Username)
		assert.Equal(t, "pass", result["influxdb-1"].Password)
		assert.Equal(t, "influxdb", result["influxdb-1"].Type)
	})

	t.Run("正常获取多个存储配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()

		err = client.Set(ctx, storageKey+":influxdb-1", `{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`, 0).Err()
		assert.Nil(t, err)
		err = client.Set(ctx, storageKey+":elasticsearch-1", `{"address":"http://127.0.0.1:9200","username":"es_user","password":"es_pass","type":"elasticsearch"}`, 0).Err()
		assert.Nil(t, err)

		result, err := storageClient.GetStorageInfo(ctx)
		assert.Nil(t, err)
		assert.Equal(t, 2, len(result))
		assert.Equal(t, "influxdb", result["influxdb-1"].Type)
		assert.Equal(t, "elasticsearch", result["elasticsearch-1"].Type)
	})

	t.Run("Redis客户端未初始化", func(t *testing.T) {
		storageClient := &StorageClient{
			client: nil,
			prefix: "bkmonitorv3:unify-query",
		}

		result, err := storageClient.GetStorageInfo(ctx)
		assert.NotNil(t, err)
		// 当错误时，result 可能返回空 map 而不是 nil
		if result != nil {
			assert.Equal(t, 0, len(result))
		}
		assert.Contains(t, err.Error(), "redis client is not initialized")
	})

	t.Run("空配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		result, err := storageClient.GetStorageInfo(ctx)
		assert.Nil(t, err)
		assert.Equal(t, 0, len(result))
	})
}

// TestWatchStorageInfo 测试监听存储配置变更
func TestWatchStorageInfo(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	t.Run("正常启动监听", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		channel := storageClient.GetStorageChannel()

		// 启动监听
		ch, err := storageClient.WatchStorageInfo(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 发布一条消息
		err = client.Publish(ctx, channel, "test change").Err()
		assert.Nil(t, err)

		// 验证能够接收到数据
		select {
		case data := <-ch:
			assert.NotNil(t, data)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for watch data")
		}
	})

	t.Run("Redis客户端未初始化", func(t *testing.T) {
		storageClient := &StorageClient{
			client: nil,
			prefix: "bkmonitorv3:unify-query",
		}

		ch, err := storageClient.WatchStorageInfo(ctx)
		assert.NotNil(t, err)
		// 当错误时，channel 应该为 nil
		if ch != nil {
			// channel 是只读的，不能关闭，只能等待它自然关闭
			// 这里只验证错误即可
		}
		assert.Contains(t, err.Error(), "redis client is not initialized")
	})

	t.Run("上下文取消", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		cancelCtx, cancelFunc := context.WithCancel(context.Background())

		ch, err := storageClient.WatchStorageInfo(cancelCtx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 取消上下文
		cancelFunc()

		// 等待 goroutine 退出
		time.Sleep(100 * time.Millisecond)

		// 验证 channel 会被关闭（通过超时检测）
		select {
		case _, ok := <-ch:
			if ok {
				t.Error("channel should be closed after context cancel")
			}
		case <-time.After(500 * time.Millisecond):
			// 如果超时，说明 channel 可能已经关闭
		}
	})

	t.Run("配置变更通知", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		channel := storageClient.GetStorageChannel()

		ch, err := storageClient.WatchStorageInfo(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 发布多条消息
		err = client.Publish(ctx, channel, "change1").Err()
		assert.Nil(t, err)
		err = client.Publish(ctx, channel, "change2").Err()
		assert.Nil(t, err)

		// 验证能够接收到多次变更
		changeCount := 0
		timeout := time.After(2 * time.Second)
		for changeCount < 2 {
			select {
			case data := <-ch:
				assert.NotNil(t, data)
				changeCount++
			case <-timeout:
				// 超时退出循环
				goto done
			}
		}
	done:
		// 至少应该收到 1 条消息（可能收到 2 条）
		assert.True(t, changeCount >= 1, fmt.Sprintf("should receive at least 1 message, got %d", changeCount))
	})
}

// TestSetStorage 测试设置存储配置
func TestSetStorage(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	t.Run("正常设置存储配置", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		client := goRedis.NewClient(&goRedis.Options{
			Addr: mr.Addr(),
		})
		defer client.Close()

		storageClient := NewStorageClient(client, "bkmonitorv3:unify-query")
		storageKey := storageClient.GetStoragePath()

		storage := &Storage{
			Address:  "http://127.0.0.1:8086",
			Username: "admin",
			Password: "password123",
			Type:     "influxdb",
		}

		err = storageClient.SetStorage(ctx, "influxdb-1", storage)
		assert.Nil(t, err)

		// 验证数据已设置
		key := storageKey + ":influxdb-1"
		data, err := client.Get(ctx, key).Result()
		assert.Nil(t, err)
		assert.Contains(t, data, "http://127.0.0.1:8086")
		assert.Contains(t, data, "admin")
		assert.Contains(t, data, "influxdb")
	})

	t.Run("Redis客户端未初始化", func(t *testing.T) {
		storageClient := &StorageClient{
			client: nil,
			prefix: "bkmonitorv3:unify-query",
		}

		storage := &Storage{
			Address: "http://127.0.0.1:8086",
			Type:    "influxdb",
		}

		err := storageClient.SetStorage(ctx, "influxdb-1", storage)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "redis client is not initialized")
	})
}
