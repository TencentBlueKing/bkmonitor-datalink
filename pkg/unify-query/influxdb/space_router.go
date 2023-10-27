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
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/kvstore"
	bolt "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/kvstore/bbolt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// SpaceRouter space router struct with bolt db
type SpaceRouter struct {
	kvClient kvstore.KVStore
}

type SpaceCache struct {
	client memcache.Cache
}

var spaceRouter *SpaceRouter

var cache *SpaceCache

func GetSpaceRouter(path, bucketName string) (*SpaceRouter, error) {
	if spaceRouter == nil {
		boltClient := bolt.NewClient(path, bucketName)
		if boltClient.DB == nil {
			if err := boltClient.Open(); err != nil {
				return nil, err
			}
		}

		spaceRouter = &SpaceRouter{
			kvClient: boltClient,
		}

	}
	return spaceRouter, nil
}

func InitSpaceCache() (*SpaceCache, error) {
	if cache == nil {
		c, err := memcache.NewRistretto()
		if err != nil {
			return nil, err
		}
		cache = &SpaceCache{
			client: c,
		}
	}

	return cache, nil
}

func (cache *SpaceCache) Get(ctx context.Context, spaceUid string) (redis.Space, bool) {
	metric.SpaceRequestCountInc(ctx, spaceUid, metric.SpaceTypeCache, metric.SpaceActionRead)

	data, exist := cache.client.Get(spaceUid)
	if exist {
		value, ok := data.(redis.Space)
		if !ok {
			log.Errorf(ctx, "assert space data error, %v", data)
			return nil, false
		}
		return value, true
	}
	return nil, false
}

// Add a space data to db
func (r *SpaceRouter) Add(ctx context.Context, spaceUid string, space redis.Space) error {
	v, err := space.MarshalMsg(nil)
	if err != nil {
		log.Errorf(ctx, "parse the space error space: %s, data: %+v, error: %v", spaceUid, space, err)
		return err
	}

	metric.SpaceRequestCountInc(ctx, spaceUid, metric.SpaceTypeBolt, metric.SpaceActionWrite)
	// 更新对应的值
	err = r.kvClient.Put(kvstore.String2byte(spaceUid), v)
	if err != nil {
		log.Errorf(ctx, "write space: %s error, %v", spaceUid, err)
		return err
	}
	return nil
}

// Get a space from db
func (r *SpaceRouter) Get(ctx context.Context, spaceUid string) redis.Space {
	// init cache
	cache, cacheErr := InitSpaceCache()
	if cacheErr == nil {
		space, ok := cache.Get(ctx, spaceUid)
		if ok {
			return space
		}
	} else {
		// 记录日志
		log.Errorf(ctx, "init space cache error, %v", cacheErr)
	}

	metric.SpaceRequestCountInc(ctx, spaceUid, metric.SpaceTypeBolt, metric.SpaceActionRead)
	v, err := r.kvClient.Get(kvstore.String2byte(spaceUid))
	if err != nil {
		log.Warnf(ctx, "get space: %s data error, %v", spaceUid, err)
		return nil
	}
	var val redis.Space
	if _, err := val.UnmarshalMsg(v); err != nil {
		log.Errorf(ctx, "parse space: %s, data: %+v, error: %v", spaceUid, v, err)
		return nil
	}
	// 添加缓存
	if cacheErr == nil {
		metric.SpaceRequestCountInc(ctx, spaceUid, metric.SpaceTypeCache, metric.SpaceActionWrite)

		// NOTE: 暂时使用 20 作为随机
		expiredTime := viper.GetInt64(memcache.RistrettoExpiredTimePath) + rand.Int63n(20)
		cache.client.SetWithTTL(spaceUid, val, 0, time.Duration(expiredTime)*time.Minute)
	}
	return val
}

var SpacePrint = func(ctx context.Context, spaceUid string) string {
	var res string

	// 通过配置获取前缀
	spaceRouter, err := GetSpaceRouter("", "")
	if err != nil {
		res += fmt.Sprintf("get space router error, %v", err)
		return res
	}
	if spaceUid != "" {
		res += fmt.Sprintf("show space(%s) detail\n", spaceUid)
		res += fmt.Sprintln("--------------------------------------------------------------------------------")

		space := spaceRouter.Get(ctx, spaceUid)
		// tsDBRouter:  dataID -> [tableInfo]
		for tableID, tsDBs := range space {
			s, _ := json.Marshal(tsDBs)
			res += fmt.Sprintf("hset %s:%s %s '%s'\n", redis.ServiceName(), spaceUid, tableID, s)
			res += fmt.Sprintln("--------------------------------------------------------------------------------")
		}
	} else {
		res += "# spaceUid List\n"
		spaceUidList, err := redis.GetSpaceIDList(ctx)
		if err != nil {
			res += err.Error()
			return res
		}

		for _, spaceUid := range spaceUidList {
			res += fmt.Sprintf("sadd %s %s\n", redis.ServiceName(), spaceUid)
		}
		res += fmt.Sprintln("--------------------------------------------------------------------------------")
	}
	return res
}

// Reload space data to bolt
func Reload(ctx context.Context) error {
	if spaceRouter != nil {
		spaceRouter.kvClient.Close()
		spaceRouter = nil
	}

	spaceUidList, err := redis.GetSpaceIDList(ctx)
	if err != nil {
		return err
	}
	log.Infof(ctx, "reload space list, %v", spaceUidList)
	if err := reloadAllSpaces(ctx, spaceUidList); err != nil {
		return err
	}
	return nil
}

func ReloadSpace(ctx context.Context, spaceUid string) error {
	spaceRouter, err := GetSpaceRouter("", "")
	if err != nil {
		log.Errorf(ctx, "get space router error, %v", err)
		return err
	}
	space, err := redis.GetSpace(ctx, spaceUid)
	log.Infof(ctx, "reload space %s => %+v", spaceUid, space)
	if err != nil {
		return err
	}
	// 写入 bolt
	if err := spaceRouter.Add(ctx, spaceUid, space); err != nil {
		return err
	}
	// 重新加载或者publish更新时，删除内存缓存数据
	cache, cacheErr := InitSpaceCache()
	if cacheErr == nil {
		metric.SpaceRequestCountInc(ctx, spaceUid, metric.SpaceTypeCache, metric.SpaceActionDelete)

		cache.client.Del(spaceUid)
	}
	return nil
}

// reload all spaces with one commit
func reloadAllSpaces(ctx context.Context, spaceUidList []string) error {
	spaceRouter, err := GetSpaceRouter("", "")
	if err != nil {
		log.Errorf(ctx, "get space router error, %v", err)
		return err
	}
	for _, suid := range spaceUidList {
		// NOTE: allow err
		space, err := redis.GetSpace(ctx, suid)
		if err != nil {
			log.Warnf(ctx, "get space error, space: %s, %v", suid, err)
			continue
		}

		metric.SpaceRequestCountInc(ctx, suid, metric.SpaceTypeBolt, metric.SpaceActionWrite)
		// 批量写入
		if err = spaceRouter.Add(ctx, suid, space); err != nil {
			log.Errorf(ctx, "batch write spaceUid error, %v", err)
			return err
		}
	}
	return nil
}
