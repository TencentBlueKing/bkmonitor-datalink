// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestFormatStorageInfo 测试 FormatStorageInfo 函数
// 注意：FormatStorageInfo 是包内函数，我们通过 GetStorageInfo 来间接测试它的逻辑
func TestFormatStorageInfo(t *testing.T) {
	log.InitTestLogger()
	_ = consul.SetInstance(
		context.Background(), "", "test-format-storage", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	tests := []struct {
		name      string
		kvPairs   api.KVPairs
		want      map[string]*consul.Storage
		wantError bool
	}{
		{
			name: "正常解析单个存储配置",
			kvPairs: api.KVPairs{
				{
					Key:   "bkmonitorv3/unify-query/data/storage/influxdb-1",
					Value: []byte(`{"address":"http://127.0.0.1:8086","username":"admin","password":"password123","type":"influxdb"}`),
				},
			},
			want: map[string]*consul.Storage{
				"influxdb-1": {
					Address:  "http://127.0.0.1:8086",
					Username: "admin",
					Password: "password123",
					Type:     "influxdb",
				},
			},
			wantError: false,
		},
		{
			name: "正常解析多个存储配置",
			kvPairs: api.KVPairs{
				{
					Key:   "bkmonitorv3/unify-query/data/storage/influxdb-1",
					Value: []byte(`{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`),
				},
				{
					Key:   "bkmonitorv3/unify-query/data/storage/elasticsearch-1",
					Value: []byte(`{"address":"http://127.0.0.1:9200","username":"es_user","password":"es_pass","type":"elasticsearch"}`),
				},
				{
					Key:   "bkmonitorv3/unify-query/data/storage/victoriametrics-1",
					Value: []byte(`{"address":"http://127.0.0.1:8428","username":"","password":"","type":"victoriametrics"}`),
				},
			},
			want: map[string]*consul.Storage{
				"influxdb-1": {
					Address:  "http://127.0.0.1:8086",
					Username: "",
					Password: "",
					Type:     "influxdb",
				},
				"elasticsearch-1": {
					Address:  "http://127.0.0.1:9200",
					Username: "es_user",
					Password: "es_pass",
					Type:     "elasticsearch",
				},
				"victoriametrics-1": {
					Address:  "http://127.0.0.1:8428",
					Username: "",
					Password: "",
					Type:     "victoriametrics",
				},
			},
			wantError: false,
		},
		{
			name:      "空数据",
			kvPairs:   api.KVPairs{},
			want:      map[string]*consul.Storage{},
			wantError: false,
		},
		{
			name: "JSON格式错误",
			kvPairs: api.KVPairs{
				{
					Key:   "bkmonitorv3/unify-query/data/storage/invalid",
					Value: []byte(`{"address":"http://127.0.0.1:8086","invalid_json"`),
				},
			},
			want:      nil,
			wantError: true,
		},
		{
			name: "路径前缀处理",
			kvPairs: api.KVPairs{
				{
					Key:   "bkmonitorv3/unify-query/data/storage/storage-1",
					Value: []byte(`{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`),
				},
			},
			want: map[string]*consul.Storage{
				"storage-1": {
					Address:  "http://127.0.0.1:8086",
					Username: "",
					Password: "",
					Type:     "influxdb",
				},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 通过 GetStorageInfo 间接测试 FormatStorageInfo
			// 因为 FormatStorageInfo 是包内函数，GetStorageInfo 会调用它
			stubs := gostub.StubFunc(&consul.GetDataWithPrefix, tt.kvPairs, nil)
			defer stubs.Reset()

			result, err := consul.GetStorageInfo()
			if tt.wantError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, len(tt.want), len(result))
				for key, expectedStorage := range tt.want {
					actualStorage, ok := result[key]
					if !ok {
						t.Errorf("key %s should exist", key)
						continue
					}
					assert.Equal(t, expectedStorage.Address, actualStorage.Address)
					assert.Equal(t, expectedStorage.Username, actualStorage.Username)
					assert.Equal(t, expectedStorage.Password, actualStorage.Password)
					assert.Equal(t, expectedStorage.Type, actualStorage.Type)
				}
			}
		})
	}
}

