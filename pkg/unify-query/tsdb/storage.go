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
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	storageMap     = make(map[string]*Storage)
	storageMapHash string
	storageLock    = new(sync.RWMutex)

	// getStorageKeysLogPending：本次 Reload 之后是否还需要打「首次命中」的全量 keys 诊断日志。
	// Reload 成功替换 storageMap 后置为 true；任意一次成功 GetStorage 通过 CAS 消费掉该标记，或 miss 时直接置 false。
	getStorageKeysLogPending atomic.Bool
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
	// 与 storageMapKeysLocked 一致：按 storage ID 数值升序，便于日志对照。
	sortStorageIDKeysAsc(newKeys)

	span.Set("old_hash", oldHash)
	span.Set("new_hash", hash)
	span.Set("storage_count", len(newStorageMap))
	span.Set("storage_keys", fmt.Sprintf("%v", newKeys))

	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap = newStorageMap
	storageMapHash = hash
	// 新 map 生效后允许再一次「首次成功 GetStorage」附带全量 map_keys，便于对照重载后的内存视图。
	getStorageKeysLogPending.Store(true)

	// oldHash 为空表示进程内首次写入，会打初始化日志；否则视为配置变更后的重载。
	if oldHash == "" {
		log.Infof(ctx, "tsdb storage map initialized: hash=%s count=%d keys=%v", hash, len(newStorageMap), newKeys)
		metadata.NewMessage("tsdb_storage", "init storage map: hash=%s count=%d keys=%v",
			hash, len(newStorageMap), newKeys).Info(ctx)
	} else {
		metadata.NewMessage("tsdb_storage", "reload storage: old_hash=%s new_hash=%s count=%d keys=%v",
			oldHash, hash, len(newStorageMap), newKeys).Info(ctx)
	}

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

// 仅在 miss 时创建 get-tsdb-storage-miss span 便于链路关联。
func GetStorage(ctx context.Context, storageID string) (*Storage, error) {
	storageLock.RLock()
	storage, ok := storageMap[storageID]
	if !ok {
		keys := storageMapKeysLocked()
		hash := storageMapHash
		getStorageKeysLogPending.Store(false)
		storageLock.RUnlock()

		var err error
		_, span := trace.NewSpan(ctx, "get-tsdb-storage-miss")
		span.Set("storage_id", storageID)
		span.Set("storage_hash", hash)
		span.Set("storage_keys", fmt.Sprintf("%v", keys))
		metadata.NewMessage("tsdb_storage", "get storage miss: id=%s hash=%s map_keys=%v",
			storageID, hash, keys).Info(ctx)
		err = fmt.Errorf("%s: storageID: %s", ErrStorageNotFound, storageID)
		span.End(&err)
		return nil, err
	}

	// 仅当 pending 仍为 true 时进入：原子地从 true 翻成 false，保证多 goroutine 下只有一次会打「重载后首次命中」全量 keys。
	if getStorageKeysLogPending.CompareAndSwap(true, false) {
		keys := storageMapKeysLocked()
		hash := storageMapHash
		storageLock.RUnlock()
		metadata.NewMessage("tsdb_storage", "get storage: id=%s hash=%s map_keys=%v",
			storageID, hash, keys).Info(ctx)
		return storage, nil
	}

	storageLock.RUnlock()
	return storage, nil
}

// SetStorage 写入实例到内存去
func SetStorage(storageID string, storage *Storage) {
	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap[storageID] = storage
}

// storageMapKeysLocked 复制当前 storageMap 的全部 key，调用方须已持有 storageLock 读锁或写锁。
func storageMapKeysLocked() []string {
	keys := make([]string, 0, len(storageMap))
	for k := range storageMap {
		keys = append(keys, k)
	}
	sortStorageIDKeysAsc(keys)
	return keys
}

// sortStorageIDKeysAsc 按 storage ID 的数值升序排列（ID 为十进制整数字符串时）。
// 任一方无法解析为整数时，对二者使用字符串字典序比较，保证顺序稳定可复现。
func sortStorageIDKeysAsc(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		ai, errI := strconv.Atoi(keys[i])
		aj, errJ := strconv.Atoi(keys[j])
		if errI == nil && errJ == nil {
			return ai < aj
		}
		return keys[i] < keys[j]
	})
}
