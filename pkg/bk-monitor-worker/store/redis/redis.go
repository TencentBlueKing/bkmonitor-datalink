// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	retry "github.com/avast/retry-go"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

var (
	StoragePeriodicTaskKey        = fmt.Sprintf("%s:periodicTask", config.StorageRedisKeyPrefix)
	StoragePeriodicTaskChannelKey = fmt.Sprintf("%s:channel:periodicTask", config.StorageRedisKeyPrefix)
)

type Instance struct {
	ctx    context.Context
	Client goRedis.UniversalClient
}

var (
	storageRedisInstance *Instance
	storageRedisOnce     sync.Once
	cacheRedisInstance   *Instance
	cacheRedisOnce       sync.Once
)

// 两个类型的redis使用场景不一样，
// GetStorageRedisInstance 获取存储类型的 redis
func GetStorageRedisInstance() *Instance {
	if storageRedisInstance != nil {
		return storageRedisInstance
	}
	storageRedisOnce.Do(func() {
		opt := redisUtils.Option{
			Mode:             config.StorageRedisMode,
			Host:             config.StorageRedisStandaloneHost,
			Port:             config.StorageRedisStandalonePort,
			SentinelAddress:  config.StorageRedisSentinelAddress,
			MasterName:       config.StorageRedisSentinelMasterName,
			SentinelPassword: config.StorageRedisSentinelPassword,
			Password:         config.StorageRedisStandalonePassword,
			Db:               config.StorageRedisDatabase,
			DialTimeout:      config.StorageRedisDialTimeout,
			ReadTimeout:      config.StorageRedisReadTimeout,
		}
		storageRedisInstance = GetInstance(&opt)
	})
	return storageRedisInstance
}

// GetCacheRedisInstance 获取缓存类型的 redis
// 如获取transfer推送的指标等
func GetCacheRedisInstance() *Instance {
	if cacheRedisInstance != nil {
		return cacheRedisInstance
	}
	cacheRedisOnce.Do(func() {
		opt := redisUtils.Option{
			Mode:             config.StorageDependentRedisMode,
			Host:             config.StorageDependentRedisStandaloneHost,
			Port:             config.StorageDependentRedisStandalonePort,
			SentinelAddress:  config.StorageDependentRedisSentinelAddress,
			MasterName:       config.StorageDependentRedisSentinelMasterName,
			SentinelPassword: config.StorageDependentRedisSentinelPassword,
			Password:         config.StorageDependentRedisStandalonePassword,
			Db:               config.StorageDependentRedisDatabase,
			DialTimeout:      config.StorageDependentRedisDialTimeout,
			ReadTimeout:      config.StorageDependentRedisReadTimeout,
		}
		cacheRedisInstance = GetInstance(&opt)
	})
	return cacheRedisInstance
}

// GetInstance get a redis instance
func GetInstance(opt *redisUtils.Option) *Instance {
	ctx := context.TODO()
	var client goRedis.UniversalClient
	var err error

	err = retry.Do(
		func() error {
			client, err = redisUtils.NewRedisClient(ctx, opt)
			if err != nil {
				logger.Errorf(
					"Failed to create storageRedis, "+
						"tasks stored in this redis may not be executed. error: %s", err,
				)
				return err
			}
			return nil
		},
		retry.Attempts(3),
		retry.Delay(1*time.Second),
	)
	if err != nil {
		logger.Fatalf("failed to create redis storage client, error: %s", err)
	}

	return &Instance{ctx: ctx, Client: client}
}

// Open new a instance
func (r *Instance) Open() error {
	return nil
}

// Put put a key-val
func (r *Instance) Put(key, val string, expiration time.Duration) error {
	if err := r.Client.Set(r.ctx, key, val, expiration).Err(); err != nil {
		logger.Debugf("put redis error, key: %s, val: %s, err: %v", key, val, err)
		return err
	}
	return nil
}

