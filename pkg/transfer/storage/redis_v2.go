// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/hashicorp/go-rootcerts"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

var (
	// MonitorRedisCommandSuccess redis 命令执行成功次数
	MonitorRedisCommandSuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "redis_command_successes_total",
		Help:      "Successes for redis command",
	}, []string{"command"})

	// MonitorRedisCommandFail redis 命令执行失败次数
	MonitorRedisCommandFail = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "redis_command_fails_total",
		Help:      "Fails for redis command",
	}, []string{"command"})

	// MonitorRedisExecuteDuration redis命令执行耗时
	MonitorRedisExecuteDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: define.AppName,
		Name:      "redis_command_execute_seconds",
		Help:      "duration spent to execute the redis command in seconds",
		Buckets:   monitor.DefBuckets,
	}, []string{"command"})

	// MonitorHitMemTotal 缓存指标命中次数
	MonitorHitMemTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "redis_hit_memory_total",
		Help:      "hits in memory",
	})

	// MonitorSkipNilTotal 查询缓存指标不存在次数
	MonitorSkipNilTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "redis_skip_nil_total",
		Help:      "skip nil key counter",
	})
)

func init() {
	prometheus.MustRegister(
		MonitorRedisCommandSuccess,
		MonitorRedisCommandFail,
		MonitorHitMemTotal,
		MonitorSkipNilTotal,
		MonitorRedisExecuteDuration,
	)
}

// 实现redisLogger
type redisLogger struct {
	logger *logging.Logger
}

func (l *redisLogger) Printf(_ context.Context, format string, v ...interface{}) {
	l.logger.Infof(format, v...)
}

func redisLoggerWrapper() *redisLogger {
	return &redisLogger{logger: logging.GetStdLogger()}
}

func init() {
	redis.SetLogger(redisLoggerWrapper())
}

const ConfSchedulerCCCacheExpires = "scheduler.cc_cache_expires"

const (
	RedisStandAloneType = "standalone" // 单节点redis
	RedisSentinelType   = "sentinel"   // 哨兵模式redis，哨兵实例
)

// Redis :
type Redis struct {
	*monitorRedis
	Slave *monitorRedis
	ctx   context.Context
}

// Ctx:
func (r *Redis) Ctx() context.Context {
	if r.ctx != nil {
		return r.ctx
	}
	return context.Background()
}

type monitorRedis struct {
	*redis.Client
	monitorHScan                 *monitor.CounterMixin // HScan 命令执行的成功/失败次数
	monitorHGet                  *monitor.CounterMixin // HGet 命令执行的成功/失败次数
	monitorHMGet                 *monitor.CounterMixin // HMGet 命令执行的成功/失败次数
	monitorHSet                  *monitor.CounterMixin // HSet 命令执行的成功/失败次数
	monitorHDel                  *monitor.CounterMixin // HDel 命令执行的成功/失败次数
	monitorZAdd                  *monitor.CounterMixin // ZAdd 命令执行的成功/失败次数
	monitorZRangeByScore         *monitor.CounterMixin // ZRangeByScore 命令执行的成功/失败次数
	monitorHScanDuration         *monitor.TimeObserver // HScan 命令执行成功耗时
	monitorHGetDuration          *monitor.TimeObserver // HGet 命令执行成功耗时
	monitorHMGetDuration         *monitor.TimeObserver // HMGet 命令执行成功耗时
	monitorHSetDuration          *monitor.TimeObserver // HSet 命令执行成功耗时
	monitorHDelDuration          *monitor.TimeObserver // HDel 命令执行成功耗时
	monitorZAddDuration          *monitor.TimeObserver // ZAdd 命令执行成功耗时
	monitorZRangeByScoreDuration *monitor.TimeObserver // ZRangeByScore 命令执行成功耗时
}

// RedisStore :
type RedisStore struct {
	redis         *Redis                // redis client
	batchSize     int                   // 指定批量操作时的数据量
	writeSize     int                   // 一次批量写入的最大值
	cacheKey      string                // redis中cmdb缓存的key
	writeCache    map[string]StoreCache // 用于批量写入缓存数据
	hotDataCache  sync.Map              // 热数据缓存 {key: *define.StoreItem}
	hotDataKeys   *sync.Map             // 热数据缓存keys {key: true}
	expiresPeriod time.Duration         // redis中的数据过期间隔
	mu            *sync.Mutex
	opMu          *sync.Mutex

	// 尽量保持 monitor 指标 在循环中使用时，在return之前计数，保证一次操作，指标只Inc一次。
	distributedLockEnabled    bool          // 是否开启分布式锁
	distributedLockKey        string        // 分布式锁key
	distributedLockValue      string        // 分布式锁信息，标识自己，可以用于重入等
	distributedExpireDuration time.Duration // 锁过期时间

	missCachedMut sync.Mutex
	missCached    map[string]time.Time
}

var cacheReady chan struct{}

// Exists :
func (s *RedisStore) Exists(key string) (bool, error) {
	value, err := s.redis.HExists(s.redis.Ctx(), s.cacheKey, key).Result()
	if err != nil {
		return false, err
	}
	return value, nil
}

// Set :
func (s *RedisStore) Set(key string, data []byte, expires time.Duration) error {
	item := define.NewStoreItem(data, expires)
	// 此处先set进内存中，保证leader内存中的store可用且为最新的缓存数据。当redis出问题时，能保证单机的transfer正常工作
	s.setIntoMem(key, item)
	value, err := json.Marshal(item)
	if err != nil {
		logging.Errorf("marshal key:[%s] data:[%s] ", key, data)
		return err
	}
	observer := s.redis.Slave.monitorHSetDuration.Start()
	_, err = s.redis.HSet(s.redis.Ctx(), s.cacheKey, key, value).Result()
	if err != nil {
		s.redis.monitorHSet.CounterFails.Inc()
		observer.Finish()
		return err
	}
	s.redis.monitorHSet.CounterSuccesses.Inc()
	observer.Finish()
	return err
}

