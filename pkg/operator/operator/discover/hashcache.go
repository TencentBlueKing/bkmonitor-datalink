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
	"sort"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/valyala/bytebufferpool"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/labelspool"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/fasttime"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var seps = []byte{'\xff'}

type hashCache struct {
	name    string
	mut     sync.Mutex
	cache   map[uint64]int64
	expired time.Duration
	done    chan struct{}
}

func newHashCache(name string, expired time.Duration) *hashCache {
	c := &hashCache{
		name:    name,
		cache:   make(map[uint64]int64),
		expired: expired,
		done:    make(chan struct{}),
	}

	go c.gc()
	return c
}

func (c *hashCache) Clean() {
	close(c.done)
}

func (c *hashCache) gc() {
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
				if now-v > secs {
					delete(c.cache, k)
					total++
				}
			}
			c.mut.Unlock()
			if total > 0 {
				logger.Infof("%s hashCache remove %d items", c.name, total)
			}

		case <-c.done:
			return
		}
	}
}

func (c *hashCache) Check(namespace string, tlset, tglbs model.LabelSet) (uint64, bool) {
	h := c.hash(namespace, tlset, tglbs)

	c.mut.Lock()
	defer c.mut.Unlock()

	_, ok := c.cache[h]
	if ok {
		c.cache[h] = fasttime.UnixTimestamp()
	}
	return h, ok
}

func (c *hashCache) Set(h uint64) {
	c.mut.Lock()
	defer c.mut.Unlock()

	c.cache[h] = fasttime.UnixTimestamp()
}

func (c *hashCache) hash(namespace string, tlset, tglbs model.LabelSet) uint64 {
	lbs := labelspool.Get()
	defer labelspool.Put(lbs)

	for k, v := range tlset {
		lbs = append(lbs, labels.Label{Name: string(k), Value: string(v)})
	}
	for k, v := range tglbs {
		lbs = append(lbs, labels.Label{Name: string(k), Value: string(v)})
	}
	sort.Sort(lbs)

	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)

	buf.WriteString(namespace)
	buf.Write(seps)
	for i := 0; i < len(lbs); i++ {
		lb := lbs[i]
		buf.WriteString(lb.Name)
		buf.Write(seps)
		buf.WriteString(lb.Value)
	}

	h := xxhash.Sum64(buf.Bytes())
	return h
}
