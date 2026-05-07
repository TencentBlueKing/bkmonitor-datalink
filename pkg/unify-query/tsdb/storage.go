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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// defaultStorageMissReloadCooldown：配置未加载或未设置时的默认最短刷新间隔
const defaultStorageMissReloadCooldown = 10 * time.Minute

// missReloadSingleflightKey：所有 miss 刷新共享同一 singleflight key，用于合并并发请求
const storageMissReloadSingleflightKey = "tsdb-storage-miss-reload"

// ReloadStrategyWithCooldown 通用的"按 cooldown 节流 + singleflight 合并执行 reloadFn"策略。
type ReloadStrategyWithCooldown interface {
	Reload(ctx context.Context, cooldown time.Duration, reloadFn func() error)
}

// cooldownReloadStrategy 用 cooldown 节流 + singleflight 合并并发请求： 每次 Reload 的 cooldown / reloadFn 由调用方传入，strategy 自身只持有运行时态
type cooldownReloadStrategy struct {
	lastAttempt atomic.Int64
	group       singleflight.Group
}

func newCooldownReloadStrategy() *cooldownReloadStrategy {
	return &cooldownReloadStrategy{}
}

func (s *cooldownReloadStrategy) Reload(ctx context.Context, cooldown time.Duration, reloadFn func() error) {
	if reloadFn == nil {
		log.Errorf(ctx, "tsdb storage miss reload func not set")
		return
	}
	if cooldown <= 0 {
		cooldown = defaultStorageMissReloadCooldown
	}

	_, _, _ = s.group.Do(storageMissReloadSingleflightKey, func() (interface{}, error) {
		now := time.Now()
		lastNano := s.lastAttempt.Load()
		// 先判断冷却窗口；只有真正允许发起 reload 时才更新时间戳。
		if lastNano != 0 && now.Sub(time.Unix(0, lastNano)) < cooldown {
			log.Infof(ctx, "tsdb storage miss reload cooldown: %v", cooldown)
			return nil, nil
		}

		s.lastAttempt.Store(now.UnixNano())
		// singleflight 保证并发场景下只有一个 goroutine 实际执行 reloadFn。
		return nil, reloadFn()
	})
}

// resetForTest 用于测试重置状态
func (s *cooldownReloadStrategy) resetForTest() {
	s.lastAttempt.Store(0)
	s.group.Forget(storageMissReloadSingleflightKey)
}

func loadConsulStorageIDs() (ids []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			// Consul 实例未初始化等异常场景下，失败日志不能再因为取 ids 而 panic。
			err = fmt.Errorf("load consul storage ids panic: %v", r)
		}
	}()

	storages, err := getTsDBStorageInfo()
	if err != nil {
		return nil, err
	}

	ids = make([]string, 0, len(storages))
	for storageID := range storages {
		ids = append(ids, storageID)
	}
	sortStorageIDKeysAsc(ids)
	return ids, nil
}

func logReloadStorageMissWithConsulIDs(ctx context.Context, storageID string) {
	ids, err := loadConsulStorageIDs()
	if err != nil {
		log.Errorf(ctx, "load consul storage ids failed: %v", err)
		return
	}
	log.Infof(ctx, "tsdb storage miss reload trigger: requested_storage_id=%s consul_storage_ids=%v",
		storageID, ids)
}

var (
	storageMap     = make(map[string]*Storage)
	storageMapHash string
	storageLock    = new(sync.RWMutex)

	// 默认策略：通用 cooldown + singleflight 实现；service 层未注入 cooldown / reloadFn 之前，Reload 内会因 reloadFn==nil 走 noop 分支。
	defaultStorageMissReloadStrategy = newCooldownReloadStrategy()
	storageMissReloadStrategyLock    sync.RWMutex
	storageMissReloadStrategy        ReloadStrategyWithCooldown = defaultStorageMissReloadStrategy
	getTsDBStorageInfo                                          = consul.GetTsDBStorageInfo

	// 包级配置：cooldown + reloadFn，由 service 层在启动 / Reload 时通过 SetStorageMissReload 注入。
	storageMissReloadCfgLock sync.RWMutex
	storageMissReloadCfg     struct {
		cooldown time.Duration
		reloadFn func() error
	}
)

// SetStorageMissReloadStrategy 设置 GetStorage miss 时使用的 reload 策略；nil 时回退默认实现。
func SetStorageMissReloadStrategy(strategy ReloadStrategyWithCooldown) {
	if strategy == nil {
		strategy = defaultStorageMissReloadStrategy
	}
	storageMissReloadStrategyLock.Lock()
	defer storageMissReloadStrategyLock.Unlock()
	storageMissReloadStrategy = strategy
}

func getStorageMissReloadStrategy() ReloadStrategyWithCooldown {
	storageMissReloadStrategyLock.RLock()
	defer storageMissReloadStrategyLock.RUnlock()
	return storageMissReloadStrategy
}

// SetStorageMissReload 由 service 层调用，决定 GetStorage miss 时使用的 cooldown 与 reloadFn。
func SetStorageMissReload(cooldown time.Duration, reloadFn func() error) {
	storageMissReloadCfgLock.Lock()
	defer storageMissReloadCfgLock.Unlock()
	storageMissReloadCfg.cooldown = cooldown
	storageMissReloadCfg.reloadFn = reloadFn
}

func getStorageMissReloadConfig() (time.Duration, func() error) {
	storageMissReloadCfgLock.RLock()
	defer storageMissReloadCfgLock.RUnlock()
	return storageMissReloadCfg.cooldown, storageMissReloadCfg.reloadFn
}

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

// GetStorage 从内存 map 按 storageID 取 Storage
func GetStorage(ctx context.Context, storageID string) (*Storage, error) {
	// 1.尝试从内存 map 中获取 Storage
	storageLock.RLock()
	storage, ok := storageMap[storageID]
	storageLock.RUnlock()
	// 2.如果获取成功，则返回 Storage
	if ok {
		metric.TsDBGetStorageInc(ctx, metric.StorageResultHit)
		return storage, nil
	}
	// 3.如果获取失败，从 Consul 中获取 Storage
	// miss 后交给策略对象处理 reload；默认实现会做 singleflight 合并、cooldown 节流。
	var err error

	cooldown, baseReloadFn := getStorageMissReloadConfig()
	var reloadFn func() error
	if baseReloadFn != nil {
		reloadFn = func() error {
			logReloadStorageMissWithConsulIDs(ctx, storageID)
			err := baseReloadFn()
			if err != nil {
				log.Errorf(ctx, "tsdb storage miss reload from consul failed: requested_storage_id=%s reload_err=%v", storageID, err)
			}
			return err
		}
	}
	getStorageMissReloadStrategy().Reload(ctx, cooldown, reloadFn)

	// 4.如果第二次获取成功，则返回 Storage
	storageLock.RLock()
	storage, ok = storageMap[storageID]
	storageLock.RUnlock()

	if ok {
		metric.TsDBGetStorageInc(ctx, metric.StorageResultHitAfterReload)
		return storage, nil
	}
	// 5.如果第二次获取失败，则返回错误
	metric.TsDBGetStorageInc(ctx, metric.StorageResultMiss)
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