// Get get a val from key
func (r *Instance) Get(key string) ([]byte, error) {
	data, err := r.Client.Get(r.ctx, key).Bytes()
	if err != nil {
		logger.Debugf("get redis key: %s error, %v", key, err)
		return nil, err
	}
	return data, nil
}

// Delete delete a key
func (r *Instance) Delete(key string) error {
	exist, err := r.Client.Exists(r.ctx, key).Result()
	if err != nil {
		logger.Debugf("check redis key: %s exist error, %v", key, err)
		return err
	}
	if exist == 0 {
		logger.Debugf("key: %s not exist from redis", key)
		return nil
	}
	if err := r.Client.Del(r.ctx, key).Err(); err != nil {
		logger.Debugf("delete key: %s error, %v", key, err)
		return err
	}
	return nil
}

// Close close connection
func (r *Instance) Close() error {
	if r.Client != nil {
		return r.Client.Close()
	}
	return nil
}

// HSetWithCompareAndPublish 与redis中数据不同才进行更新和推送操作
func (r *Instance) HSetWithCompareAndPublish(key, field, value, channelName, channelKey string) (bool, error) {
	// 参数非空校验
	if key == "" || field == "" || value == "" || channelName == "" || channelKey == "" {
		logger.Errorf("HSetWithCompareAndPublish: key or field or value or channelName or channelKey is empty")
		return false, fmt.Errorf("key or field or value or channelName or channelKey is empty")
	}
	// var isNeedUpdate bool
	logger.Infof("HSetWithCompareAndPublish: try to operate [redis_diff] HashSet key [%s] field [%s],value [%s] channelName [%s],channelKey [%s]", key, field, value, channelName, channelKey)
	oldValue := r.HGet(key, field)
	if oldValue == value {
		logger.Infof("HSetWithCompareAndPublish: [redis_diff] HashSet key [%s] field [%s] not need update, new [%s]  old [%s]", key, field, value, oldValue)
		return false, nil
	}
	if equal, _ := jsonx.CompareJson(oldValue, value); equal {
		return false, nil
	}
	logger.Debugf("HSetWithCompareAndPublish: [redis_diff] HashSet key [%s] field [%s] need update, new [%s]  old [%s]", key, field, value, oldValue)
	metrics.RedisCount(key, "HSet")
	err := r.Client.HSet(r.ctx, key, field, value).Err()
	if err != nil {
		logger.Errorf("HSetWithCompareAndPublish: hset field error, key: %s, field: %s, value: %s", key, field, value)
		return false, err
	}

	logger.Infof("HSetWithCompareAndPublish: [redis_diff] HashSet key [%s] field [%s] channelName [%s] channelKey [%s] now try to publish", key, field, channelName, channelKey)

	// 如果走到这里，说明当前value与Redis中的数据存在不同，需要进行推送操作
	if err := r.Publish(channelName, channelKey); err != nil {
		logger.Errorf("HSetWithCompareAndPublish: publish redis failed, channel: %s, key: %s, %s", channelName, channelKey, err)
		return false, err
	}

	logger.Infof("[redis_diff] HashSet key [%s] field [%s] channelName [%s] channelKey [%s] update and publish success", key, field, channelName, channelKey)
	return true, nil
}