// Get :
func (s *RedisStore) Get(key string) ([]byte, error) {
	var (
		result []byte
		err    error
		ok     bool
		i      int
	)

	// 先从内存中获取 取到则直接返回
	result, ok = s.getFromMem(key)
	if ok {
		return result, nil
	}

	// 内存中没有取到 尝试访问 redis 获取
	// 整体加锁 避免瞬时穿透
	s.missCachedMut.Lock()
	defer s.missCachedMut.Unlock()
	now := time.Now()
	latest, ok := s.missCached[key]
	if ok && now.Unix()-latest.Unix() < 10 {
		// 如果已经查询过且 10s 已经有访问 则跳过
		return nil, define.ErrItemNotFound // redis key 不存在
	}
	s.missCached[key] = now

	// 内存中没有，说明还没有pipeline将数据放到内存中，则从数据库中获取
	// 重试三次
	logging.Infof("data not fount in mem, try to get from redis")

	for i < 3 {
		i++
		logging.Warnf("get key [%s] from redis, retry [%d] time", key, i)

		observer := s.redis.Slave.monitorHGetDuration.Start()
		result, err = s.redis.Slave.HGet(s.redis.Ctx(), s.cacheKey, key).Bytes()
		observer.Finish()

		// 如果key不存在，则直接返回
		if err == redis.Nil {
			MonitorSkipNilTotal.Inc()
			return nil, define.ErrItemNotFound
		}
		// 否则视为网络波动等其他问题，试着重试
		if err != nil {
			logging.Warnf("try to get key[%s] [%d] time fail, err:[%s]", key, i, err)
			continue
		}

		// 走到这里，说明HGet成功
		s.redis.Slave.monitorHGet.CounterSuccesses.Inc()

		// 将获取的数据写入内存一份
		item := new(define.StoreItem)
		marshalErr := json.Unmarshal(result, item)
		if marshalErr != nil {
			err = marshalErr
			logging.Errorf("marshal:[%s] error err:[%s]", string(result), marshalErr)
			return nil, marshalErr
		}
		s.setIntoMem(key, item)
		return item.GetData(false), nil
	}
	// 获取三次，仍然失败，则返回错误
	logging.Errorf("try to get [%s] from redis failed: err: [%s]", key, err)
	// 为了便于观察和计算，这里重试三次，算作一次HGet失败
	s.redis.Slave.monitorHGet.CounterFails.Inc()
	return nil, err
}

func (s *RedisStore) getFromMem(key string) ([]byte, bool) {
	value, ok := s.hotDataCache.Load(key)
	s.hotDataKeys.LoadOrStore(key, true)

	if !ok {
		return nil, ok
	}

	data, ok := value.(*define.StoreItem)
	MonitorHitMemTotal.Inc()
	return data.GetData(false), ok
}

func (s *RedisStore) setIntoMem(key interface{}, item *define.StoreItem) {
	logging.Debugf("set key:[%s] value:[%s] expiresAt:[%s], into mem", key, item.GetData(false), item.ExpiresAt)
	s.hotDataCache.Store(key, item)
}

// Delete :
func (s *RedisStore) Delete(key string) error {
	observer := s.redis.Slave.monitorHDelDuration.Start()
	_, err := s.redis.HDel(s.redis.Ctx(), s.cacheKey, key).Result()
	observer.Finish()
	if err != nil {
		logging.Warnf("delete key error:[%s], err:[%s]", key, err)
		s.redis.monitorHDel.CounterFails.Inc()
		return err
	}

	s.hotDataCache.Delete(key)
	logging.Debugf("delete key success:[%s]", key)
	s.redis.monitorHDel.CounterSuccesses.Inc()
	return err
}

// Commit :
func (s *RedisStore) Commit() error {
	// 发送信号，将缓存通道中的数据刷盘
	UpdateSignal <- struct{}{}
	return nil
}

