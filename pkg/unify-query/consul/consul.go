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
	"sync"

	"github.com/hashicorp/consul/api"
)

var (
	basePath    = "bkmonitorv3/unify-query"
	dataPath    = "data"
	versionPath = "version"
)

var globalInstance *Instance

var lock *sync.RWMutex

// init 初始化读写锁
func init() {
	lock = new(sync.RWMutex)
}

// Wait 等待实例释放
func Wait() {
	if globalInstance != nil {
		globalInstance.Wait()
	}
}

// SetInstance 创建 consul 实例
func SetInstance(ctx context.Context, kvBasePath, serviceName, consulAddress string,
	tags []string, address string, port int, ttl string, caFile, keyFile, certFile string,
) error {
	lock.Lock()
	defer lock.Unlock()
	var err error
	if kvBasePath != "" {
		basePath = kvBasePath
	}

	globalInstance, err = NewConsulInstance(
		ctx, serviceName, consulAddress, tags, address, port, ttl, caFile, keyFile, certFile,
	)
	if err != nil {
		return err
	}
	return nil
}

// LoopAwakeService 注册服务，并循环激活
func LoopAwakeService() error {
	lock.RLock()
	defer lock.RUnlock()
	return globalInstance.LoopAwakeService()
}

// StopAwakeService 停止服务的激活动作,并注销服务
func StopAwakeService() error {
	lock.RLock()
	defer lock.RUnlock()
	return globalInstance.StopAwakeService()
}

// WatchChange 监听指定地址，监听触发时，channel将会传出信息
var WatchChange = func(ctx context.Context, watchPath string) (<-chan any, error) {
	kvChan, err := globalInstance.Watch(watchPath)
	if err != nil {
		return nil, err
	}
	return kvChan, nil
}

// WatchChangeOnce 监听指定地址，监听触发时，channel将会传出信息
var WatchChangeOnce = func(ctx context.Context, watchPath, separator string) (<-chan any, error) {
	kvChan, err := globalInstance.WatchOnce(watchPath, separator)
	if err != nil {
		return nil, err
	}
	return kvChan, nil
}

// GetDataWithPrefix 通过前缀获取 kv 列表
var GetDataWithPrefix = func(prefix string) (api.KVPairs, error) {
	return globalInstance.GetDataWithPrefix(prefix)
}

// GetKVData 通过 path 路径获取 value
var GetKVData = func(path string) ([]byte, error) {
	res, err := globalInstance.GetData(path)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return res.Value, nil
	}
	return nil, nil
}
