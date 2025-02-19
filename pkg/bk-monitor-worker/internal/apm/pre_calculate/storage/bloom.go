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
	"crypto/md5"
	"math"
	"sync"
	"time"

	redisBloom "github.com/RedisBloom/redisbloom-go"
	qf "github.com/facebookincubator/go-qfext"
	"github.com/gomodule/redigo/redis"
	"github.com/minio/highwayhash"
	boom "github.com/tylertreat/BoomFilters"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BloomStorageData storage request of bloom-filter
type BloomStorageData struct {
	DataId string
	Key    string
}

// BloomOperator interface of bloom-filter
type BloomOperator interface {
	Add(BloomStorageData) error
	Exist(string) (bool, error)
}

// BloomOptions config of bloom-filter
type BloomOptions struct {
	FpRate float64 `json:"fpRate"`

	NormalMemoryBloomOptions    MemoryBloomOptions `json:"normalMemoryBloomConfig"`
	NormalMemoryQuotientOptions QuotientFilterOptions

	NormalOverlapBloomOptions     OverlapBloomOptions           `json:"normalOverlapBloomConfig"`
	LayersBloomOptions            LayersBloomOptions            `json:"layersBloomConfig"`
	LayersCapDecreaseBloomOptions LayersCapDecreaseBloomOptions `json:"layersCapDecreaseBloomConfig"`
}

type RedisNormalBloom struct {
	filterName string
	config     BloomOptions
	c          *redisBloom.Client
}

func (b *RedisNormalBloom) Add(data BloomStorageData) error {
	_, err := b.c.Add(b.filterName, data.Key)
	return err
}

func (b *RedisNormalBloom) Exist(k string) (bool, error) {
	return b.c.Exists(b.filterName, k)
}

func newRedisBloomClient(rConfig RedisCacheOptions, opts BloomOptions) (BloomOperator, error) {
	pool := &redis.Pool{Dial: func() (redis.Conn, error) {
		return redis.Dial("tcp", rConfig.Host, redis.DialPassword(rConfig.Password), redis.DialDatabase(rConfig.Db))
	}}
	c := redisBloom.NewClientFromPool(pool, "bloom-client")

	return &RedisNormalBloom{filterName: "traceMeta", config: opts, c: c}, nil
}

type MemoryBloomOptions struct {
	AutoClean time.Duration `json:"autoClean"`
}

type MemoryBloom struct {
	config        MemoryBloomOptions
	c             boom.Filter
	nextCleanDate time.Time
	cleanDuration time.Duration
	resetFunc     func()
	logger        monitorLogger.Logger
}

func (m *MemoryBloom) Add(data []byte) boom.Filter {
	return m.c.Add(data)
}

func (m *MemoryBloom) Test(key []byte) bool {
	return m.c.Test(key)
}

func (m *MemoryBloom) TestAndAdd(key []byte) bool {
	return m.c.TestAndAdd(key)
}

func (m *MemoryBloom) AutoReset() {
	// Prevent the memory from being too large.
	// Data will be cleared after a specified time.
	m.logger.Infof("RedisNormalBloom-filter will reset every %s", m.config.AutoClean)
	for {
		if time.Now().After(m.nextCleanDate) {
			m.resetFunc()
			m.nextCleanDate = time.Now().Add(m.cleanDuration)
			m.logger.Infof("RedisNormalBloom-filter reset data trigger, next time the filter reset data is %s", m.nextCleanDate)
		}
		time.Sleep(1 * time.Minute)
	}
}

// newBloomClient base on boom.Filter, support for outer filter as param
func newBloomClient(f boom.Filter, resetFunc func(), options BloomOptions) boom.Filter {
	bloom := &MemoryBloom{
		c:             f,
		config:        options.NormalMemoryBloomOptions,
		nextCleanDate: time.Now().Add(options.NormalMemoryBloomOptions.AutoClean),
		cleanDuration: options.NormalMemoryBloomOptions.AutoClean,
		resetFunc:     resetFunc,
		logger:        monitorLogger.With(zap.String("name", "memoryBloom")),
	}
	go bloom.AutoReset()
	return bloom
}

type QuotientFilterOption func(*QuotientFilterOptions)

type QuotientFilterOptions struct {
	MagnitudePerMin int
}

