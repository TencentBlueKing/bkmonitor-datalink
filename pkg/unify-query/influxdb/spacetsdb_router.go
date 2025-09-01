// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	bolt "go.etcd.io/bbolt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/kvstore"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/kvstore/bbolt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var (
	globalSpaceTsDbRouter     *SpaceTsDbRouter
	globalSpaceTsDbRouterLock sync.RWMutex
)

// getRedisRouterKey generates a  key for lookup ResultTableDetail or other space-related data.
// If enable MultiTenantMode, it appends the tenant ID to the key.
func getRedisRouterKey(ctx context.Context, key string) (newKey string) {
	newKey = key
	if !MultiTenantMode {
		return
	}

	user := metadata.GetUser(ctx)
	tenantID := user.TenantID

	newKey = key + "|" + tenantID

	return
}

type SpaceTsDbRouter struct {
	ctx          context.Context
	cancelFunc   context.CancelFunc
	rwLock       sync.RWMutex
	router       influxdb.Router
	routerPrefix string
	kvBucketName string
	kvPath       string
	kvClient     kvstore.KVStore

	isCache   bool
	cache     memcache.Cache
	hasInit   bool
	batchSize int
}

// SetSpaceTsDbRouter 设置全局可用的 Router 单例，用于管理空间数据
func SetSpaceTsDbRouter(ctx context.Context, kvPath string, kvBucketName string, routerPrefix string, batchSize int, isCache bool) (*SpaceTsDbRouter, error) {
	globalSpaceTsDbRouterLock.Lock()
	defer globalSpaceTsDbRouterLock.Unlock()
	if globalSpaceTsDbRouter != nil {
		return globalSpaceTsDbRouter, nil
	}
	if batchSize == 0 {
		return nil, errors.New("BatchSize must be positive integer")
	}
	globalSpaceTsDbRouter = &SpaceTsDbRouter{
		kvBucketName: kvBucketName,
		kvPath:       kvPath,
		routerPrefix: routerPrefix,
		batchSize:    batchSize,
		isCache:      isCache,
	}
	err := globalSpaceTsDbRouter.initRouter(ctx)
	if err != nil {
		return nil, err
	}
	return globalSpaceTsDbRouter, nil
}

func GetSpaceTsDbRouter() (*SpaceTsDbRouter, error) {
	if globalSpaceTsDbRouter == nil {
		return nil, errors.New("Initial Space TsDb Router first please ")
	}
	return globalSpaceTsDbRouter, nil
}

// BatchItemMeta 一个批次每个元素的更新情况
type BatchItemMeta struct {
	key string
	val influxdb.GenericValue
}

func (m *BatchItemMeta) Print() string {
	return fmt.Sprintf("Meta{key=%s, update=%s}", m.key, m.val.Print())
}

func (r *SpaceTsDbRouter) BatchAdd(ctx context.Context, stoPrefix string, entities []influxdb.GenericKV, once bool, printBytes bool) error {
	keys := make([][]byte, 0)
	values := make([][]byte, 0)
	batchItems := make([]*BatchItemMeta, 0)
	createdCount := 0
	updatedCount := 0
	for _, entity := range entities {
		var (
			keyNotFound bool
		)
		k := fmt.Sprintf("%s:%s", stoPrefix, entity.Key)
		v, err := entity.Val.Marshal(nil)
		if err != nil {
			log.Errorf(
				ctx, "Fail to parse value for MarshalMsg, %+v, error: %v", entity, err)
			if once {
				return err
			}
			continue
		}
		// 更新前读取上一次的数值是否一致
		rawV, kvErr := r.kvClient.Get(kvstore.String2byte(k))
		if kvErr != nil {
			if kvErr.Error() == bbolt.KeyNotFound || kvErr.Error() == bbolt.BucketNotFount {
				log.Debugf(ctx, "No key found and create, %s", k)
				keyNotFound = true
			}
		}
		if bytes.Equal(rawV, v) {
			continue
		}
		if keyNotFound {
			createdCount += 1
		} else {
			updatedCount += 1
		}
		batchItems = append(batchItems, &BatchItemMeta{key: k, val: entity.Val})
		keys = append(keys, kvstore.String2byte(k))
		values = append(values, v)
	}

	// 如果变更和新增都为空则不处理该逻辑
	if createdCount == 0 && updatedCount == 0 {
		return nil
	}

	err := r.kvClient.BatchWrite(keys, values)
	if err != nil {
		return err
	}
	// 记录更新日志
	log.Debugf(ctx, "[SpaceTSDB] Write count in kvStorage, once=%v, key=%s, %d created, %d updated", once, stoPrefix, createdCount, updatedCount)

	// 更新成功的对象，需要进行额外操作
	// 1. 清理对应的缓存
	// 2. 针对 ResultTableDetail 记录元数据情况
	// 3. 打印更新的对象内容
	for _, item := range batchItems {
		r.cache.Del(item.key)
		if rt, ok := item.val.(*influxdb.ResultTableDetail); ok {
			metric.ResultTableInfoSet(
				ctx, float64(len(rt.Fields)), rt.TableId, strconv.FormatInt(rt.DataId, 10), rt.MeasurementType,
				rt.VmRt, rt.BcsClusterID)
		}
		if printBytes {
			log.Debugf(ctx, "[SpaceTSDB] Write content in kvStorage, once=%v, %s", once, item.Print())
		}
	}
	return nil
}

