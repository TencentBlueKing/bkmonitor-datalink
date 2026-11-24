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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul/base"
	"github.com/hashicorp/consul/api"
)

var (
	basePath    = "bkmonitorv3/unify-query"
	dataPath    = "data"
	versionPath = "version"
)

var globalInstance *Instance

// addressErr 记录最近一次 SetInstance 因地址没有创建 client 的原因。
var addressErr *AddressError

var lock *sync.RWMutex

var errUnavailable = errors.New("consul is not initialized")

func currentInstance() (*Instance, error) {
	lock.RLock()
	defer lock.RUnlock()
	if globalInstance == nil {
		if addressErr != nil {
			return nil, addressErr
		}
		return nil, errUnavailable
	}
	return globalInstance, nil
}

// AddressUnavailable 返回最近一次 SetInstance 因地址没有创建 client 的原因；已创建 client 或尚未初始化时为 nil。
func AddressUnavailable() *AddressError {
	lock.RLock()
	defer lock.RUnlock()
	return addressErr
}

// init 初始化读写锁
func init() {
	lock = new(sync.RWMutex)
}

// Close releases the KV client and stops every watch before reload.
func Close() {
	lock.Lock()
	instance := globalInstance
	globalInstance = nil
	addressErr = nil
	lock.Unlock()
	if instance != nil {
		instance.cancel()
		_ = instance.client.Close()
	}
}

// SetInstance creates a KV client without contacting Consul. An empty address or one without a host
// creates no client; every call then returns the AddressError.
func SetInstance(ctx context.Context, kvBasePath, consulAddress, caFile, keyFile, certFile string) error {
	return SetInstanceWithAuth(ctx, kvBasePath, consulAddress, caFile, keyFile, certFile, nil)
}

func SetInstanceWithAuth(ctx context.Context, kvBasePath, consulAddress, caFile, keyFile, certFile string, auth *base.AuthConfig) error {
	lock.Lock()
	defer lock.Unlock()
	var err error
	if kvBasePath != "" {
		basePath = kvBasePath
	}
	if addressErr = checkAddress(consulAddress); addressErr != nil {
		globalInstance = nil
		return addressErr
	}
	globalInstance, err = NewConsulInstanceWithAuth(ctx, consulAddress, caFile, keyFile, certFile, auth)
	return err
}

// WatchChange 监听指定地址，监听触发时，channel将会传出信息
var WatchChange = func(ctx context.Context, watchPath string) (<-chan any, error) {
	instance, err := currentInstance()
	if err != nil {
		return nil, err
	}
	return instance.Watch(watchPath)
}

// GetDataWithPrefix 通过前缀获取 kv 列表
var GetDataWithPrefix = func(prefix string) (api.KVPairs, error) {
	instance, err := currentInstance()
	if err != nil {
		return nil, err
	}
	return instance.GetDataWithPrefix(prefix)
}

// GetDataWithPrefixContext 通过前缀获取 KV 列表，并传递取消信号。
var GetDataWithPrefixContext = func(ctx context.Context, prefix string) (api.KVPairs, error) {
	instance, err := currentInstance()
	if err != nil {
		return nil, err
	}
	return instance.GetDataWithPrefixContext(ctx, prefix)
}

// GetKVData 通过 path 路径获取 value
var GetKVData = func(path string) ([]byte, error) {
	instance, err := currentInstance()
	if err != nil {
		return nil, err
	}
	res, err := instance.GetData(path)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return res.Value, nil
	}
	return nil, nil
}

// GetKVDataContext 读取 KV，并将取消信号传递到底层 HTTP 请求。
var GetKVDataContext = func(ctx context.Context, path string) ([]byte, error) {
	instance, err := currentInstance()
	if err != nil {
		return nil, err
	}
	res, err := instance.GetDataContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return res.Value, nil
	}
	return nil, nil
}