// AllKeys : 获取Hash缓存所有的field
func (s *RedisStore) AllKeys() ([]string, error) {
	result, err := s.redis.HKeys(s.redis.Ctx(), s.cacheKey).Result()
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ZAddBatch : 将多个member和score写入到redis的sorted set中
// ZAdd命令使用文档：https://redis.io/commands/zadd/
func (s *RedisStore) ZAddBatch(key string, memberScorePairs map[string]float64) error {
	pairs := []*redis.Z{}
	for member, score := range memberScorePairs {
		pairs = append(pairs, &redis.Z{score, member})
	}

	// 分批写入
	pairsLen := len(pairs)
	for start := 0; start < pairsLen; start = start + s.writeSize {
		end := start + s.writeSize
		if end > pairsLen {
			end = pairsLen
		}
		observer := s.redis.Slave.monitorZAddDuration.Start()
		_, err := s.redis.ZAdd(s.redis.Ctx(), key, pairs[start:end]...).Result()
		observer.Finish()
		if err != nil {
			logging.Errorf("ZAdd error, key: %s, err: %s", key, err)
			s.redis.monitorZAdd.CounterFails.Inc()
			return err
		}
		s.redis.monitorZAdd.CounterSuccesses.Inc()
	}

	return nil
}

// ZRangeByScore : 获取sorted set指定score区间的members
// ZRangeByScore命令使用文档：https://redis.io/commands/zrangebyscore/
func (s *RedisStore) ZRangeByScore(key string, min, max float64) ([]string, error) {
	var (
		members = make([]string, 0)
		opt     = &redis.ZRangeBy{
			Min:    fmt.Sprintf("%f", min),
			Max:    fmt.Sprintf("%f", max),
			Offset: 0,
			Count:  int64(s.batchSize),
		}
	)

	// 分批获取
	for {
		observer := s.redis.Slave.monitorZRangeByScoreDuration.Start()
		result, err := s.redis.ZRangeByScore(s.redis.Ctx(), key, opt).Result()
		observer.Finish()
		if err != nil {
			logging.Errorf("ZRangeByScore error, key: %s, opt:%v, err: %s", key, *opt, err)
			s.redis.monitorZRangeByScore.CounterFails.Inc()
			return nil, err
		}
		s.redis.monitorZRangeByScore.CounterSuccesses.Inc()
		members = append(members, result...)
		if len(result) < s.batchSize {
			break
		}
		opt.Offset += int64(s.batchSize)
	}

	return members, nil
}

// HSetBatch : 将多个field-value paris写入到redis的hash中
// HSet命令使用文档：https://redis.io/commands/hset/
func (s *RedisStore) HSetBatch(key string, fieldValuePairs map[string]string) error {
	pairs := []interface{}{}
	for field, value := range fieldValuePairs {
		pairs = append(pairs, field, value)
	}

	// 分批写入
	pairsLen := len(pairs)
	for start := 0; start < pairsLen; start = start + s.writeSize {
		end := start + s.writeSize
		if end > pairsLen {
			end = pairsLen
		}

		observer := s.redis.Slave.monitorHSetDuration.Start()
		_, err := s.redis.HSet(s.redis.Ctx(), key, pairs[start:end]...).Result()
		observer.Finish()
		if err != nil {
			logging.Errorf("HSet error, key: %s, pairs: %v, err: %s", key, pairs, err)
			s.redis.monitorHSet.CounterFails.Inc()
			return err
		}
		s.redis.monitorHSet.CounterSuccesses.Inc()
	}

	return nil
}

// HGetBatch : 从redis的hash中获取多个fields对应的values，入参fields和返回值values的下标顺序一一对应
// HMGet命令使用文档：https://redis.io/commands/hmget/
func (s *RedisStore) HGetBatch(key string, fields []string) ([]interface{}, error) {
	result := []interface{}{}

	// 分批获取
	fieldsLen := len(fields)
	for start := 0; start < fieldsLen; start = start + s.batchSize {
		end := start + s.batchSize
		if end > fieldsLen {
			end = fieldsLen
		}

		observer := s.redis.Slave.monitorHMGetDuration.Start()
		r, err := s.redis.HMGet(s.redis.Ctx(), key, fields[start:end]...).Result()
		observer.Finish()
		if err != nil {
			logging.Errorf("HMGet error, key: %s, err: %s", key, err)
			s.redis.monitorHMGet.CounterFails.Inc()
			return nil, err
		}
		s.redis.monitorHMGet.CounterSuccesses.Inc()
		result = append(result, r...)
	}

	return result, nil
}

// Scan :
func (s *RedisStore) Scan(prefix string, callback define.StoreScanCallback, withTime ...bool) error {
	var (
		cursor        uint64 = 0
		match                = fmt.Sprintf("%s*", prefix)
		err           error
		result        []string
		withExpiresAt bool
	)
	if len(withTime) != 0 {
		withExpiresAt = withTime[0]
	}

loop:
	for {
		observer := s.redis.Slave.monitorHScanDuration.Start()
		result, cursor, err = s.redis.HScan(s.redis.Ctx(), s.cacheKey, cursor, match, int64(s.batchSize)).Result()
		observer.Finish()
		if err != nil {
			s.redis.Slave.monitorHScan.CounterFails.Inc()
			return err
		}
		s.redis.Slave.monitorHScan.CounterSuccesses.Inc()

		for i := 0; i < len(result); i = i + 2 {

			if result[i] == define.StoreFlag {
				continue
			}

			// 判断是否需要带上过期数据
			if withExpiresAt {
				if !callback(result[i], []byte(result[i+1])) {
					break loop
				}
				continue
			}

			// 返回非过期数据
			item, err := s.getItem([]byte(result[i+1]))
			if err != nil {
				return err
			}

			// 因为过期数据由另一个线程处理，此处过期不返回，跳过
			if item.IsExpired() {
				continue
			}

			data := item.GetData(false)
			if data != nil {
				if !callback(result[i], data) {
					break loop
				}
			}
		}

		if cursor == 0 {
			break loop
		}
	}
	return nil
}

// Close :
func (s *RedisStore) Close() error {
	return s.redis.Close()
}

// PutCache
func (s *RedisStore) PutCache(key string, data []byte, expires time.Duration) error {
	CacheChan <- CacheItem{key, data, expires}
	return nil
}

// Batch :
func (s *RedisStore) Batch() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	logging.Debug("store batch start")

	pipeline := s.redis.Pipeline()
	observer := s.redis.Slave.monitorHSetDuration.Start()
	defer observer.Finish()

	for k, v := range s.writeCache {
		item := define.NewStoreItem(v.data, v.expires)
		value, err := json.Marshal(item)
		if err != nil {
			return err
		}
		pipeline.HSet(s.redis.Ctx(), s.cacheKey, k, value)
	}
	cmds, err := pipeline.Exec(s.redis.Ctx())
	if err != nil {
		logging.Errorf("error exec pipeline :[%s]. cmds len: [%d]", err, len(cmds))
		s.redis.monitorHSet.CounterFails.Add(float64(len(cmds)))
		return err
	}
	s.redis.monitorHSet.CounterSuccesses.Add(float64(len(cmds)))
	// 重置writeCache
	s.writeCache = make(map[string]StoreCache)
	logging.Debug("store batch over")
	return nil
}

// getItem:
func (s *RedisStore) getItem(data []byte) (*define.StoreItem, error) {
	item := new(define.StoreItem)
	err := json.Unmarshal(data, item)
	if err != nil {
		return nil, err
	}

	return item, err
}

// clean: 清理过期的数据
func (s *RedisStore) clean() error {
	var (
		cursor      uint64 = 0
		err         error
		result      []string
		cleanFields []string
		key         string
		data        []byte
		item        *define.StoreItem
	)

loop:
	for {
		observer := s.redis.Slave.monitorHScanDuration.Start()
		result, cursor, err = s.redis.HScan(s.redis.Ctx(), s.cacheKey, cursor, "", int64(s.batchSize)).Result()
		observer.Finish()
		if err != nil {
			return err
		}

		for i := 0; i < len(result); i = i + 2 {
			key = result[i]
			// 跳过标志位
			if key == define.StoreFlag {
				continue
			}
			data = []byte(result[i+1])
			item, err = s.getItem(data)

			if err != nil {
				return err
			}

			if item.IsExpired() {
				cleanFields = append(cleanFields, key)
			}
		}

		if cursor == 0 {
			break loop
		}
	}

	logging.Debugf("clean cache length:[%d], keys: [%v]", len(cleanFields), cleanFields)

	// del fields
	fieldsLen := len(cleanFields)
	if fieldsLen > 0 {
		pipeline := s.redis.Pipeline()
		for start := 0; start < fieldsLen; start = start + s.writeSize {
			end := start + s.writeSize
			if end > fieldsLen {
				end = fieldsLen
			}

			observer := s.redis.Slave.monitorHDelDuration.Start()
			pipeline.HDel(s.redis.Ctx(), s.cacheKey, cleanFields[start:end]...)
			observer.Finish()
			_, err := pipeline.Exec(s.redis.Ctx())
			if err != nil {
				// 此处redis应该是 一条 HDel删除 很多keys，此处故意用keys的长度计数，方便transfer观察缓存的变化，删除成功同理
				s.redis.monitorHDel.CounterFails.Add(float64(len(cleanFields)))
				return err
			}
			s.redis.monitorHDel.CounterSuccesses.Add(float64(len(cleanFields)))
			// 执行完，无论对错，等待100ms，防止对redis造成太大压力
			time.Sleep(100 * time.Millisecond)
			// 清空pipeline，否则会重复执行已缓存的命令
			if err = pipeline.Discard(); err != nil {
				logging.Errorf("clean cache, pipeline discard err: %s", err)
			}
		}
	}

	s.missCachedMut.Lock()
	now := time.Now()
	for k, v := range s.missCached {
		// 1h 过期清理
		if now.Unix()-v.Unix() > 3600 {
			delete(s.missCached, k)
		}
	}
	s.missCachedMut.Unlock()

	logging.Debug("store clean expired data over")
	return nil
}

