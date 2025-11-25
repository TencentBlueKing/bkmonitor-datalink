package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
	"github.com/asaskevich/EventBus"
	"github.com/dgraph-io/ristretto"
	"github.com/spf13/viper"
)

const (
	locked  = "1"
	doneMsg = "done"
)

type Service struct {
	ctx         context.Context
	localCache  *ristretto.Cache
	inflightMap sync.Map
	executeTTL  time.Duration // 执行函数的最大允许时间
	payloadTTL  time.Duration // 缓存数据的 TTL
	lockTTL     time.Duration // 分布式锁的 TTL

	sumRetry        int
	shortRetry      int
	shortTermRetry  time.Duration
	mediumTermRetry time.Duration

	closeCh chan struct{}

	bus     EventBus.Bus
	session define.Session
	enabled bool
}

func (d *Service) Stop() error {
	select {
	case d.closeCh <- struct{}{}:
	default:
	}

	if d.localCache != nil {
		d.localCache.Close()
		log.Infof(d.ctx, "local cache stopped successfully")
	}

	log.Infof(d.ctx, "cache service stopped")
	return nil
}

func (d *Service) Info(serviceType define.ServiceType) ([]*define.ServiceInfo, error) {
	infos := make([]*define.ServiceInfo, 0, 1)

	switch serviceType {
	case define.ServiceTypeMe:
		info := &define.ServiceInfo{
			ID:      "cache-service",
			Address: "localhost",
			Tags:    []string{"cache", "internal"},
			Meta: map[string]string{
				"type":        "cache",
				"enabled":     fmt.Sprintf("%v", d.enabled),
				"local_cache": "ristretto",
			},
			Detail: nil,
		}
		infos = append(infos, info)

	case define.ServiceTypeAll, define.ServiceTypeClusterAll, define.ServiceTypeLeader, define.ServiceTypeLeaderAll:
		return []*define.ServiceInfo{}, nil
	}

	return infos, nil
}

func (d *Service) Enable() error {
	if d.enabled {
		log.Infof(d.ctx, "cache service is already enabled")
		return nil
	}

	d.enabled = true
	log.Infof(d.ctx, "cache service enabled")

	if d.bus != nil {
		d.bus.Publish("service-enable")
	}

	return nil
}

func (d *Service) Disable() error {
	if !d.enabled {
		log.Infof(d.ctx, "cache service is already disabled")
		return nil
	}

	d.enabled = false
	log.Warnf(d.ctx, "cache service disabled")

	if d.bus != nil {
		d.bus.Publish("service-disable")
	}

	return nil
}

func (d *Service) Session() define.Session {
	return d.session
}

func (d *Service) EventBus() EventBus.Bus {
	return d.bus
}

func (d *Service) Type() string {
	return "cache"
}

func (d *Service) Start() error {
	return d.initialize(context.Background())
}

func (d *Service) Close() {
	if d.localCache != nil {
		d.localCache.Close()
	}

	select {
	case d.closeCh <- struct{}{}:
	default:
	}
}

func (d *Service) Reload(ctx context.Context) {
	d.Close()
	d.Wait()

	err := d.Start()
	if err != nil {
		log.Errorf(ctx, "failed to reload cache service: %v", err)
	}
}

func (d *Service) Wait() error {
	if d.localCache != nil {
		d.localCache.Wait()
	}
	return nil
}

type flightCall struct {
	wg  sync.WaitGroup
	val interface{}
	err error
}

func (d *Service) initialize(ctx context.Context) error {
	if d.localCache != nil {
		d.localCache.Close()
	}

	localCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters:        viper.GetInt64(memcache.RistrettoNumCountersPath),
		MaxCost:            viper.GetInt64(memcache.RistrettoMaxCostPath),
		BufferItems:        viper.GetInt64(memcache.RistrettoBufferItemsPath),
		IgnoreInternalCost: viper.GetBool(memcache.RistrettoIgnoreInternalCostPath),
	})
	if err != nil {
		return err
	}

	d.ctx = ctx
	d.localCache = localCache
	d.executeTTL = viper.GetDuration(http.SlowQueryThresholdConfigPath)
	d.payloadTTL = viper.GetDuration(http.ReadTimeOutConfigPath) * 2
	d.lockTTL = viper.GetDuration(http.SlowQueryThresholdConfigPath)
	d.sumRetry = viper.GetInt(http.QueryCacheSumRetryConfigPath)
	d.shortRetry = viper.GetInt(http.QueryCacheShortRetryConfigPath)
	d.shortTermRetry = viper.GetDuration(http.QueryCacheShortTermRetryConfigPath)
	d.mediumTermRetry = viper.GetDuration(http.QueryCacheMediumTermRetryConfigPath)
	d.closeCh = make(chan struct{})
	d.bus = EventBus.New()
	d.enabled = true

	return nil
}

