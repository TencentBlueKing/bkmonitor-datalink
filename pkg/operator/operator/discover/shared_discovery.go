// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	promdiscover "github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/prometheus/prometheus/discovery/targetgroup"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/logconf"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	gWg     sync.WaitGroup
	gCtx    context.Context
	gCancel context.CancelFunc

	sharedDiscoveryLock sync.Mutex
	sharedDiscoveryMap  map[string]*sharedDiscovery
)

// Activate 初始化全局 sharedDiscovery
func Activate() {
	gCtx, gCancel = context.WithCancel(context.Background())
	sharedDiscoveryMap = map[string]*sharedDiscovery{}
}

// Deactivate 清理全局 sharedDiscovery
func Deactivate() {
	gCancel()
	gWg.Wait()
}

type sharedDiscovery struct {
	id         string
	namespaces []string
	ctx        context.Context
	discovery  *promdiscover.Discovery
	ch         chan []*targetgroup.Group
	mut        sync.RWMutex
	store      map[string]*tgWithTime
	mm         *metricMonitor
}

type tgWithTime struct {
	tg        *targetgroup.Group
	updatedAt int64
}

func newSharedDiscovery(ctx context.Context, id string, namespaces []string, discovery *promdiscover.Discovery) *sharedDiscovery {
	return &sharedDiscovery{
		ctx:        ctx,
		id:         id,
		namespaces: namespaces,
		discovery:  discovery,
		ch:         make(chan []*targetgroup.Group),
		store:      map[string]*tgWithTime{},
		mm:         newMetricMonitor(id),
	}
}

func (sd *sharedDiscovery) watch() {
	sd.discovery.Run(sd.ctx, sd.ch)
}

func (sd *sharedDiscovery) start() {
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
						logger.Infof("sharedDiscovery %s skip tg source '%s'", sd.id, tg.Source)
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
						logger.Infof("sharedDiscovery %s delete tg source '%s'", sd.id, source)
					}
				}
			}
			sd.mut.Unlock()
		}
	}
}

func (sd *sharedDiscovery) fetch() ([]*targetgroup.Group, int64) {
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

type SharedDiscoveryInfo struct {
	Role       string   `json:"role"`
	Namespaces []string `json:"namespaces"`
}

func (si SharedDiscoveryInfo) ID() string {
	return fmt.Sprintf("%s/%s", si.Role, strings.Join(si.Namespaces, "/"))
}

func GetActiveSharedDiscovery() []SharedDiscoveryInfo {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	info := make([]SharedDiscoveryInfo, 0)
	for k := range sharedDiscoveryMap {
		parts := strings.Split(k, "/")
		info = append(info, SharedDiscoveryInfo{
			Role:       parts[0],
			Namespaces: parts[1:],
		})
	}
	return info
}

func GetSharedDiscoveryCount() int {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	return len(sharedDiscoveryMap)
}

func getUniqueKey(role string, namespaces []string) string {
	return fmt.Sprintf("%s/%s", role, strings.Join(namespaces, "/"))
}

// RegisterSharedDiscover 注册 sharedDiscovery
// 共享 Discovery 实例可以减少 API Server 请求压力 同时也可以减少进程内存开销
func RegisterSharedDiscover(role, kubeConfig string, namespaces []string) {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	uniqueKey := getUniqueKey(role, namespaces)
	if _, ok := sharedDiscoveryMap[uniqueKey]; !ok {
		cfg := promdiscover.DefaultSDConfig
		cfg.Role = promdiscover.Role(role)
		cfg.NamespaceDiscovery.Names = namespaces
		cfg.KubeConfig = kubeConfig

		discovery, err := promdiscover.New(new(logconf.Logger), &cfg)
		if err != nil {
			logger.Errorf("failed to create promdiscover: %v", err)
			return
		}
		sd := newSharedDiscovery(gCtx, uniqueKey, namespaces, discovery)
		gWg.Add(2)
		go func() {
			defer gWg.Done()
			sd.watch()
		}()
		go func() {
			defer gWg.Done()
			sd.start()
		}()
		sharedDiscoveryMap[uniqueKey] = sd
	}
}

func GetTargetGroups(role string, namespaces []string) ([]*targetgroup.Group, int64) {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	uniqueKey := getUniqueKey(role, namespaces)
	if d, ok := sharedDiscoveryMap[uniqueKey]; ok {
		return d.fetch()
	}

	return nil, 0
}