func (s *RedisStore) HotData() sync.Map {
	// 由于Map在使用后不可以复制使用，因此此处会复制新的对象出来
	var (
		newMap sync.Map
	)

	s.hotDataCache.Range(func(key, value interface{}) bool {
		newMap.Store(key, value)
		return true
	})

	return newMap
}

// ScanMemData:
func (s *RedisStore) ScanMemData(prefix string, callback define.StoreScanCallback, withAll ...bool) error {
	var (
		isWithTime bool
		err        error
	)

	if len(withAll) > 0 {
		isWithTime = withAll[0]
		logging.Debug("scan memory data with expires time")
	}

	s.hotDataCache.Range(func(key, value interface{}) bool {
		item, ok := value.(*define.StoreItem)
		if !ok {
			// 如果获取 item 不是 storeItem类型，则跳过
			return true
		}

		keyString, ok := key.(string)
		if !ok {
			return true
		}

		if !strings.HasPrefix(keyString, prefix) {
			return true
		}

		// 判断是否带过期时间返回
		if isWithTime {
			data, innerErr := json.Marshal(item)
			if innerErr != nil {
				err = innerErr

				logging.Warnf("scan memory data marshal error: %s", err)
				return false
			}
			if !callback(keyString, data) {
				logging.Warnf("scan memory data with key->[%s] callback return false, will break remain data.", keyString)
				return false
			}
			return true
		}

		if !callback(keyString, item.GetData(false)) {
			logging.Warnf("scan memory data with key->[%s] callback return false, will break remain data.", keyString)
			return false
		}

		return true
	})

	// 如果发现存在异常，需要将异常内容进行打印
	if err != nil {
		logging.Errorf("scan memory done with error->[%s]", err)
	}

	return err
}

// getAllDataIntoMem: 从 slaveRedis中拿到所有数据到内存中
func (s *RedisStore) getAllDataIntoMem() error {
	var (
		err    error
		cursor uint64 = 0
		result []string
		field  string
		cnt    int64
	)
	logging.Info("begin get all data from redis into memory")
	start := time.Now()

	for {
		observer := s.redis.Slave.monitorHScanDuration.Start()
		// redis scan命令说明：https://redis.io/commands/scan/
		// 我们设定了scan的HashKey、返回数量(Count)、未使用match，返回的Cursor循环使用
		result, cursor, err = s.redis.Slave.HScan(s.redis.Ctx(), s.cacheKey, cursor, "", int64(s.batchSize)).Result()
		observer.Finish()
		if err != nil {
			logging.Errorf("getAllDataIntoMem HScan err: %s, cacheKey: %s, cursor: %d", err, s.cacheKey, cursor)
			s.redis.Slave.monitorHScan.CounterFails.Inc()
			return err
		}
		s.redis.Slave.monitorHScan.CounterSuccesses.Inc()

		for i := 0; i < len(result); i = i + 2 {
			field = result[i]
			// 跳过标志位
			if field == define.StoreFlag {
				continue
			}
			item := new(define.StoreItem)
			err := json.Unmarshal([]byte(result[i+1]), item)
			if err != nil {
				logging.Errorf("getAllDataIntoMem Unmarshal err: %s, field: %s, value: %s", err, field, result[i+1])

				continue
			}
			s.setIntoMem(field, item)
			cnt++
		}

		if cursor == 0 {
			break
		}
	}

	logging.Infof("finish get all data from redis into memory, kv cnt: %d, cost time: %v", cnt, time.Since(start))
	return err
}

// Lock: 阻塞方法，试图获取分布式锁
func (s *RedisStore) Lock() {
	if !s.distributedLockEnabled {
		return
	}
	var (
		isSuccess bool
		cmd       *redis.BoolCmd
	)
	logging.Debugf("redis store try to get distributed lock")
	for !isSuccess {
		time.Sleep(100 * time.Millisecond)
		// 试图拿到分布式锁
		cmd = s.redis.SetNX(s.redis.Ctx(), s.distributedLockKey, s.distributedLockValue,
			s.distributedExpireDuration)
		isSuccess = cmd.Val()
	}
	logging.Debugf("redis store get distributed lock")
}

// UnLock 释放分布式锁。
// 注：此处如果释放锁失败，则只能等待锁过期自动释放。
// 重复释放不会导致panic，也不返回错误。只有释放失败会返回错误。
func (s *RedisStore) UnLock() error {
	if !s.distributedLockEnabled {
		return nil
	}
	cmd := s.redis.Get(s.redis.Ctx(), s.distributedLockKey)
	// 如果key已经不存在，可能是锁过期，或者锁过过期之后，再次被其他实例获取，再进行了解锁
	// 上面两种情况算解锁成功
	if cmd.Err() == redis.Nil {
		logging.Debugf("redis store unlock but get value:[%s]", redis.Nil)
		return nil
	}
	// 当锁信息 != 此实例的信息，说明锁过期，并且被其他实例获取到了。则也算解锁成功
	if cmd.Val() != s.distributedLockValue {
		logging.Debugf("redis store unlock but get value:[%s]", cmd.Val())
		return nil
	}
	// 解锁
	unlockCmd := s.redis.Del(s.redis.Ctx(), s.distributedLockKey)
	err := unlockCmd.Err()
	if err != nil {
		logging.Errorf("[%s] unlock distributedLock error:[%s], will wait until expired", s.distributedLockValue, err)
	} else {
		logging.Debugf("[%s] redis stroe unlock", s.distributedLockValue)
	}
	return err
}