func (d *Service) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	if value, found := d.localCache.Get(key); found {
		return value, nil
	}

	if val, ok := d.inflightMap.Load(key); ok {
		call := val.(*flightCall)
		call.wg.Wait()
		return call.val, call.err
	}

	call := &flightCall{}
	call.wg.Add(1)

	actual, loaded := d.inflightMap.LoadOrStore(key, call)
	if loaded {
		actualCall := actual.(*flightCall)
		actualCall.wg.Wait()
		return actualCall.val, actualCall.err
	}

	defer func() {
		call.wg.Done()
		d.inflightMap.Delete(key)
	}()

	result, err := d.doDistributed(d.ctx, key, fn)
	call.val = result
	call.err = err

	if err == nil {
		d.localCache.SetWithTTL(key, result, 1, d.payloadTTL)
	}

	return result, err
}

func (d *Service) doDistributed(ctx context.Context, key string, fn func() (interface{}, error)) (interface{}, error) {
	lockKey := "dsf:lock:" + key
	dataKey := "dsf:data:" + key
	channelKey := "dsf:chan:" + key
	exeKeyPrefix := "dsf:exe:" + key

	// 1. try to get from cache
	if val, err := d.getResultFromCache(ctx, dataKey); err == nil {
		return val, nil
	}

	// 2. try to acquire the distributed lock
	acquired, err := redis.SetNX(ctx, lockKey, locked, d.lockTTL)
	if err != nil {
		return nil, err
	}

	if acquired {
		// 3.1 executor case
		return d.runAndShare(ctx, exeKeyPrefix, dataKey, channelKey, lockKey, fn)
	}

	// 3.2 waiter case
	return d.waitResult(ctx, dataKey, channelKey)
}

func (d *Service) runAndShare(ctx context.Context, exeKeyPrefix, dataKey, channelKey, lockKey string, fn func() (interface{}, error)) (interface{}, error) {
	exeLockValue := fmt.Sprintf("%s:%d", exeKeyPrefix, time.Now().UnixNano())

	_, err := redis.Set(ctx, exeLockValue, exeLockValue, d.lockTTL)
	if err != nil {
		return nil, err
	}
	defer func() {
		currentVal, err := redis.Get(ctx, exeLockValue)
		if err != nil {
			log.Warnf(ctx, "failed to get exe lock value: %v", err)
			return
		}
		if currentVal == exeLockValue {
			// who set who unlock
			if _, err = redis.Delete(ctx, lockKey); err != nil {
				log.Warnf(ctx, "failed to delete exe lock value: %v", err)
				return
			}
		}
	}()

	result, err := fn()
	if err != nil {
		return nil, err
	}

	bytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	pipe := redis.TxPipeline(ctx)
	pipe.Set(ctx, dataKey, bytes, d.payloadTTL)
	pipe.Publish(ctx, channelKey, doneMsg)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	return result, nil
}

func (d *Service) waitResult(ctx context.Context, dataKey, channel string) (interface{}, error) {
	ch, closeFn := redis.Subscribe(ctx, channel)
	// before waiting, try to get from cache again
	if val, err := d.getResultFromCache(ctx, dataKey); err == nil {
		return val, nil
	}

	defer func() {
		err := closeFn()
		if err != nil {
			log.Warnf(ctx, "failed to close redis subscription: %v", err)
		}
	}()

	// wait for notification or timeout
	select {
	case msg := <-ch:
		if msg != nil {
			for i := 0; i < d.sumRetry; i++ {
				if val, err := d.getResultFromCache(ctx, dataKey); err == nil {
					return val, nil
				}
				if i < d.shortRetry {
					time.Sleep(d.shortTermRetry)
				} else {
					time.Sleep(d.mediumTermRetry)
				}
			}
		}
		return nil, errors.New("failed to get cache after notification")
	case <-time.After(d.executeTTL):
		return nil, errors.New("exceeded maximum wait time")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *Service) getResultFromCache(ctx context.Context, key string) (interface{}, error) {
	valStr, err := redis.Get(ctx, key)
	if redis.IsNil(err) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	var res interface{}
	err = json.Unmarshal([]byte(valStr), &res)
	return res, err
}