// HSetManyWithCompareAndPublish 批量比较并更新 hash 中的 JSON 数据，返回实际发生变化的 field 数。
// 方法本身不切批；参数和临时内存随 field 数及 payload 大小线性增长，调用方负责限制批次。
// values 为空时直接返回；isPublish 为 true 时，仅为实际发生变化的 field 逐个发布通知。
func (r *Instance) HSetManyWithCompareAndPublish(
	key string, values map[string]string, channelName string, isPublish bool,
) (int, error) {
	if len(values) == 0 {
		return 0, nil
	}
	if key == "" {
		return 0, fmt.Errorf("HSetManyWithCompareAndPublish: key is empty")
	}
	if isPublish && channelName == "" {
		return 0, fmt.Errorf("HSetManyWithCompareAndPublish: channelName is empty when publish is enabled")
	}

	fields := make([]string, 0, len(values))
	for field := range values {
		if field == "" {
			return 0, fmt.Errorf("HSetManyWithCompareAndPublish: field is empty")
		}
		fields = append(fields, field)
	}
	// map 的遍历顺序不稳定。固定顺序既便于定位问题，也让发布顺序可预测。
	sort.Strings(fields)

	oldValues, err := r.Client.HMGet(r.ctx, key, fields...).Result()
	if err != nil {
		return 0, fmt.Errorf("HSetManyWithCompareAndPublish: hmget key %q failed: %w", key, err)
	}
	if len(oldValues) != len(fields) {
		return 0, fmt.Errorf(
			"HSetManyWithCompareAndPublish: hmget key %q returned %d values for %d fields",
			key, len(oldValues), len(fields),
		)
	}

	changedFields := make([]string, 0, len(fields))
	for i, field := range fields {
		oldValue, exists, err := redisStringValue(oldValues[i])
		if err != nil {
			return 0, fmt.Errorf(
				"HSetManyWithCompareAndPublish: decode old value for key %q field %q failed: %w",
				key, field, err,
			)
		}

		newValue := values[field]
		if exists {
			// 原始字节相等是大 payload 的低成本快路径，只有字节不同时才解析 JSON。
			if oldValue == newValue {
				continue
			}
			// 保持单字段接口的兼容语义：JSON 解析失败时视为不相等，用新值修复旧缓存。
			if equal, compareErr := resultTableDetailJSONEqual(oldValue, newValue); compareErr == nil && equal {
				continue
			}
		}
		changedFields = append(changedFields, field)
	}

	if len(changedFields) == 0 {
		return 0, nil
	}

	pipe := r.Client.Pipeline()
	defer pipe.Close()
	// HMGET 在 pipeline 外先完成一次读往返；随后一条 HSET 写完所有变化 field。
	// pipeline 只合并 HSET/PUBLISH 的网络往返，并不提供事务性；发布时每个 field
	// 仍对应一条独立的 PUBLISH 命令。
	hsetArgs := make([]any, 0, len(changedFields)*2)
	for _, field := range changedFields {
		hsetArgs = append(hsetArgs, field, values[field])
	}
	pipe.HSet(r.ctx, key, hsetArgs...)
	if isPublish {
		for _, field := range changedFields {
			pipe.Publish(r.ctx, channelName, field)
		}
	}
	if _, err := pipe.Exec(r.ctx); err != nil {
		return 0, fmt.Errorf("HSetManyWithCompareAndPublish: update key %q failed: %w", key, err)
	}

	for range changedFields {
		metrics.RedisCount(key, "HSet")
	}
	return len(changedFields), nil
}

// resultTableDetailJSONEqual 延续原有 JSON 语义比较，但 storage_cluster_records
// 必须保留数组顺序；分段顺序决定 UQ 推导的时间区间，不能按集合视为相等。
func resultTableDetailJSONEqual(oldValue, newValue string) (bool, error) {
	var oldObject, newObject map[string]json.RawMessage
	if err := json.Unmarshal([]byte(oldValue), &oldObject); err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(newValue), &newObject); err != nil {
		return false, err
	}

	oldRecords, oldHasRecords := oldObject["storage_cluster_records"]
	newRecords, newHasRecords := newObject["storage_cluster_records"]
	if oldHasRecords != newHasRecords {
		return false, nil
	}
	if oldHasRecords {
		var oldSegments, newSegments any
		if err := json.Unmarshal(oldRecords, &oldSegments); err != nil {
			return false, err
		}
		if err := json.Unmarshal(newRecords, &newSegments); err != nil {
			return false, err
		}
		if !reflect.DeepEqual(oldSegments, newSegments) {
			return false, nil
		}
	}

	return jsonx.CompareJson(oldValue, newValue)
}