// 检查redisStore中的数据过期, 并更新, 拉 slaveRedis数据更新
func (s *RedisStore) checkAndUpdateMemData() error {
	logging.Infof("check memory data start")

	kChan := s.checkMemData()
	// 此方法阻塞
	cmds := s.getMemData(kChan)

	logging.Debug("check and update pipeline done")

	// 遍历 cmds，将更新的数据放到内存中
	for _, c := range cmds {
		cmd := c.(*redis.StringCmd)
		args := cmd.Args()
		key := args[len(args)-1]
		result, err := cmd.Bytes()
		if err != nil {
			// 当redis的缓存数据发生变动时（有数据被删除），此时有可能会发生取某些键的值取不到的情况
			logging.Warnf("the key->[%s] get value error : %s", err, key)
			continue
		}

		// 此时取出来的result 是 storeItem类型，需要marshal，取出来 item.Data
		item := new(define.StoreItem)
		err = json.Unmarshal(result, item)
		if err != nil {
			logging.Errorf("unmarshal result:[%s] error: [%s]", string(result), err)
			continue
		}

		s.setIntoMem(key, item)
	}
	// 检查结束，则重新记录热数据。
	s.hotDataKeys = &sync.Map{}
	logging.Infof("check memory data over")
	return nil
}

// checkMemData 返回需要更新的热数据,返回一个channel
func (s *RedisStore) checkMemData() chan string {
	var (
		kChan   = make(chan string, s.writeSize)
		nowTime = time.Now()
	)
	go func() {
		defer close(kChan)
		// sync.Map 中直接delete会直接映射到map中，可以看到结果。range不会影响sync.Map正常读写
		s.hotDataCache.Range(func(key, value interface{}) bool {
			data, ok := value.(*define.StoreItem)
			if !ok {
				logging.Errorf("Incorrect value type->[%T] for key->[%s] in hotdatcache", value, key)
				s.hotDataCache.Delete(key)
				return true
			}
			// 检查数据是否有被使用: 当没有被使用,且数据过期,则从缓存中清理掉。
			_, has := s.hotDataKeys.Load(key)
			// 如果没有被使用过，则不更新
			if !has {
				// 当没有被使用,且数据过期,则从缓存中清理掉。
				if data.IsExpired() {
					logging.Infof("Delete unused data for key->[%s] in hotdatcache, nowTime->[%s], ExpiresAt->[%s]",
						key, nowTime.Format(time.RFC3339), data.ExpiresAt.Format(time.RFC3339))
					s.hotDataCache.Delete(key)
				}
				return true
			}

			kStr, ok := key.(string)
			if !ok {
				logging.Errorf("Incorrect value type->[%T] of key->[%v]", key, key)
				s.hotDataCache.Delete(key)
				return true
			}
			kChan <- kStr
			return true
		})
	}()
	return kChan
}

// updateMemData: 从channel 中接收数据并更新, 此方法阻塞，直到kChan关闭
func (s *RedisStore) getMemData(kChan chan string) []redis.Cmder {
	var (
		count     int
		pipeline  = s.redis.Pipeline()
		partSize  = s.writeSize
		totalCmds []redis.Cmder
	)
	logging.Debugf("pratSize:%d", partSize)

	for kStr := range kChan {
		count++
		observer := s.redis.Slave.monitorHGetDuration.Start()
		pipeline.HGet(s.redis.Ctx(), s.cacheKey, kStr)
		if count < partSize {
			logging.Debugf("current count: [%d]", count)
			observer.Finish()
			continue
		}
		count = 0
		logging.Debug("check and update pipeline do once part")
		cmds, err := pipeline.Exec(s.redis.Ctx())
		if err != nil {
			// 失败可能是是只失败一部分，此处为方便观察指标，观察到 Fail指标增加，即代表了HGet出错。所以指标值Add执行的keys的长度。
			s.redis.Slave.monitorHGet.CounterFails.Add(float64(len(cmds)))
			logging.Errorf("updateMemDataTask get hotDataCache error : %s", err)
		} else {
			s.redis.Slave.monitorHGet.CounterSuccesses.Add(float64(len(cmds)))
			totalCmds = append(totalCmds, cmds...)
		}
		observer.Finish()

		// 执行完，无论对错，等待500ms，防止对redis造成太大压力
		time.Sleep(500 * time.Millisecond)
		// 清空pipeline，否则会重复执行已缓存的命令
		if err = pipeline.Discard(); err != nil {
			logging.Errorf("check and updata pipeline discard err: %s", err)
		}
	}
	if count != 0 {
		if cmds, err := pipeline.Exec(s.redis.Ctx()); err != nil {
			s.redis.Slave.monitorHGet.CounterFails.Add(float64(len(cmds)))
			logging.Errorf("updateMemDataTask get hotDataCache error : %s", err)
		} else {
			s.redis.Slave.monitorHGet.CounterSuccesses.Add(float64(len(cmds)))
			totalCmds = append(totalCmds, cmds...)
		}
	}
	return totalCmds
}

