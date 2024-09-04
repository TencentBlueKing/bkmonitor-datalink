// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shareddiscovery

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/prometheus/discovery/targetgroup"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	gWg     sync.WaitGroup
	gCtx    context.Context
	gCancel context.CancelFunc

	sharedDiscoveryLock sync.Mutex
	sharedDiscoveryMap  map[string]*SharedDiscovery
)

// Activate 初始化全局 SharedDiscovery
func Activate() {
	gCtx, gCancel = context.WithCancel(context.Background())
	sharedDiscoveryMap = map[string]*SharedDiscovery{}
}

// Deactivate 清理全局 SharedDiscovery
func Deactivate() {
	gCancel()
	gWg.Wait()
}

// AllDiscovery 返回全局注册的 shared discovery 名称
func AllDiscovery() []string {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	names := make([]string, 0, len(sharedDiscoveryMap))
	for k := range sharedDiscoveryMap {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

type Discovery interface {
	Run(ctx context.Context, ch chan<- []*targetgroup.Group)
}

type SharedDiscovery struct {
	uk        string
	ctx       context.Context
	discovery Discovery
	ch        chan []*targetgroup.Group
	mut       sync.RWMutex
	store     map[string]*tgWithTime
	mm        *MetricMonitor
}

type tgWithTime struct {
	tg        *targetgroup.Group
	updatedAt int64
}

// FetchTargetGroups 获取缓存 targetgroups 以及最新更新时间
func FetchTargetGroups(uk string) ([]*targetgroup.Group, int64) {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	if d, ok := sharedDiscoveryMap[uk]; ok {
		return d.fetch()
	}

	return nil, 0
}

// Register 注册 shared discovery
// 共享 Discovery 实例可以减少获取 tgs 通信压力 减少内存使用
func Register(uk string, createFunc func() (*SharedDiscovery, error)) error {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	if _, ok := sharedDiscoveryMap[uk]; !ok {
		sd, err := createFunc()
		if err != nil {
			logger.Errorf("failed to create shared discovery(%s): %v", uk, err)
			return err
		}
		gWg.Add(2)
		go func() {
			defer gWg.Done()
			sd.watch()
		}()
		go func() {
			defer gWg.Done()
			sd.start()
		}()
		sharedDiscoveryMap[uk] = sd
	}

	return nil
}

func New(uk string, discovery Discovery) *SharedDiscovery {
	return &SharedDiscovery{
		ctx:       gCtx, // 生命周期由全局管理
		uk:        uk,
		discovery: discovery,
		ch:        make(chan []*targetgroup.Group),
		store:     map[string]*tgWithTime{},
		mm:        NewMetricMonitor(uk),
	}
}

func (sd *SharedDiscovery) watch() {
	sd.discovery.Run(sd.ctx, sd.ch)
}

func (sd *SharedDiscovery) start() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sd.ctx.Done():
			return

		case tgs := <-sd.ch:
			sd.mut.Lock()
			now := time.Now()
			for _, tg := range tgs {
				logger.Debugf("targetgroup %s updated at: %v", tg.Source, now)
				_, ok := sd.store[tg.Source]
				if !ok {
					// 第一次记录且没有 targets 则跳过
					if tg == nil || len(tg.Targets) == 0 {
						logger.Infof("sharedDiscovery %s skip tg source '%s'", sd.uk, tg.Source)
						continue
					}
				}
				sd.store[tg.Source] = &tgWithTime{tg: tg, updatedAt: now.Unix()}
			}
			sd.mut.Unlock()

		case <-ticker.C:
			sd.mut.Lock()
			now := time.Now().Unix()
			for source, tg := range sd.store {
				// 超过 10 分钟未更新且已经没有目标的对象需要删除
				if now-tg.updatedAt > 600 {
					if tg.tg == nil || len(tg.tg.Targets) == 0 {
						delete(sd.store, source)
						sd.mm.IncDeletedTgSourceCounter()
						logger.Infof("sharedDiscovery %s delete tg source '%s'", sd.uk, source)
					}
				}
			}
			sd.mut.Unlock()
		}
	}
}

func (sd *SharedDiscovery) fetch() ([]*targetgroup.Group, int64) {
	sd.mut.RLock()
	defer sd.mut.RUnlock()

	var maxTs int64 = math.MinInt64
	ret := make([]*targetgroup.Group, 0, 2)
	for _, v := range sd.store {
		if maxTs < v.updatedAt {
			maxTs = v.updatedAt
		}
		ret = append(ret, v.tg)
	}
	return ret, maxTs
}