func redisStringValue(value any) (string, bool, error) {
	switch value := value.(type) {
	case nil:
		return "", false, nil
	case string:
		return value, true, nil
	case []byte:
		return string(value), true, nil
	default:
		return "", false, fmt.Errorf("unexpected redis value type %T", value)
	}
}

// HSet 原生 hset 方法
func (r *Instance) HSet(key, field, value string) error {
	err := r.Client.HSet(r.ctx, key, field, value).Err()
	if err != nil {
		logger.Debugf("hset field error, key: %s, field: %s, value: %s", key, field, value)
		return err
	}
	return nil
}

func (r *Instance) HGet(key, field string) string {
	val := r.Client.HGet(r.ctx, key, field).Val()
	if val == "" {
		logger.Debugf("hset field error, key: %s, field: %s, value: %s", key, field, val)
	}
	return val
}

func (r *Instance) HGetAll(key string) map[string]string {
	val := r.Client.HGetAll(r.ctx, key).Val()
	if len(val) == 0 {
		logger.Debugf("hset field error, key: %s, value is empty", key)
	}
	return val
}

// Publish message
func (r *Instance) Publish(channelName string, msg any) error {
	if err := r.Client.Publish(r.ctx, channelName, msg).Err(); err != nil {
		return err
	}
	return nil
}

// Subscribe subscribe channel from redis
func (r *Instance) Subscribe(channelNames ...string) <-chan *goRedis.Message {
	p := r.Client.Subscribe(r.ctx, channelNames...)
	return p.Channel()
}

func (r *Instance) ZCount(key, min, max string) (int64, error) {
	zcount := r.Client.ZCount(r.ctx, key, min, max)
	return zcount.Result()
}

func (r *Instance) ZRangeByScoreWithScores(key string, opt *goRedis.ZRangeBy) ([]goRedis.Z, error) {
	return r.Client.ZRangeByScoreWithScores(r.ctx, key, opt).Result()
}

func (r *Instance) HMGet(key string, fields ...string) ([]any, error) {
	return r.Client.HMGet(r.ctx, key, fields...).Result()
}

// SAdd set add
func (r *Instance) SAdd(key string, field ...any) error {
	err := r.Client.SAdd(r.ctx, key, field...).Err()
	if err != nil {
		logger.Debugf("sadd fields error, key: %s, fields: %v", key, field)
		return err
	}
	return nil
}

// HKeys get all field of hash set
func (r *Instance) HKeys(key string) ([]string, error) {
	fields, err := r.Client.HKeys(r.ctx, key).Result()
	if err != nil {
		logger.Debugf("hkeys error, key: %s, err: %s", key, err)
		return nil, err
	}
	return fields, nil
}

// HScanFields 分页扫描 hash 并只向调用方返回 field。Redis HSCAN 仍会传回 field/value
// 对，本方法只是在客户端丢弃 value，因此分页限制的是单次峰值，不会消除总网络传输。
// count 只是分页提示；返回数量可能偏离 count，cursor 非零时也可能返回空页。
// HSCAN 不是一致性快照，并发修改时可能重复或遗漏，调用方应容忍重复并持续扫描到 cursor=0。
func (r *Instance) HScanFields(key string, cursor uint64, count int64) ([]string, uint64, error) {
	values, nextCursor, err := r.Client.HScan(r.ctx, key, cursor, "*", count).Result()
	if err != nil {
		return nil, 0, err
	}
	if len(values)%2 != 0 {
		return nil, 0, fmt.Errorf("hscan key %q returned odd field/value count %d", key, len(values))
	}
	fields := make([]string, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		fields = append(fields, values[index])
	}
	return fields, nextCursor, nil
}

// HDel delete hash set
func (r *Instance) HDel(key string, fields ...string) error {
	return r.Client.HDel(r.ctx, key, fields...).Err()
}