// NewRedisStoreFromContext :
func NewRedisStoreFromContext(ctx context.Context) (*RedisStore, error) {
	conf := config.FromContext(ctx)
	redisType := conf.GetString(ConfRedisStorageType)
	// 获取哨兵模式配置
	masterName := conf.GetString(ConfRedisStorageMasterName)
	sentinelAddrs := conf.GetStringSlice(ConfRedisStorageSentinelAddrs)
	sentinelPassword := conf.GetString(ConfRedisStorageSentinelPasswd)
	// 获取redis实例配置
	redisAddr := fmt.Sprintf("%s:%d", conf.GetString(ConfRedisStorageHost), conf.GetInt(ConfRedisStoragePort))
	password := conf.GetString(ConfRedisStoragePassword)
	dbIndex := conf.GetInt(ConfRedisStorageDatabase)
	cacheKey := conf.GetString(ConfRedisStorageKey)
	ccCacheSize := conf.GetInt(ConfCcCacheSize)
	redisBatchSize := conf.GetInt(ConfRedisStorageBatchSize)
	expiresPeriod := conf.GetDuration(ConfSchedulerCCCacheExpires)
	// tls认证相关
	certFile := conf.GetString(ConfRedisStorageCertFile)
	keyFile := conf.GetString(ConfRedisStorageKeyFile)
	insecureSkipVerify := conf.GetBool(ConfRedisStorageInsecureSkipVerify)
	CAFile := conf.GetString(ConfRedisStorageCAFile)
	CAPath := conf.GetString(ConfRedisStorageCAPath)
	// 分布式锁相关
	lockEnable := conf.GetBool(ConfRedisStorageLockEnable)
	distributedLockKey := conf.GetString(ConfRedisStorageLockKey)
	distributedLockExpireDuration := conf.GetDuration(ConfRedisStorageLockExpire)

	enableTLS := false
	tlsConf := &tls.Config{}
	tlsConf.InsecureSkipVerify = insecureSkipVerify
	if certFile != "" && keyFile != "" {
		enableTLS = true
		tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		tlsConf.Certificates = []tls.Certificate{tlsCert}
	}

	if CAFile != "" || CAPath != "" {
		enableTLS = true
		rootConfig := &rootcerts.Config{
			CAFile: CAFile,
			CAPath: CAPath,
		}
		if err := rootcerts.ConfigureTLS(tlsConf, rootConfig); err != nil {
			logging.Errorf("load CA certs:[CAFile:%s, CAPath:%s] error:[%s]", CAFile, CAPath, err)
			return nil, err
		}
	}
	// TLS如果不启用或不完整，必须设置为nil，否则client无法正常使用
	if !enableTLS {
		tlsConf = nil
	}

	RedisStoreItem, err := NewRedisStore(redisType, masterName, redisAddr, password, cacheKey, sentinelPassword,
		sentinelAddrs, dbIndex, ccCacheSize, redisBatchSize, expiresPeriod, ctx, tlsConf, distributedLockKey,
		distributedLockExpireDuration, lockEnable)
	if err != nil {
		return nil, err
	}

	// 判断是否需要 “不”启动 同步cmdb缓存 动作
	isStopSyncData := conf.GetBool(ConfStopCcCache)
	if isStopSyncData {
		logging.Infof("stop sync data")
		cacheReady <- struct{}{}
		return RedisStoreItem, err
	}

	UpdateSignal = make(chan struct{}, 1)
	CacheChan = make(chan CacheItem, RedisStoreItem.writeSize)

	checkExpiredDataPeriod := conf.GetDuration(ConfRedisStorageCleanDataPeriod)
	if checkExpiredDataPeriod == 0 {
		checkExpiredDataPeriod = 1 * time.Hour
	}
	go UpdateRedisDataTask(RedisStoreItem, ctx, checkExpiredDataPeriod)

	checkRandomTime := conf.GetDuration(ConfRedisStorageUpdateWaitTime)
	checkPeriod := conf.GetDuration(ConfRedisStorageMemCheckPeriod)
	waitTime := conf.GetDuration(ConfRedisStorageMemWaitTime)
	// 最小不能小于设置的等待时间，否则内存缓存没有意义
	if checkPeriod < waitTime {
		checkPeriod = waitTime
		logging.Warnf("checkPeriod less than waitTime:[%s], will use the minimum checkPeriod->[waitTime]", waitTime)
	}
	// 且检查内存时间不能小于随机等待的最大时间
	if checkPeriod < checkRandomTime {
		checkPeriod = checkRandomTime
		logging.Warnf("checkPeriod less than random wait time:[%s], will use the minimum checkPeriod->[random wati time]", checkRandomTime)
	}

	// 更新内存数据
	go UpdateMemDataTask(RedisStoreItem, checkPeriod, waitTime, checkRandomTime, ctx)

	return RedisStoreItem, nil
}

// NewRedisStore:
func NewRedisStore(redisType, masterName, redisAddr, password, cacheKey, sentinelPassword string, sentinelAddrs []string,
	db, writeSize, batchSize int, expiresPeriod time.Duration, ctx context.Context, tlsConf *tls.Config,
	distributedLockKey string, distributedLockExpireDuration time.Duration, lockEnable bool,
) (*RedisStore, error) {
	var (
		client *Redis
		err    error
	)

	// 防止ctx 为nil，导致client使用命令时报错
	if ctx == nil {
		ctx = context.Background()
	}

	if redisType == RedisSentinelType {
		if len(sentinelAddrs) == 0 {
			sentinelAddrs = append(sentinelAddrs, redisAddr)
		}
		client, err = NewSentinelRedis(sentinelAddrs, sentinelPassword, password, masterName, db, ctx, tlsConf)
	} else {
		client, err = NewStandAloneRedis(redisAddr, password, db, ctx, tlsConf)
	}

	// 哨兵模式连接从节点出错时，仍然能保持可用状态。故此状态不返回, 而当连接主节点出错则直接返回
	if client == nil && err != nil {
		return nil, err
	}

	ServiceID := utils.GetServiceID(config.Configuration)
	name := config.Configuration.GetString(consul.ConfKeyServiceName)
	distributedLockValue := fmt.Sprintf("%s-%s", name, ServiceID)

	RedisStoreItem := &RedisStore{
		redis:                     client,
		cacheKey:                  cacheKey,
		writeSize:                 writeSize,
		batchSize:                 batchSize,
		writeCache:                make(map[string]StoreCache),
		hotDataKeys:               new(sync.Map),
		mu:                        new(sync.Mutex),
		opMu:                      new(sync.Mutex),
		expiresPeriod:             expiresPeriod,
		distributedLockEnabled:    lockEnable,
		distributedLockKey:        distributedLockKey,
		distributedLockValue:      distributedLockValue,
		distributedExpireDuration: distributedLockExpireDuration,
		missCached:                map[string]time.Time{},
	}
	return RedisStoreItem, nil
}

// NewSentinelRedis: 哨兵模式redis，返回一个一主一从的client
func NewSentinelRedis(sentinelAddrs []string, sentinelPassword, password, masterName string, db int,
	ctx context.Context, tlsConf *tls.Config,
) (*Redis, error) {
	var (
		opts = redis.FailoverOptions{
			MasterName:            masterName,
			SentinelAddrs:         sentinelAddrs,
			SentinelPassword:      sentinelPassword,
			UseDisconnectedSlaves: false,
			Username:              "",
			Password:              password,
			DB:                    db,
			TLSConfig:             tlsConf,
		}
		slaveOpts  = opts
		masterOpts = opts
	)

	// 创建两个client，一个用于读，一个用于写。
	masterClient, masterErr := newFailoverMonitorRedis(masterOpts, ctx)
	if masterErr != nil {
		logging.Errorf("ping master error :%s, sentinel-addrs:[%v]", masterErr, sentinelAddrs)
		return nil, masterErr
	}

	slaveOpts.SlaveOnly = true
	slaveClient, err := newFailoverMonitorRedis(slaveOpts, ctx)
	if err != nil {
		// 允许从节点错误
		logging.Errorf("ping slave error :%s, sentinel-addrs:[%v], read and write will both use master node", err, sentinelAddrs)
		slaveClient = masterClient
	}
	logging.Infof("redis connect succeed, sentinel-addrs->[%v]", sentinelAddrs)

	return &Redis{
		masterClient,
		slaveClient,
		ctx,
	}, err
}

