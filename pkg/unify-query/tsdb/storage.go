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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/utils"
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
func ReloadTsDBStorage(ctx context.Context, tsDBs map[string]any, opt *Options) error {
	var err error
	ctx, span := trace.NewSpan(ctx, "reload-tsdb-storage")
	defer span.End(&err)

	hash := StorageConfigHash(tsDBs, opt)
	newStorageMap := make(map[string]*Storage, len(tsDBs))
	oldHash := storageMapHash

	for storageID, tsDB := range tsDBs {
		storageType, address, username, password := getStorageFields(tsDB)

		storage := &Storage{
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

	newKeys := make([]string, 0, len(newStorageMap))
	for k := range newStorageMap {
		newKeys = append(newKeys, k)
	}
	// 按 storage ID 数值升序，便于日志对照。
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

// StorageConfigHash 计算包含存储记录和运行时查询参数的稳定指纹。
func StorageConfigHash(tsDBs map[string]any, opt *Options) string {
	return utils.HashItDeterministic(struct {
		Storage map[string]any
		Options *Options
	}{
		Storage: tsDBs,
		Options: opt,
	})
}

func Print() string {
	storageLock.RLock()
	defer storageLock.RUnlock()
	str := "--------------------------- storage list --------------------------------------\n"
	for k, s := range storageMap {
		str += fmt.Sprintf(
			"%s: type=%s address=%s username=%s password=****** timeout=%s max_limit=%d max_slimit=%d\n",
			k,
			s.Type,
			s.Address,
			s.Username,
			s.Timeout,
			s.MaxLimit,
			s.MaxSLimit,
		)
	}
	return str
}

// GetStorage 从内存 map 按 storageID 取 Storage。
func GetStorage(ctx context.Context, storageID string) (*Storage, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "get-tsdb-storage")
	defer span.End(&err)

	storageLock.RLock()
	defer storageLock.RUnlock()

	storage, ok := storageMap[storageID]
	if !ok {
		// miss 场景每次记录缺失 storageID，并上报缺失 ID 指标用于排障
		metric.TSDBGetStorageTotalInc(ctx, metric.ResultMiss)
		metric.TSDBGetStorageMissIDTotalInc(ctx, storageID)
		metadata.NewMessage("tsdb_storage", "get storage miss: id=%s hash=%s",
			storageID, storageMapHash).Info(ctx)
		err = fmt.Errorf("%s: storageID: %s", ErrStorageNotFound, storageID)
		return nil, err
	}

	metric.TSDBGetStorageTotalInc(ctx, metric.ResultHit)

	return storage, nil
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
