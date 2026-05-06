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
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// defaultStorageMissReloadCooldown：配置未加载或未设置时的默认最短刷新间隔。
const defaultStorageMissReloadCooldown = 10 * time.Minute

// storageMissReloadCooldownNanos：GetStorage miss 触发 Consul 刷新的最短间隔（纳秒），运行时由 SetStorageMissReloadCooldown 写入。
var storageMissReloadCooldownNanos atomic.Int64

func init() {
	storageMissReloadCooldownNanos.Store(int64(defaultStorageMissReloadCooldown))
}

// SetStorageMissReloadCooldown 由配置加载逻辑调用（如 service/tsdb hook）。d<=0 时回退为 defaultStorageMissReloadCooldown。
func SetStorageMissReloadCooldown(d time.Duration) {
	if d <= 0 {
		d = defaultStorageMissReloadCooldown
	}
	storageMissReloadCooldownNanos.Store(int64(d))
}

func storageMissReloadCooldown() time.Duration {
	n := storageMissReloadCooldownNanos.Load()
	if n <= 0 {
		return defaultStorageMissReloadCooldown
	}
	return time.Duration(n)
}

var (
	storageMap     = make(map[string]*Storage)
	storageMapHash string
	storageLock    = new(sync.RWMutex)

	storageMissReloadGroup singleflight.Group

	// lastMissReloadAttemptUnixNano：上一次 miss 路径实际发起 ReloadStorageFromConsul 尝试的时间（Unix 纳秒）。
	lastMissReloadAttemptUnixNano atomic.Int64

	// ReloadStorageFromConsul 由上层（如 prometheus tsdb Service）注入；miss 时在释放读锁后按需调用
	ReloadStorageFromConsul func(context.Context) error
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
	// 与 storage map key 排序一致：按 storage ID 数值升序，便于日志对照
	sortStorageIDKeysAsc(newKeys)

	span.Set("old_hash", oldHash)
	span.Set("new_hash", hash)
	span.Set("storage_count", len(newStorageMap))
	span.Set("storage_keys", fmt.Sprintf("%v", newKeys))

	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap = newStorageMap
	storageMapHash = hash

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

// GetStorage 从内存 map 按 storageID 取 Storage。
// 命中不写 span 属性。miss 时在释放读锁后通过 ReloadStorageAfterMiss（singleflight + 可配置冷却，键 tsdb.storage_miss_reload_cooldown）尝试触发上层 Consul 刷新，再二次查找；
// 仍失败则 span/metadata 携带首次 miss 时内存 hash（storage_hash，兼容旧语义）与二次查找时刻内存 hash（storage_hash_after_reload），以及 storage_id；不带 map_keys。
func GetStorage(ctx context.Context, storageID string) (*Storage, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "get-tsdb-storage")
	defer span.End(&err)

	storageLock.RLock()
	storage, ok := storageMap[storageID]
	hashBeforeMiss := storageMapHash
	storageLock.RUnlock()

	if ok {
		return storage, nil
	}

	ReloadStorageAfterMiss(ctx)

	storageLock.RLock()
	storage, ok = storageMap[storageID]
	hashAfterReload := storageMapHash
	storageLock.RUnlock()

	if ok {
		return storage, nil
	}

	span.Set("storage_id", storageID)
	span.Set("storage_hash", hashBeforeMiss)
	span.Set("storage_hash_after_reload", hashAfterReload)
	metadata.NewMessage("tsdb_storage", "get storage miss: id=%s hash_before=%s hash_after=%s",
		storageID, hashBeforeMiss, hashAfterReload).Info(ctx)
	err = fmt.Errorf("%s: storageID: %s", ErrStorageNotFound, storageID)
	return nil, err
}

// SetStorage 写入实例到内存去
func SetStorage(storageID string, storage *Storage) {
	storageLock.Lock()
	defer storageLock.Unlock()

	storageMap[storageID] = storage
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

// ReloadStorageAfterMiss 在 GetStorage miss 路径调用：合并并发（singleflight）、并按 storageMissReloadCooldown() 节流对 Consul 的刷新尝试。
func ReloadStorageAfterMiss(ctx context.Context) {
	reloadFn := ReloadStorageFromConsul
	if reloadFn == nil {
		return
	}
	_, _, _ = storageMissReloadGroup.Do("tsdb-storage-miss-reload", func() (interface{}, error) {
		now := time.Now()
		lastNano := lastMissReloadAttemptUnixNano.Load()
		if lastNano != 0 {
			if now.Sub(time.Unix(0, lastNano)) < storageMissReloadCooldown() {
				return nil, nil
			}
		}
		lastMissReloadAttemptUnixNano.Store(now.UnixNano())
		_ = reloadFn(ctx)
		return nil, nil
	})
}