func newFailoverMonitorRedis(options redis.FailoverOptions, ctx context.Context) (*monitorRedis, error) {
	var (
		outAddr          string
		err              error
		defaultTimeOut   = 5 * time.Second
		defaultKeepAlive = 5 * time.Minute
	)
	// 重写此方法仅仅为了拿到真实的addr
	options.Dialer = func(ctx context.Context, network, addr string) (conn net.Conn, e error) {
		outAddr = addr
		netDialer := &net.Dialer{
			Timeout:   defaultTimeOut,
			KeepAlive: defaultKeepAlive,
		}
		return netDialer.DialContext(ctx, network, addr)
	}
	client := redis.NewFailoverClient(&options)
	_, err = client.Ping(ctx).Result()
	if err != nil {
		logging.Errorf("ping redis:[%s] error :%s", outAddr, err)
		return nil, fmt.Errorf("ping redis:[%s] error:[%s]", outAddr, err)
	}
	logging.Infof("redis connect succeed, redis-addr->[%s]", outAddr)

	monitorClient := generateMonitorRedis(client, outAddr)

	return monitorClient, nil
}

// NewStandAloneRedis: 单机redis
func NewStandAloneRedis(redisAddr, password string, db int, ctx context.Context, tlsConf *tls.Config) (*Redis, error) {
	var (
		err         error
		redisClient *redis.Client
	)

	redisClient = redis.NewClient(&redis.Options{
		Addr:      redisAddr,
		Password:  password,
		DB:        db,
		TLSConfig: tlsConf,
	})

	_, err = redisClient.Ping(ctx).Result()
	if err != nil {
		logging.Errorf("ping redis:[%s] error: %s", redisAddr, err)
		return nil, err
	}

	logging.Infof("redis connect succeed, addr->[%s]", redisAddr)

	monitorClient := generateMonitorRedis(redisClient, redisAddr)

	// 单点redis，两个指针指向一个连接。
	return &Redis{
		monitorClient,
		monitorClient,
		ctx,
	}, nil
}

// generateMonitorRedis: 生成redis相关监控指标，并返回monitorRedis对象
func generateMonitorRedis(client *redis.Client, redisAddr string) *monitorRedis {
	return &monitorRedis{
		Client: client,
		monitorHScan: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "HScan"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "HScan"}),
		),
		monitorHGet: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "HGet"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "HGet"}),
		),
		monitorHMGet: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "HMGet"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "HMGet"}),
		),
		monitorHSet: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "HSet"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "HSet"}),
		),
		monitorHDel: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "HDel"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "HDel"}),
		),
		monitorZAdd: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "ZAdd"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "ZAdd"}),
		),
		monitorZRangeByScore: monitor.NewCounterMixin(
			MonitorRedisCommandSuccess.With(prometheus.Labels{"command": "ZRangeByScore"}),
			MonitorRedisCommandFail.With(prometheus.Labels{"command": "ZRangeByScore"}),
		),
		monitorHScanDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "HScan"}),
		),
		monitorHGetDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "HGet"}),
		),
		monitorHMGetDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "HMGet"}),
		),
		monitorHSetDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "HSet"}),
		),
		monitorHDelDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "HDel"}),
		),
		monitorZAddDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "ZAdd"}),
		),
		monitorZRangeByScoreDuration: monitor.NewTimeObserver(
			MonitorRedisExecuteDuration.With(prometheus.Labels{"command": "ZRangeByScore"}),
		),
	}
}

// UpdateRedisDataTask: 维护Redis中的数据
func UpdateRedisDataTask(redisStore *RedisStore, ctx context.Context, checkExpiredDataPeriod time.Duration) {
	// 是否经历过一次完整更新
	isUpdate := false
	checkExpiredTicker := time.NewTicker(checkExpiredDataPeriod)
	// 初始化缓存通道
	logging.Infof("update redis data task start")
loop:
	for {
		select {
		case item := <-CacheChan:

			redisStore.writeCache[item.key] = StoreCache{data: item.data, expires: item.expires}
			// 如果大于一定值刷入redis
			if len(redisStore.writeCache) >= redisStore.writeSize {
				if err := redisStore.Batch(); err != nil {
					logging.Errorf("batch store error : %s", err)
				}
			}
		case <-UpdateSignal:
			logging.Debug("start update store")
			// 触发更新缓存
			if err := redisStore.Batch(); err != nil {
				logging.Errorf("batch store error : %s", err)
				break
			}
			logging.Infof("The commit operation is completed")
			// 标志经历过一次完整更新
			isUpdate = true
		case <-checkExpiredTicker.C:
			// 保护一直有缓存可用，只有当完整的更新过一次后，才启动清理过期数据
			if !isUpdate {
				logging.Info("cache has not bean updated yet")
				break
			}
			logging.Info("start clean expired data")
			if err := redisStore.clean(); err != nil {
				logging.Errorf("clean store error : %s", err)
				break
			}
			isUpdate = false
		case <-ctx.Done():
			logging.Info("ctx done, store chan close")
			break loop
		}
	}
}

// UpdateMemDataTask 维护内存中的数据；会维护一个热数据的集合，当数据被使用的时候，会将键值更新到集合中
// 在清理内存数据时，如果发现热数据集合中没有这个键值，则认为数据已经不再使用，从内存中清理；
// 对于在用的数据，则会从redis中获取最新的配置值，更新到内存当中
func UpdateMemDataTask(redisStore *RedisStore, checkPeriod, waitTime, checkRandomTime time.Duration, ctx context.Context) {
	logging.Infof("update hotData cache task start")
	// 为了防止transfer对redis的并发请求过多，每个transfer实例启动后需要全量的拉取一次redis缓存数据
	initMemData(redisStore, waitTime)
	memDataCheck := time.NewTicker(checkPeriod)
	updateMemSig := make(chan struct{})
	logging.Infof("start sync memory data checkPeriod:[%s]", checkPeriod.String())
	utils.CheckError(eventbus.Subscribe(eventbus.EvSigUpdateMemCache, func(params map[string]string) {
		logging.Infof("start update memory cache")
		updateMemSig <- struct{}{}
	}))
loop:
	for {
		select {
		case <-memDataCheck.C:
			randomWaitTime := utils.RandInt(0, checkRandomTime)
			logging.Infof("check memory data after [%s]", randomWaitTime)
			time.Sleep(randomWaitTime)
			logging.Infof("check memory data")
			if err := redisStore.checkAndUpdateMemData(); err != nil {
				logging.Errorf("check and update memory data error")
			}
		case <-updateMemSig:
			// 此信号将更新 redis中的全部数据到 transfer内存
			logging.Infof("update memory data, get all data into memory")
			if err := redisStore.getAllDataIntoMem(); err != nil {
				logging.Errorf("check and update memory data error")
				continue
			}
			logging.Infof("get all data into memory success and reset check-time:[%s]", checkPeriod.String())
			// 将内存数据检查热数据的时间间隔重置。
			memDataCheck.Stop()
			memDataCheck = time.NewTicker(checkPeriod)
		case <-ctx.Done():
			logging.Info("ctx done, memDataCheck close")
			break loop
		}
	}
}