// Add a space data to db
func (r *SpaceTsDbRouter) Add(ctx context.Context, stoPrefix string, stoKey string, stoValue influxdb.GenericValue) error {
	entities := make([]influxdb.GenericKV, 0, 1)
	entities = append(entities, influxdb.GenericKV{Key: stoKey, Val: stoValue})
	return r.BatchAdd(ctx, stoPrefix, entities, true, true)
}

// Delete a space data from db
func (r *SpaceTsDbRouter) Delete(ctx context.Context, stoPrefix string, stoKey string) error {
	fullKey := fmt.Sprintf("%s:%s", stoPrefix, stoKey)

	err := r.kvClient.Delete(kvstore.String2byte(fullKey))
	if err != nil {
		log.Warnf(ctx, "Failed to delete key(%s) from kvClient: %v", fullKey, err)
		return err
	}

	if r.isCache {
		r.cache.Del(fullKey)
	}

	log.Debugf(ctx, "[SpaceTSDB] Deleted key from storage and cache: %s", fullKey)
	log.Infof(ctx, "Deleted key from storage and cache: %s", fullKey)
	return nil
}

// Get a space data from db
func (r *SpaceTsDbRouter) Get(ctx context.Context, stoPrefix string, stoKey string, cached bool, ignoreKeyNotFound bool) influxdb.GenericValue {
	stoKey = fmt.Sprintf("%s:%s", stoPrefix, stoKey)
	stoVal, err := influxdb.NewGenericValue(stoPrefix)
	if err != nil {
		log.Warnf(ctx, "Fail to new generic value, %s", err)
		return nil
	}
	if cached && r.isCache {
		data, exist := r.cache.Get(stoKey)
		if exist {
			// 存入缓存的数据可能有 nil 情况，需要兼容
			if data == nil {
				return nil
			}
			value, ok := data.(influxdb.GenericValue)
			if ok {
				return value
			}
			log.Warnf(ctx, "Fail to unSerialize cached data, %s, %v", stoKey, data)
		}
	}
	v, err := r.kvClient.Get(kvstore.String2byte(stoKey))
	if err != nil {
		if err.Error() == "keyNotFound" {
			if !ignoreKeyNotFound {
				log.Debugf(ctx, "Key(%s) not found in KVBolt", stoKey)
			}
		} else {
			log.Warnf(ctx, "Fail to get value in KVBolt, key: %s, error: %v", stoKey, err)
		}
		stoVal = nil
	} else {
		if _, err := stoVal.Unmarshal(v); err != nil {
			log.Errorf(ctx, "Fail to parse value in KVBolt, key: %s, data: %+v, error: %v", stoKey, v, err)
			stoVal = nil
		}
	}
	// 添加缓存
	if cached && r.isCache {
		// NOTE: 暂时使用 20 作为随机
		expiredTime := viper.GetInt64(memcache.RistrettoExpiredTimePath) + rand.Int63n(viper.GetInt64(memcache.RistrettoExpiredTimeFluxValuePath))
		r.cache.SetWithTTL(stoKey, stoVal, 0, time.Duration(expiredTime)*time.Minute)
	}
	return stoVal
}