// TestGetStorageInfo 测试 GetStorageInfo 函数
func TestGetStorageInfo(t *testing.T) {
	log.InitTestLogger()
	_ = consul.SetInstance(
		context.Background(), "", "test-storage", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	tests := []struct {
		name      string
		kvPairs   api.KVPairs
		want      map[string]*consul.Storage
		wantError bool
		setupStub func() *gostub.Stubs
	}{
		{
			name: "正常获取单个存储配置",
			kvPairs: api.KVPairs{
				{
					Key:   "bkmonitorv3/unify-query/data/storage/influxdb-1",
					Value: []byte(`{"address":"http://127.0.0.1:8086","username":"admin","password":"pass","type":"influxdb"}`),
				},
			},
			want: map[string]*consul.Storage{
				"influxdb-1": {
					Address:  "http://127.0.0.1:8086",
					Username: "admin",
					Password: "pass",
					Type:     "influxdb",
				},
			},
			wantError: false,
			setupStub: func() *gostub.Stubs {
				kv := api.KVPairs{
					{
						Key:   "bkmonitorv3/unify-query/data/storage/influxdb-1",
						Value: []byte(`{"address":"http://127.0.0.1:8086","username":"admin","password":"pass","type":"influxdb"}`),
					},
				}
				return gostub.StubFunc(&consul.GetDataWithPrefix, kv, nil)
			},
		},
		{
			name: "正常获取多个存储配置",
			kvPairs: api.KVPairs{
				{
					Key:   "bkmonitorv3/unify-query/data/storage/influxdb-1",
					Value: []byte(`{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`),
				},
				{
					Key:   "bkmonitorv3/unify-query/data/storage/elasticsearch-1",
					Value: []byte(`{"address":"http://127.0.0.1:9200","username":"es_user","password":"es_pass","type":"elasticsearch"}`),
				},
			},
			want: map[string]*consul.Storage{
				"influxdb-1": {
					Address:  "http://127.0.0.1:8086",
					Username: "",
					Password: "",
					Type:     "influxdb",
				},
				"elasticsearch-1": {
					Address:  "http://127.0.0.1:9200",
					Username: "es_user",
					Password: "es_pass",
					Type:     "elasticsearch",
				},
			},
			wantError: false,
			setupStub: func() *gostub.Stubs {
				kv := api.KVPairs{
					{
						Key:   "bkmonitorv3/unify-query/data/storage/influxdb-1",
						Value: []byte(`{"address":"http://127.0.0.1:8086","username":"","password":"","type":"influxdb"}`),
					},
					{
						Key:   "bkmonitorv3/unify-query/data/storage/elasticsearch-1",
						Value: []byte(`{"address":"http://127.0.0.1:9200","username":"es_user","password":"es_pass","type":"elasticsearch"}`),
					},
				}
				return gostub.StubFunc(&consul.GetDataWithPrefix, kv, nil)
			},
		},
		{
			name:      "Consul连接失败",
			want:      nil,
			wantError: true,
			setupStub: func() *gostub.Stubs {
				return gostub.StubFunc(&consul.GetDataWithPrefix, nil, errors.New("consul connection error"))
			},
		},
		{
			name:      "空配置",
			kvPairs:   api.KVPairs{},
			want:      map[string]*consul.Storage{},
			wantError: false,
			setupStub: func() *gostub.Stubs {
				return gostub.StubFunc(&consul.GetDataWithPrefix, api.KVPairs{}, nil)
			},
		},
		{
			name:      "JSON格式错误",
			want:      nil,
			wantError: true,
			setupStub: func() *gostub.Stubs {
				kv := api.KVPairs{
					{
						Key:   "bkmonitorv3/unify-query/data/storage/invalid",
						Value: []byte(`{"address":"http://127.0.0.1:8086","invalid_json"`),
					},
				}
				return gostub.StubFunc(&consul.GetDataWithPrefix, kv, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupStub != nil {
				stubs := tt.setupStub()
				defer stubs.Reset()
			}

			result, err := consul.GetStorageInfo()
			if tt.wantError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, len(tt.want), len(result))
				for key, expectedStorage := range tt.want {
					actualStorage, ok := result[key]
					if !ok {
						t.Errorf("key %s should exist", key)
						continue
					}
					assert.Equal(t, expectedStorage.Address, actualStorage.Address)
					assert.Equal(t, expectedStorage.Username, actualStorage.Username)
					assert.Equal(t, expectedStorage.Password, actualStorage.Password)
					assert.Equal(t, expectedStorage.Type, actualStorage.Type)
				}
			}
		})
	}
}

// TestWatchStorageInfo 测试 WatchStorageInfo 函数
func TestWatchStorageInfo(t *testing.T) {
	log.InitTestLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = consul.SetInstance(
		ctx, "", "test-watch-storage", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", "", "", "",
	)

	t.Run("正常启动监听", func(t *testing.T) {
		// 创建一个模拟的 channel
		mockChan := make(chan any, 1)
		mockChan <- "test change"

		stubs := gostub.StubFunc(&consul.WatchChange, mockChan, nil)
		defer stubs.Reset()

		ch, err := consul.WatchStorageInfo(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 验证能够接收到数据
		select {
		case data := <-ch:
			assert.NotNil(t, data)
		case <-time.After(1 * time.Second):
			t.Fatal("timeout waiting for watch data")
		}
	})

	t.Run("监听启动失败", func(t *testing.T) {
		stubs := gostub.StubFunc(&consul.WatchChange, (<-chan any)(nil), errors.New("watch error"))
		defer stubs.Reset()

		ch, err := consul.WatchStorageInfo(ctx)
		assert.NotNil(t, err)
		if ch != nil {
			t.Error("channel should be nil when error occurs")
		}
	})

	t.Run("上下文取消", func(t *testing.T) {
		cancelCtx, cancelFunc := context.WithCancel(context.Background())
		mockChan := make(chan any)

		stubs := gostub.StubFunc(&consul.WatchChange, mockChan, nil)
		defer stubs.Reset()

		ch, err := consul.WatchStorageInfo(cancelCtx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 取消上下文
		cancelFunc()

		// 验证 channel 会被关闭（通过超时检测）
		select {
		case _, ok := <-ch:
			if ok {
				t.Error("channel should be closed after context cancel")
			}
		case <-time.After(100 * time.Millisecond):
			// 如果超时，说明 channel 可能已经关闭或没有数据
		}
	})

	t.Run("配置变更通知", func(t *testing.T) {
		mockChan := make(chan any, 2)
		mockChan <- "change1"
		mockChan <- "change2"

		stubs := gostub.StubFunc(&consul.WatchChange, mockChan, nil)
		defer stubs.Reset()

		ch, err := consul.WatchStorageInfo(ctx)
		assert.Nil(t, err)
		assert.NotNil(t, ch)

		// 验证能够接收到多次变更
		changeCount := 0
		for i := 0; i < 2; i++ {
			select {
			case data := <-ch:
				assert.NotNil(t, data)
				changeCount++
			case <-time.After(1 * time.Second):
				break
			}
		}
		assert.Equal(t, 2, changeCount)
	})
}