func initMemData(redisStore *RedisStore, waitTime time.Duration) {
	var (
		doneTimer = time.NewTimer(waitTime)
		startTime = time.Now()
		completed bool
		err       error
	)
	if waitTime == 0 {
		doneTimer.Stop()
	}

	// 随机生成一个 1,5 秒之间的。将时间打散，错开多个transfer同时启动对redis的压力峰值。
	checkRandTime := utils.RandInt(1*time.Second, 6*time.Second)
	checkTicker := time.NewTicker(checkRandTime)

loop:
	for {
		select {
		case <-checkTicker.C:
			// 当StoreFlag被写入redis中，代表cmdb数据的初次更新完成
			completed, err = redisStore.Exists(define.StoreFlag)
			if err != nil {
				logging.Errorf("determines whether the bootstrap_update is exist error : %s", err)
				continue
			}
			if completed {
				// 将redis中的所有数据拉取到内存中
				_ = redisStore.getAllDataIntoMem()
				break loop
			}

			checkRandTime = utils.RandInt(1*time.Second, 6*time.Second)
			logging.Warn("redis data not ready, and reset checkTime: ", checkRandTime)
			checkTicker.Stop()
			checkTicker = time.NewTicker(checkRandTime)
		case <-doneTimer.C:
			// 如果超过了一定时间，就不再继续等待cmdb数据就绪
			logging.Warnf("redis data ready time exceeds [%s]", waitTime)
			// 将redis中的所有数据拉取到内存中
			_ = redisStore.getAllDataIntoMem()
			break loop
		default:
			// pass
		}
	}
	logging.Infof("sync data from redis cost time(including the random wait time %s): %s",
		checkRandTime, time.Since(startTime))
	checkTicker.Stop()
	cacheReady <- struct{}{}
	logging.Infof("init memory data done")
}

const (
	ConfRedisStorageType               = "storage.redis.type"
	ConfRedisStorageHost               = "storage.redis.host"
	ConfRedisStoragePort               = "storage.redis.port"
	ConfRedisStoragePassword           = "storage.redis.password"
	ConfRedisStorageSentinelAddrs      = "storage.redis.sentinel_addrs"
	ConfRedisStorageSentinelPasswd     = "storage.redis.sentinel_password"
	ConfRedisStorageDatabase           = "storage.redis.database"
	ConfRedisStorageMasterName         = "storage.redis.master_name"
	ConfRedisStorageKey                = "storage.redis.cc_cache_key"      // cmdb缓存在redis中的key
	ConfRedisStorageBatchSize          = "storage.redis.batch_size"        // 指定批量操作时的数据量
	ConfRedisStorageMemCheckPeriod     = "storage.redis.mem_check_period"  // 内存数据的维护周期
	ConfRedisStorageMemWaitTime        = "storage.redis.wait_time"         // 检测cmdb缓存是否完整的最大时间，0则一直等到完成。
	ConfRedisStorageCleanDataPeriod    = "storage.redis.clean_data_period" // 定期清理redis中的过期数据，默认值最好要比更新时间要长一些。
	ConfRedisStorageUpdateWaitTime     = "storage.redis.random_wait_time"  // 更新热数据之前，随机等待的时间区间
	ConfRedisStorageCertFile           = "storage.redis.tls.cert_file"     // tsl认证信息
	ConfRedisStorageKeyFile            = "storage.redis.tls.key_file"
	ConfRedisStorageInsecureSkipVerify = "storage.redis.tls.insecure_skip_verify"
	ConfRedisStorageCAFile             = "storage.redis.tls.ca_file"
	ConfRedisStorageCAPath             = "storage.redis.tls.ca_path"
	ConfRedisStorageLockEnable         = "storage.redis.lock.enabled"
	ConfRedisStorageLockKey            = "storage.redis.lock.key"
	ConfRedisStorageLockExpire         = "storage.redis.lock.expire_duration" // 分布式锁过期时间
)

func initRedisConfiguration(c define.Configuration) {
	c.SetDefault(ConfRedisStorageType, RedisStandAloneType)
	c.SetDefault(ConfRedisStorageHost, "localhost")
	c.SetDefault(ConfRedisStoragePassword, "")
	c.SetDefault(ConfRedisStorageSentinelAddrs, []string{"127.0.0.1:26379"})
	c.SetDefault(ConfRedisStorageSentinelPasswd, "")
	c.SetDefault(ConfRedisStoragePort, 6379)
	c.SetDefault(ConfRedisStorageDatabase, 11)
	c.SetDefault(ConfRedisStorageMasterName, "mymaster")
	c.SetDefault(ConfRedisStorageKey, "bkmonitorv3.transfer.cmdb.cache")
	c.SetDefault(ConfRedisStorageBatchSize, 500)
	c.SetDefault(ConfRedisStorageMemCheckPeriod, "15m")
	c.SetDefault(ConfRedisStorageMemWaitTime, "0")
	c.SetDefault(ConfRedisStorageCleanDataPeriod, "1h")
	c.SetDefault(ConfRedisStorageCertFile, "")
	c.SetDefault(ConfRedisStorageKeyFile, "")
	c.SetDefault(ConfRedisStorageInsecureSkipVerify, false)
	c.SetDefault(ConfRedisStorageCAFile, "")
	c.SetDefault(ConfRedisStorageCAPath, "")
	c.SetDefault(ConfRedisStorageUpdateWaitTime, "10m")
	c.SetDefault(ConfRedisStorageLockKey, "bkmonitorv3.transfer.cmdb.cache.lock")
	c.SetDefault(ConfRedisStorageLockExpire, "30s") // 分布式锁过期时间
	c.SetDefault(ConfRedisStorageLockEnable, false) // 是否开启分布式锁
}

func init() {
	cacheReady = make(chan struct{}, 1)
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initRedisConfiguration))
	define.RegisterStore("redis", func(ctx context.Context, name string) (define.Store, error) {
		WaitCache = func() {
			<-cacheReady
		}
		return NewRedisStoreFromContext(ctx)
	})
}