type MemoryQuotientFilter struct {
	c      *qf.Filter
	config QuotientFilterOptions
}

func (f *MemoryQuotientFilter) Add(data []byte) boom.Filter {
	f.c.Insert(data)
	return f
}

func (f *MemoryQuotientFilter) Test(data []byte) bool {
	return f.c.Contains(data)
}

func (f *MemoryQuotientFilter) TestAndAdd(data []byte) bool {
	// unsafe
	res := f.c.Contains(data)
	f.c.Insert(data)
	return res
}

func newQuotientFilter(fpRate float64, resetDuration time.Duration, options QuotientFilterOptions) boom.Filter {
	exceptEntries := options.MagnitudePerMin * int(resetDuration.Minutes())
	perEntry := uint(math.Ceil(-math.Log2(fpRate) / 0.75))

	f := qf.NewWithConfig(qf.Config{
		BitsOfStoragePerEntry: perEntry,
		BitPacked:             true,
		ExpectedEntries:       uint64(exceptEntries),
	})
	return &MemoryQuotientFilter{c: f, config: options}
}

type OverlapBloomOptions struct {
	ResetDuration time.Duration `json:"resetDuration"`
}

type OverlapBloomOption func(*OverlapBloomOptions)

type BloomChain struct {
	front boom.Filter
	after boom.Filter
}

// OverlapBloom time-overlap bloom, base on boom.Filter
type OverlapBloom struct {
	dataId string

	bloomChain BloomChain
	cap        uint
	fpRate     float64

	resetDuration time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	lock          sync.Mutex
	logger        monitorLogger.Logger
}

func (m *OverlapBloom) Add(data []byte) boom.Filter {
	m.bloomChain.front.Add(data)
	if m.bloomChain.after != nil {
		m.bloomChain.after.Add(data)
	}
	return m
}

func (m *OverlapBloom) Test(key []byte) bool {
	return m.bloomChain.front.Test(key)
}

func (m *OverlapBloom) TestAndAdd(key []byte) bool {
	r := m.bloomChain.front.TestAndAdd(key)
	if m.bloomChain.after != nil {
		m.bloomChain.after.TestAndAdd(key)
	}
	return r
}

func (m *OverlapBloom) AddOverlap() {

	intervalTicker := time.NewTicker(m.resetDuration / 2)
	m.logger.Infof("overlap bloom add overlap interval: %s", m.resetDuration/2)

	for {
		select {
		case <-intervalTicker.C:
			m.logger.Debugf("add overlap trigger")
			for {
				m.lock.Lock()
				m.logger.Debugf("add overlap get lock")
				if m.bloomChain.after != nil {
					m.logger.Debugf("add overlap release lock via after not null")
					m.lock.Unlock()
					time.Sleep(time.Second)
					continue
				}
				m.bloomChain.after = boom.NewBloomFilter(m.cap, m.fpRate)
				m.logger.Infof("add overlap release lock，after is created")
				// changed to interleaved execution
				intervalTicker = time.NewTicker(m.resetDuration)
				m.lock.Unlock()
				break
			}
		case <-m.ctx.Done():
			intervalTicker.Stop()
			return
		}
	}
}

func (m *OverlapBloom) AutoReset() {
	intervalTicker := time.NewTicker(m.resetDuration)
	m.logger.Infof("overlap bloom reset interval: %s", m.resetDuration)

	for {
		select {
		case <-intervalTicker.C:
			m.logger.Debugf("auto reset trigger")
			m.lock.Lock()
			m.logger.Debugf("auto reset get lock")
			m.bloomChain.front = m.bloomChain.after
			m.bloomChain.after = nil
			m.logger.Infof("auto reset release lock, move after to front, set after = null dataId: %s", m.dataId)
			m.lock.Unlock()
		case <-m.ctx.Done():
			intervalTicker.Stop()
			return
		}
	}
}

func (m *OverlapBloom) Close() {
	m.cancel()
}

func newOverlapBloomClient(dataId string, ctx context.Context, f boom.Filter, cap uint, fpRate float64, resetDuration time.Duration) boom.Filter {
	childCtx, childCancel := context.WithCancel(ctx)
	bloom := OverlapBloom{
		dataId:        dataId,
		bloomChain:    BloomChain{front: f},
		resetDuration: resetDuration,
		cap:           cap,
		fpRate:        fpRate,
		ctx:           childCtx,
		cancel:        childCancel,
		logger:        monitorLogger.With(zap.String("name", "overlapBloom"), zap.String("dataId", dataId)),
	}

	go bloom.AddOverlap()
	go bloom.AutoReset()

	return &bloom
}

