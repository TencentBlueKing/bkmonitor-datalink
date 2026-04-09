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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	storageMap     = make(map[string]*Storage)
	storageMapHash string
	storageLock    = new(sync.RWMutex)
)

// StorageMapHash 返回最近一次 ReloadTsDBStorage 写入的配置哈希（与 Consul 侧 hash 比对用于短路 reload）
func StorageMapHash() string {
	storageLock.RLock()
	defer storageLock.RUnlock()
	return storageMapHash
}

// ReloadTsDBStorage 重新加载存储实例到内存里面
func ReloadTsDBStorage(ctx context.Context, hash string, tsDBs map[string]*consul.Storage, opt *Options) error {
	var err error
	ctx, span := trace.NewSpan(ctx, "reload-tsdb-storage")
	defer span.End(&err)

	newStorageMap := make(map[string]*Storage, len(tsDBs))
	oldHash := storageMapHash

	for storageID, tsDB := range tsDBs {
		storage := &Storage{
			Type:     tsDB.Type,
			Address:  tsDB.Address,
			Username: tsDB.Username,
			Password: tsDB.Password,
		}

		switch tsDB.Type {
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

	newKeys := make([]string, 0, len(newStorageMap))
	for k := range newStorageMap {
		newKeys = append(newKeys, k)
	}

	span.Set("old_hash", oldHash)
	span.Set("new_hash", hash)
	span.Set("storage_count", len(newStorageMap))
	span.Set("storage_keys", fmt.Sprintf("%v", newKeys))

	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap = newStorageMap
	storageMapHash = hash

	metadata.NewMessage("tsdb_storage", "reload storage: old_hash=%s new_hash=%s count=%d keys=%v",
		oldHash, hash, len(newStorageMap), newKeys).Info(ctx)

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
func GetStorage(ctx context.Context, storageID string) (*Storage, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "get-tsdb-storage")
	defer span.End(&err)

	storageLock.RLock()
	defer storageLock.RUnlock()

	keys := make([]string, 0, len(storageMap))
	for k := range storageMap {
		keys = append(keys, k)
	}

	span.Set("storage_id", storageID)
	span.Set("storage_hash", storageMapHash)
	span.Set("storage_keys", fmt.Sprintf("%v", keys))

	metadata.NewMessage("tsdb_storage", "get storage: id=%s hash=%s map_keys=%v",
		storageID, storageMapHash, keys).Info(ctx)

	storage, ok := storageMap[storageID]
	if !ok {
		err = fmt.Errorf("%s: storageID: %s", ErrStorageNotFound, storageID)
		return nil, err
	}
	return storage, nil
}

// SetStorage 写入实例到内存去
func SetStorage(storageID string, storage *Storage) {
	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap[storageID] = storage
}
