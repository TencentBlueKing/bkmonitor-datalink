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
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	gWg     sync.WaitGroup
	gCtx    context.Context
	gCancel context.CancelFunc

	sharedDiscoveryLock sync.Mutex
	sharedDiscoveryMap  map[string]*SharedDiscovery
	sharedDiscoveryRefs map[string]int // 记录 shared discover 持有引用数
)

// Activate 初始化全局 SharedDiscovery
func Activate() {
	gCtx, gCancel = context.WithCancel(context.Background())
	sharedDiscoveryMap = map[string]*SharedDiscovery{}
	sharedDiscoveryRefs = map[string]int{}
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

type WrapTargetGroup struct {
	// Targets is a list of targets identified by a label set. Each target is
	// uniquely identifiable in the group by its address label.
	Targets []labels.Labels
	// Labels is a set of labels that is common across all targets in the group.
	Labels labels.Labels

	// Source is an identifier that describes a group of targets.
	Source string
}

// castTg 转换 *targetgroup.Group 到 *WrapTargetGroup
// 但需要确保所有 labels/targets 已经排序
func castTg(tg *targetgroup.Group) *WrapTargetGroup {
	targets := make([]labels.Labels, 0, len(tg.Targets))
	for _, target := range tg.Targets {
		tgLbs := make(labels.Labels, 0, len(target))
		for k, v := range target {
			tgLbs = append(tgLbs, labels.Label{
				Name:  string(k),
				Value: string(v),
			})
		}
		sort.Sort(tgLbs)
		targets = append(targets, tgLbs)
	}

	lbs := make(labels.Labels, 0, len(tg.Labels))
	for k, v := range tg.Labels {
		lbs = append(lbs, labels.Label{
			Name:  string(k),
			Value: string(v),
		})
	}
	sort.Sort(lbs)

	return &WrapTargetGroup{
		Targets: targets,
		Labels:  lbs,
		Source:  tg.Source,
	}
}

type Discovery interface {
	Run(ctx context.Context, ch chan<- []*targetgroup.Group)
	Stop()
}

type SharedDiscovery struct {
	ctx       context.Context
	cancel    context.CancelFunc
	uk        string
	discovery Discovery
	ch        chan []*targetgroup.Group
	mut       sync.RWMutex
	store     map[string]*tgWithTime
	mm        *MetricMonitor
}

type tgWithTime struct {
	tg        *WrapTargetGroup
	updatedAt int64
}

// FetchTargetGroups 获取缓存 targetgroups
func FetchTargetGroups(uk string) []*WrapTargetGroup {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	if d, ok := sharedDiscoveryMap[uk]; ok {
		return d.fetch()
	}

	return nil
}

// FetchTargetGroupsUpdatedAt 获取缓存最新更新时间
func FetchTargetGroupsUpdatedAt(uk string) int64 {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	if d, ok := sharedDiscoveryMap[uk]; ok {
		return d.fetchUpdatedAt()
	}

	return 0
}

// Register 注册 shared discovery
// 共享 Discovery 实例可以减少获取 tgs 通信压力 减少内存使用
func Register(uk string, createFunc func() (*SharedDiscovery, error)) error {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	sharedDiscoveryRefs[uk]++
	if _, ok := sharedDiscoveryMap[uk]; !ok {
		sd, err := createFunc()
		if err != nil {
			logger.Errorf("failed to create shared discovery (%s): %v", uk, err)
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

// Unregister 解注册 shared discovery
func Unregister(uk string) {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	n, ok := sharedDiscoveryRefs[uk]
	if !ok || n <= 0 {
		return
	}

	n--
	// 没有任何 discover 持有 则需要清理
	if n == 0 {
		if d, ok := sharedDiscoveryMap[uk]; ok {
			d.stop()
		}
		delete(sharedDiscoveryRefs, uk)
		delete(sharedDiscoveryMap, uk)
		logger.Infof("cleanup sharedDiscovery '%s'", uk)
	} else {
		sharedDiscoveryRefs[uk] = n
	}
}

func New(uk string, discovery Discovery) *SharedDiscovery {
	ctx, cancel := context.WithCancel(gCtx)
	return &SharedDiscovery{
		ctx:       ctx,
		cancel:    cancel,
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

func (sd *SharedDiscovery) stop() {
	sd.cancel()
	sd.discovery.Stop()
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
				if tg == nil {
					continue
				}

				logger.Debugf("targetgroup %s updated at: %v", tg.Source, now)
				_, ok := sd.store[tg.Source]
				if !ok && len(tg.Targets) == 0 {
					// 第一次记录且没有 targets 则跳过
					logger.Debugf("sharedDiscovery %s skip tg source '%s'", sd.uk, tg.Source)
					continue
				}
				sd.store[tg.Source] = &tgWithTime{tg: castTg(tg), updatedAt: now.Unix()}
			}
			sd.mut.Unlock()

		case <-ticker.C:
			sd.mut.Lock()
			now := time.Now().Unix()

			var total int
			for source, tgt := range sd.store {
				// 超过 10 分钟未更新且已经没有目标的对象需要删除
				// 确保 basediscovery 已经处理了删除事件
				if now-tgt.updatedAt > 600 && len(tgt.tg.Targets) == 0 {
					delete(sd.store, source)
					sd.mm.IncDeletedTgSourceCounter()
					logger.Infof("sharedDiscovery %s delete tg source '%s'", sd.uk, source)
				} else {
					total += len(tgt.tg.Targets)
				}
			}
			sd.mm.SetTargetCount(total)
			sd.mut.Unlock()
		}
	}
}

func (sd *SharedDiscovery) fetch() []*WrapTargetGroup {
	sd.mut.RLock()
	defer sd.mut.RUnlock()

	ret := make([]*WrapTargetGroup, 0, len(sd.store))
	for _, v := range sd.store {
		ret = append(ret, v.tg)
	}
	return ret
}

func (sd *SharedDiscovery) fetchUpdatedAt() int64 {
	sd.mut.RLock()
	defer sd.mut.RUnlock()

	var maxTs int64 = math.MinInt64
	for _, v := range sd.store {
		if maxTs < v.updatedAt {
			maxTs = v.updatedAt
		}
	}
	return maxTs
}