func (r *SpaceTsDbRouter) initRouter(ctx context.Context) error {
	r.rwLock.Lock()
	defer r.rwLock.Unlock()
	if r.hasInit {
		return nil
	}

	r.ctx, r.cancelFunc = context.WithCancel(ctx)
	// 初始化 bolt 本地文件存储
	boltClient := bbolt.NewClient(r.kvPath, r.kvBucketName)
	if boltClient.DB == nil {
		if err := boltClient.Open(); err != nil {
			return err
		}
	}
	r.kvClient = boltClient
	// 初始化内存缓存
	c, err := memcache.NewRistretto()
	if err != nil {
		return err
	}
	r.cache = c
	// 初始化 redis 路由器
	rdbClient := redis.Client()
	if rdbClient == nil {
		return errors.New("No available redis client in global namespace ")
	}
	r.router = influxdb.NewRouter(r.routerPrefix, rdbClient)
	r.hasInit = true
	return nil
}

func (r *SpaceTsDbRouter) RouterSubscribe(ctx context.Context) <-chan *goRedis.Message {
	r.rwLock.RLock()
	defer r.rwLock.RUnlock()
	return r.router.SubscribeChannels(ctx, influxdb.SpaceChannelKeys...)
}

func (r *SpaceTsDbRouter) ReloadAllKey(ctx context.Context, printBytes bool) error {
	for _, k := range influxdb.SpaceAllKey {
		err := r.LoadRouter(ctx, k, printBytes)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *SpaceTsDbRouter) ReloadByChannel(ctx context.Context, channelKey string, hashKey string) error {
	r.rwLock.Lock()
	defer r.rwLock.Unlock()
	if strings.HasPrefix(channelKey, r.routerPrefix) {
		channelKey = channelKey[len(r.routerPrefix)+1:]
	}
	switch channelKey {
	case influxdb.BkAppToSpaceChannelKey:
		spaceUidList, err := r.router.GetBkAppSpace(ctx, hashKey)
		if err != nil {
			return err
		}
		err = r.Add(ctx, influxdb.BkAppToSpaceKey, hashKey, &spaceUidList)
	case influxdb.SpaceToResultTableChannelKey:
		space, err := r.router.GetSpace(ctx, hashKey)
		if err != nil {
			return err
		}
		err = r.Add(ctx, influxdb.SpaceToResultTableKey, hashKey, &space)
		if err != nil {
			return err
		}
	case influxdb.ResultTableDetailChannelKey:
		table, err := r.router.GetResultTableDetail(ctx, hashKey)
		if err != nil {
			return err
		}
		err = r.Add(ctx, influxdb.ResultTableDetailKey, hashKey, table)
		if err != nil {
			return err
		}
	case influxdb.DataLabelToResultTableChannelKey:
		tableIds, err := r.router.GetDataLabelToResultTableDetail(ctx, hashKey)
		if err != nil {
			return err
		}
		err = r.Add(ctx, influxdb.DataLabelToResultTableKey, hashKey, &tableIds)
		if err != nil {
			return err
		}
	case influxdb.ResultTableDetailChannelDeleteKey:
		err := r.Delete(ctx, influxdb.ResultTableDetailKey, hashKey)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("Invalid channel key(%s) from subscribe process ", channelKey)
	}
	return nil
}

func (r *SpaceTsDbRouter) LoadRouter(ctx context.Context, key string, printBytes bool) error {
	r.rwLock.Lock()
	defer r.rwLock.Unlock()
	start := time.Now()
	defer func() {
		log.Debugf(ctx, "[SpaceTSDB] Load key(%s), time cost: %s", key, time.Since(start))
	}()
	var (
		err error
		ok  bool
		val influxdb.GenericKV
	)
	batchSize := int64(r.batchSize)
	entities := make([]influxdb.GenericKV, 0)

	genericCh := make(chan influxdb.GenericKV, batchSize)
	go r.router.IterGenericKeyResult(ctx, key, batchSize, genericCh)

	count := int64(0)

	for {
		select {
		case val, ok = <-genericCh:
			if ok {
				if val.Err != nil {
					log.Errorf(ctx, "Record error when loading, %v", val.Err)
					continue
				}
				entities = append(entities, val)
				count += 1
			}
			if !ok || count%batchSize == 0 {
				log.Debugf(ctx, "Read %v entities from key(%s) channel", len(entities), key)
				err = r.BatchAdd(ctx, key, entities, false, printBytes)
				if err != nil {
					log.Errorf(ctx, "Fail to add batch from key(%s), %v", key, err)
				}
				// 清空缓存
				count = 0
				entities = entities[:0]
			}
			if !ok {
				return nil
			}
		}
	}
}

func (r *SpaceTsDbRouter) Stop() error {
	r.rwLock.Lock()
	defer r.rwLock.Unlock()
	if !r.hasInit {
		return nil
	}

	if r.router != nil {
		err := r.router.Close()
		if err != nil {
			return err
		}
	}

	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	if r.kvClient != nil {
		err := r.kvClient.Close()
		if err != nil {
			return err
		}
	}
	r.hasInit = false
	return nil
}

// GetSpaceUIDList 获取 bkAppCode 下的空间信息
func (r *SpaceTsDbRouter) GetSpaceUIDList(ctx context.Context, bkAppCode string) *influxdb.SpaceUIDList {
	genericRet := r.Get(ctx, influxdb.BkAppToSpaceKey, bkAppCode, true, true)
	if genericRet != nil {
		return genericRet.(*influxdb.SpaceUIDList)

	}
	return nil
}

// GetSpace 获取空间信息
func (r *SpaceTsDbRouter) GetSpace(ctx context.Context, spaceID string) influxdb.Space {
	key := getRedisRouterKey(ctx, spaceID)
	genericRet := r.Get(ctx, influxdb.SpaceToResultTableKey, key, true, false)
	if genericRet != nil {
		return *genericRet.(*influxdb.Space)
	}
	return nil
}

// GetResultTable 获取 RT 详情
func (r *SpaceTsDbRouter) GetResultTable(ctx context.Context, tableID string, ignoreKeyNotFound bool) *influxdb.ResultTableDetail {
	key := getRedisRouterKey(ctx, tableID)
	genericRet := r.Get(ctx, influxdb.ResultTableDetailKey, key, true, ignoreKeyNotFound)
	if genericRet != nil {
		return genericRet.(*influxdb.ResultTableDetail)
	}
	return nil
}

// GetDataLabelRelatedRts 获取 DataLabel 详情，仅包含映射的 RT 信息
func (r *SpaceTsDbRouter) GetDataLabelRelatedRts(ctx context.Context, dataLabel string) influxdb.ResultTableList {
	key := getRedisRouterKey(ctx, dataLabel)
	genericRet := r.Get(ctx, influxdb.DataLabelToResultTableKey, key, true, false)
	if genericRet != nil {
		return *genericRet.(*influxdb.ResultTableList)
	}
	return nil
}

func (r *SpaceTsDbRouter) Print(ctx context.Context, typeKey string, includeContent bool) string {
	ret := make([]string, 0)
	parts := make([]string, 0)
	typeCounter := make(map[string]int64)
	err := r.kvClient.(*bbolt.Client).DB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(r.kvBucketName))
		if bucket == nil {
			return fmt.Errorf("Bucket(%s) not found ", r.kvBucketName)
		}
		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			ks := string(k)
			parts = strings.Split(ks, ":")
			if len(parts) != 2 {
				continue
			}
			// 如果声明了 typeKey 就需要过滤以这个内容进行匹配
			if typeKey != "" && typeKey != parts[0] {
				continue
			}
			typeCounter[parts[0]] += 1
			// 如果不需要原始内容，则跳过以下内容解析过程
			if !includeContent {
				continue
			}
			// 遍历并解析存储值
			stoVal, err := influxdb.NewGenericValue(parts[0])
			if err != nil {
				log.Errorf(ctx, "Fail to new generic value, %v", err)
				continue
			}
			if _, err := stoVal.Unmarshal(v); err != nil {
				log.Errorf(ctx, "Fail to parse value in KVBolt, key: %s, data: %+v, error: %v", ks, v, err)
				continue
			}
			ret = append(ret, fmt.Sprintf("$%-80s : %+v", ks, stoVal.Print()))
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("Fail to read all content from bbolt client, %v", err)
	}
	ret = append(ret, fmt.Sprint("---------------------------------------------------------------------"))
	for k, v := range typeCounter {
		ret = append(ret, fmt.Sprintf("$count:%-40s: %v", k, v))
	}
	return strings.Join(ret, "\n")
}
