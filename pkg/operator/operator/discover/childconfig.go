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
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/fasttime"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ChildConfig 子任务配置文件信息
type ChildConfig struct {
	Meta         define.MonitorMeta
	Node         string
	FileName     string
	Address      string
	Data         []byte
	Scheme       string
	Path         string
	Mask         string
	TaskType     string
	Namespace    string
	AntiAffinity bool
}

func (c ChildConfig) String() string {
	return fmt.Sprintf("Node=%s, FileName=%s, Address=%s", c.Node, c.FileName, c.Address)
}

func (c ChildConfig) Hash() uint64 {
	h := fnv.New64a()
	h.Write([]byte(c.Node))
	h.Write(c.Data)
	h.Write([]byte(c.Mask))
	return h.Sum64()
}

type childConfigWithTime struct {
	config  *ChildConfig
	updated int64
}

type childConfigCache struct {
	name    string
	mut     sync.Mutex
	cache   map[uint64]*childConfigWithTime
	expired time.Duration
	done    chan struct{}
}

func newChildConfigCache(name string, expired time.Duration) *childConfigCache {
	c := &childConfigCache{
		name:    name,
		cache:   make(map[uint64]*childConfigWithTime),
		expired: expired,
		done:    make(chan struct{}),
	}

	go c.gc()
	return c
}

func (c *childConfigCache) Get(h uint64) (*ChildConfig, bool) {
	c.mut.Lock()
	defer c.mut.Unlock()

	v, ok := c.cache[h]
	if ok {
		c.cache[h].updated = fasttime.UnixTimestamp()
		return v.config, true
	}
	return nil, false
}

func (c *childConfigCache) Set(h uint64, config *ChildConfig) {
	c.mut.Lock()
	defer c.mut.Unlock()

	c.cache[h] = &childConfigWithTime{
		config:  config,
		updated: fasttime.UnixTimestamp(),
	}
}

func (c *childConfigCache) Clean() {
	close(c.done)
}

func (c *childConfigCache) gc() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now().Unix()
			secs := int64(c.expired.Seconds())
			var total int
			c.mut.Lock()
			for k, v := range c.cache {
				if now-v.updated > secs {
					delete(c.cache, k)
					total++
				}
			}
			c.mut.Unlock()
			if total > 0 {
				logger.Infof("%s childConfigCache remove %d items", c.name, total)
			}

		case <-c.done:
			return
		}
	}
}
