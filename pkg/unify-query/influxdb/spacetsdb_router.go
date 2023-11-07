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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var (
	globalSpaceTsDbRouter     *SpaceTsDbRouter
	globalSpaceTsDbRouterLock sync.RWMutex
)

type SpaceTsDbRouter struct {
	ctx          context.Context
	cancelFunc   context.CancelFunc
	rwLock       sync.RWMutex
	router       influxdb.Router
	routerPrefix string
	kvBucketName string
	kvPath       string
	kvClient     kvstore.KVStore
	cache        memcache.Cache
	hasInit      bool
}

// SetSpaceTsDbRouter 设置全局可用的 Router 单例，用于管理空间数据
func SetSpaceTsDbRouter(ctx context.Context, kvPath string, kvBucketName string, routerPrefix string) (*SpaceTsDbRouter, error) {
	globalSpaceTsDbRouterLock.Lock()
	defer globalSpaceTsDbRouterLock.Unlock()
	if globalSpaceTsDbRouter != nil {
		return globalSpaceTsDbRouter, nil
	}
	globalSpaceTsDbRouter = &SpaceTsDbRouter{
		kvBucketName: kvBucketName,
		kvPath:       kvPath,
		routerPrefix: routerPrefix,
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

// Add a space data to db
func (r *SpaceTsDbRouter) Add(ctx context.Context, stoPrefix string, stoKey string, stoValue influxdb.GenericValue) error {
	stoKey = fmt.Sprintf("%s:%s", stoPrefix, stoKey)
	v, err := stoValue.Marshal(nil)
	if err != nil {
		log.Errorf(
			ctx, "Fail to parse value for MarshalMsg, key: %s, value: %+v, error: %v", stoKey, stoValue, err)
		return err
	}
	// 更新前读取上一次的数值是否一致
	keyNotFount := false
	rawV, err := r.kvClient.Get(kvstore.String2byte(stoKey))
	if err != nil {
		if err.Error() == bbolt.KeyNotFound || err.Error() == bbolt.BucketNotFount {
			log.Debugf(ctx, "No key found and create, %s, %v", stoKey, stoValue)
			keyNotFount = true
		}
	}
	if bytes.Equal(rawV, v) {
		log.Debugf(ctx, "No change and not to write, %s, %v", stoKey, stoValue)
		return nil
	}

	// 更新对应的值
	if keyNotFount {
		metric.SpaceRequestCountInc(ctx, stoPrefix, metric.SpaceTypeBolt, metric.SpaceActionCreate)
	} else {
		metric.SpaceRequestCountInc(ctx, stoPrefix, metric.SpaceTypeBolt, metric.SpaceActionWrite)
	}
	err = r.kvClient.Put(kvstore.String2byte(stoKey), v)
	if err != nil {
		log.Errorf(ctx, "Fail to write space to KVBolt, %s, %v", stoKey, err)
		return err
	}
	// 当 bBolt 文件更新时，需要重置内存缓存数据
	r.cache.Del(stoKey)
	return nil
}

// Get a space data from db
func (r *SpaceTsDbRouter) Get(ctx context.Context, stoPrefix string, stoKey string, cached bool) influxdb.GenericValue {
	stoKey = fmt.Sprintf("%s:%s", stoPrefix, stoKey)
	stoVal := NewGenericValue(stoPrefix)
	if stoVal == nil {
		log.Warnf(ctx, "Invalid type({%s})", stoPrefix)
		return nil
	}
	if cached {
		data, exist := r.cache.Get(stoKey)
		if exist {
			metric.SpaceRequestCountInc(ctx, stoPrefix, metric.SpaceTypeCache, metric.SpaceActionRead)
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
	metric.SpaceRequestCountInc(ctx, stoPrefix, metric.SpaceTypeBolt, metric.SpaceActionRead)
	v, err := r.kvClient.Get(kvstore.String2byte(stoKey))
	if err != nil {
		if err.Error() == "keyNotFound" {
			log.Infof(ctx, "Key(%s) not found in KVBolt", stoKey)
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
	if cached {
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

func (r *SpaceTsDbRouter) ReloadAllKey(ctx context.Context) error {
	for _, k := range influxdb.SpaceAllKey {
		err := r.LoadRouter(ctx, k)
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
	case influxdb.FieldToResultTableChannelKey:
		tableIds, err := r.router.GetFieldToResultTableDetail(ctx, hashKey)
		if err != nil {
			return err
		}
		err = r.Add(ctx, influxdb.FieldToResultTableKey, hashKey, &tableIds)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("Invalid channel key(%s) from subscribe process ", channelKey)
	}
	return nil
}

func (r *SpaceTsDbRouter) LoadRouter(ctx context.Context, key string) error {
	r.rwLock.Lock()
	defer r.rwLock.Unlock()
	var (
		err error
	)
	switch key {
	case influxdb.SpaceToResultTableKey:
		spaceInfo, err := r.router.GetSpaceInfo(ctx)
		if err == nil {
			for k, v := range spaceInfo {
				r.Add(ctx, influxdb.SpaceToResultTableKey, k, &v)
			}
		}
	case influxdb.DataLabelToResultTableKey:
		dataLabelInfo, err := r.router.GetDataLabelResultTable(ctx)
		if err == nil {
			for k, v := range dataLabelInfo {
				r.Add(ctx, influxdb.DataLabelToResultTableKey, k, &v)
			}
		}
	case influxdb.ResultTableDetailKey:
		resultTableInfo, err := r.router.GetResultTableDetailInfo(ctx)
		if err == nil {
			for k, v := range resultTableInfo {
				r.Add(ctx, influxdb.ResultTableDetailKey, k, v)
			}
		}
	case influxdb.FieldToResultTableKey:
		fieldInfo, err := r.router.GetFieldToResultTable(ctx)
		if err == nil {
			for k, v := range fieldInfo {
				r.Add(ctx, influxdb.FieldToResultTableKey, k, &v)
			}
		}
	}
	if err != nil {
		log.Errorf(ctx, "Fail to get %s information from router, %v", key, err)
	}
	return err
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

// GetSpace 获取空间信息
func (r *SpaceTsDbRouter) GetSpace(ctx context.Context, spaceID string) influxdb.Space {
	genericRet := r.Get(ctx, influxdb.SpaceToResultTableKey, spaceID, true)
	if genericRet != nil {
		return *genericRet.(*influxdb.Space)
	}
	return nil
}

// GetResultTable 获取 RT 详情
func (r *SpaceTsDbRouter) GetResultTable(ctx context.Context, tableID string) *influxdb.ResultTableDetail {
	genericRet := r.Get(ctx, influxdb.ResultTableDetailKey, tableID, true)
	if genericRet != nil {
		return genericRet.(*influxdb.ResultTableDetail)
	}
	return nil
}

// GetDataLabelRelatedRts 获取 DataLabel 详情，仅包含映射的 RT 信息
func (r *SpaceTsDbRouter) GetDataLabelRelatedRts(ctx context.Context, dataLabel string) influxdb.ResultTableList {
	genericRet := r.Get(ctx, influxdb.DataLabelToResultTableKey, dataLabel, true)
	if genericRet != nil {
		return *genericRet.(*influxdb.ResultTableList)
	}
	return nil
}

// GetFieldRelatedRts 获取 Field 指标详情，仅包含映射的 RT 信息
func (r *SpaceTsDbRouter) GetFieldRelatedRts(ctx context.Context, field string) influxdb.ResultTableList {
	genericRet := r.Get(ctx, influxdb.FieldToResultTableKey, field, true)
	if genericRet != nil {
		return *genericRet.(*influxdb.ResultTableList)
	}
	return nil
}

func (r *SpaceTsDbRouter) Print(ctx context.Context, typeKey string) string {
	ret := make([]string, 0)
	parts := make([]string, 0)
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
			// 遍历并解析存储值
			stoVal := NewGenericValue(parts[0])
			if stoVal == nil {
				log.Errorf(ctx, "Invalid type({%s})", parts[0])
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
	return strings.Join(ret, "\n")
}

func NewGenericValue(typeKey string) influxdb.GenericValue {
	var stoVal influxdb.GenericValue
	switch typeKey {
	case influxdb.FieldToResultTableKey:
		stoVal = &influxdb.ResultTableList{}
	case influxdb.SpaceToResultTableKey:
		stoVal = &influxdb.Space{}
	case influxdb.DataLabelToResultTableKey:
		stoVal = &influxdb.ResultTableList{}
	case influxdb.ResultTableDetailKey:
		stoVal = &influxdb.ResultTableDetail{}
	}
	return stoVal
}
