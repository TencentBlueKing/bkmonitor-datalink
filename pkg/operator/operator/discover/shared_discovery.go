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

type Kind struct {
	id         string
	namespaces []string
	ctx        context.Context
	discovery  *promdiscover.Discovery
	ch         chan []*targetgroup.Group
	mut        sync.RWMutex
	store      map[string]*TgWithTime
}

type TgWithTime struct {
	tg        *targetgroup.Group
	updatedAt int64
}

func NewKind(ctx context.Context, id string, namespaces []string, discovery *promdiscover.Discovery) *Kind {
	return &Kind{
		ctx:        ctx,
		id:         id,
		namespaces: namespaces,
		discovery:  discovery,
		ch:         make(chan []*targetgroup.Group),
		store:      map[string]*TgWithTime{},
	}
}

func (k *Kind) Watch() {
	k.discovery.Run(k.ctx, k.ch)
}

func (k *Kind) Start() {
	for {
		select {
		case <-k.ctx.Done():
			return

		case tgs := <-k.ch:
			k.mut.Lock()
			now := time.Now()
			for _, tg := range tgs {
				logger.Debugf("targetgroup [%s] updated at: %v", tg.Source, now)
				k.store[tg.Source] = &TgWithTime{tg: tg, updatedAt: now.Unix()}
			}
			k.mut.Unlock()
		}
	}
}

func (k *Kind) Fetch() ([]*targetgroup.Group, int64) {
	k.mut.RLock()
	defer k.mut.RUnlock()

	var maxTs int64 = math.MinInt64
	ret := make([]*targetgroup.Group, 0, 2)
	for _, v := range k.store {
		if maxTs < v.updatedAt {
			maxTs = v.updatedAt
		}
		ret = append(ret, v.tg)
	}
	return ret, maxTs
}

var (
	globalWg            sync.WaitGroup
	globalCtx           context.Context
	globalCancel        context.CancelFunc
	sharedDiscoveryLock sync.Mutex
	sharedDiscoveryMap  map[string]*Kind
)

func Init() {
	globalCtx, globalCancel = context.WithCancel(context.Background())
	globalWg = sync.WaitGroup{}
	sharedDiscoveryMap = map[string]*Kind{}
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

func StopAllSharedDiscovery() {
	globalCancel()
	globalWg.Wait()
}

func getUniqueKey(role string, namespaces []string) string {
	return fmt.Sprintf("%s/%s", role, strings.Join(namespaces, "/"))
}

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
		kind := NewKind(globalCtx, uniqueKey, namespaces, discovery)
		globalWg.Add(2)
		go func() {
			defer globalWg.Done()
			kind.Watch()
		}()
		go func() {
			defer globalWg.Done()
			kind.Start()
		}()
		sharedDiscoveryMap[uniqueKey] = kind
	}
}

func GetTargetGroups(role string, namespaces []string) ([]*targetgroup.Group, int64) {
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()

	uniqueKey := getUniqueKey(role, namespaces)
	if d, ok := sharedDiscoveryMap[uniqueKey]; ok {
		return d.Fetch()
	}

	return nil, 0
}