type LayersBloomOptions struct {
	Layers int `json:"layers"`
}

type layerStrategy func(string) []byte

var (
	strategies = []layerStrategy{
		// truncated 16
		func(s string) []byte {
			return []byte(s[16:])
		},
		// truncated 8
		func(s string) []byte {
			return []byte(s[24:])
		},
		// full
		func(s string) []byte {
			return []byte(s)
		},
		// md5
		func(s string) []byte {
			hash := md5.New()
			hash.Write([]byte(s))
			return hash.Sum(nil)
		},
		// hash
		func(s string) []byte {
			h, _ := highwayhash.New([]byte(config.HashSecret))
			h.Write([]byte(s))
			return h.Sum(nil)
		},
	}
)

type LayersMemoryBloom struct {
	blooms     []boom.Filter
	strategies []layerStrategy
	logger     monitorLogger.Logger
}

func (l *LayersMemoryBloom) Add(data BloomStorageData) error {
	for index, b := range l.blooms {
		key := l.strategies[index](data.Key)
		if err := b.Add(key); err != nil {
			l.logger.Errorf("failed to add data in blooms[%d]. error: %s", index, err)
		}

	}
	return nil
}

func (l *LayersMemoryBloom) Exist(originKey string) (bool, error) {

	for index, b := range l.blooms {
		key := l.strategies[index](originKey)
		e := b.Test(key)
		if !e {
			return false, nil
		}
	}

	return true, nil
}

func newLayersBloomClient(options BloomOptions) (BloomOperator, error) {
	var blooms []boom.Filter

	for i := 0; i < options.LayersBloomOptions.Layers; i++ {
		sbf := boom.NewScalableBloomFilter(uint(options.LayersBloomOptions.Layers), options.FpRate, 0.8)
		bloom := newBloomClient(sbf, func() { sbf.Reset() }, options)
		blooms = append(blooms, bloom)
	}
	monitorLogger.Infof("bloom-filter Layers: %d", options.LayersBloomOptions.Layers)
	return &LayersMemoryBloom{
		blooms:     blooms,
		strategies: strategies,
		logger:     monitorLogger.With(zap.String("name", "layerBloomFilter")),
	}, nil
}

type LayersCapDecreaseBloomOptions struct {
	Cap     int `json:"cap"`
	Layers  int `json:"layers"`
	Divisor int `json:"divisor"`
}

// LayersCapDecreaseOverlapBloom Layers + overlap filter.
// It is optional to choice base-filter: boom.BloomFilter or QuotientFilter
type LayersCapDecreaseOverlapBloom struct {
	blooms []boom.Filter
}

func (l *LayersCapDecreaseOverlapBloom) Add(data BloomStorageData) error {
	key := []byte(data.Key)
	for _, b := range l.blooms {
		b.Add(key)
	}
	return nil
}

func (l *LayersCapDecreaseOverlapBloom) Exist(originKey string) (bool, error) {
	key := []byte(originKey)

	for _, b := range l.blooms {
		exist := b.Test(key)
		if !exist {
			return false, nil
		}
	}

	return true, nil
}

func newLayersCapDecreaseBloomClient(dataId string, ctx context.Context, options BloomOptions) (BloomOperator, error) {
	var blooms []boom.Filter

	curCap := options.LayersCapDecreaseBloomOptions.Cap
	for i := 0; i < options.LayersCapDecreaseBloomOptions.Layers; i++ {
		sbf := boom.NewDefaultStableBloomFilter(uint(curCap), options.FpRate)
		// select overlapBloom as super stratum
		bloom := newOverlapBloomClient(
			dataId, ctx, sbf, uint(curCap), options.FpRate, options.NormalOverlapBloomOptions.ResetDuration,
		)
		blooms = append(blooms, bloom)
		curCap = curCap / options.LayersCapDecreaseBloomOptions.Divisor
	}

	return &LayersCapDecreaseOverlapBloom{blooms: blooms}, nil
}
