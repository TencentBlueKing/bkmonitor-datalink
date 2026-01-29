// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

var (
	storageMap  = make(map[string]*Storage)
	storageLock = new(sync.RWMutex)
)

// getStorageFields 从存储结构体中提取字段，支持 consul.Storage 和 redis.Storage
func getStorageFields(storage any) (storageType, address, username, password string) {
	switch s := storage.(type) {
	case *consul.Storage:
		return s.Type, s.Address, s.Username, s.Password
	case *redis.Storage:
		return s.Type, s.Address, s.Username, s.Password
	default:
		panic(fmt.Sprintf("unsupported storage type: %T", storage))
	}
}

// ReloadTsDBStorage 重新加载存储实例到内存里面
// 支持 consul.Storage 和 redis.Storage
func ReloadTsDBStorage(_ context.Context, tsDBs map[string]any, opt *Options) error {
	newStorageMap := make(map[string]*Storage, len(tsDBs))

	for storageID, tsDB := range tsDBs {
		storageType, address, username, password := getStorageFields(tsDB)
		var storage *Storage

		storage = &Storage{
			Type:     storageType,
			Address:  address,
			Username: username,
			Password: password,
		}

		switch storageType {
		case metadata.ElasticsearchStorageType:
			storage.Timeout = opt.Es.Timeout
			storage.MaxRouting = opt.Es.MaxRouting
			storage.MaxLimit = opt.Es.MaxSize
		case metadata.InfluxDBStorageType:
			storage.Timeout = opt.InfluxDB.Timeout
			storage.MaxLimit = opt.InfluxDB.MaxLimit
			storage.MaxSLimit = opt.InfluxDB.MaxSLimit
			storage.Toleration = opt.InfluxDB.Tolerance
			storage.ReadRateLimit = opt.InfluxDB.ReadRateLimit

			storage.ContentType = opt.InfluxDB.ContentType
			storage.ChunkSize = opt.InfluxDB.ChunkSize

			storage.UriPath = opt.InfluxDB.RawUriPath
			storage.Accept = opt.InfluxDB.Accept
			storage.AcceptEncoding = opt.InfluxDB.AcceptEncoding
		}
		newStorageMap[storageID] = storage

	}
	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap = newStorageMap
	return nil
}

func Print() string {
	storageLock.RLock()
	defer storageLock.RUnlock()
	str := "--------------------------- storage list --------------------------------------\n"
	for k, s := range storageMap {
		str += fmt.Sprintf("%s: %+v \n", k, s)
	}
	return str
}

// GetStorage 初始化全局 tsdb 标准实例
func GetStorage(storageID string) (*Storage, error) {
	storageLock.Lock()
	defer storageLock.Unlock()
	storage, ok := storageMap[storageID]
	if !ok {
		return nil, fmt.Errorf("%s: storageID: %s", ErrStorageNotFound, storageID)
	}
	return storage, nil
}

// SetStorage 写入实例到内存去
func SetStorage(storageID string, storage *Storage) {
	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap[storageID] = storage
}

// GetAllStorageFromMemory 从内存中获取所有存储配置
func GetAllStorageFromMemory() map[string]*Storage {
	storageLock.RLock()
	defer storageLock.RUnlock()

	result := make(map[string]*Storage, len(storageMap))
	for k, v := range storageMap {
		result[k] = v
	}
	return result
}

// GetTsDBStorageFromMemory 从内存中获取 TSDB 存储配置（过滤出有效的存储类型）
func GetTsDBStorageFromMemory() map[string]*Storage {
	storageLock.RLock()
	defer storageLock.RUnlock()

	typeList := []string{
		metadata.InfluxDBStorageType,
		metadata.ElasticsearchStorageType,
		metadata.BkSqlStorageType,
		metadata.VictoriaMetricsStorageType,
	}

	result := make(map[string]*Storage)
	for k, v := range storageMap {
		for _, t := range typeList {
			if v.Type == t {
				result[k] = v
				break
			}
		}
	}
	return result
}
