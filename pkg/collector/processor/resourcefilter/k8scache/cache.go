// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package k8scache

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Config struct {
	URL      string        `config:"url" mapstructure:"url"`
	Timeout  time.Duration `config:"timeout" mapstructure:"timeout"`
	Interval time.Duration `config:"interval" mapstructure:"interval"`
}

func (c *Config) Validate() bool {
	if c.URL == "" {
		return false
	}

	if c.Timeout <= 0 {
		c.Timeout = time.Minute
	}
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	return true
}

type Cache interface {
	Sync()
	Clean()
	Get(k string) (map[string]string, bool)
}

type noneCache struct{}

func (noneCache) Sync() {}

func (noneCache) Clean() {}

func (noneCache) Get(_ string) (map[string]string, bool) {
	return nil, false
}

type innerCache struct {
	mut    sync.RWMutex
	cache  map[string]map[string]string
	conf   *Config
	client *http.Client
	done   chan struct{}
	lastRv int
	synced atomic.Bool
}

func New(conf *Config) Cache {
	if conf == nil || !conf.Validate() {
		return noneCache{} // 如果配置不合法返回一个空实现
	}

	tr := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     time.Minute * 5,
	}

	return &innerCache{
		cache: make(map[string]map[string]string),
		conf:  conf,
		done:  make(chan struct{}),
		client: &http.Client{
			Transport: tr,
			Timeout:   conf.Timeout,
		},
	}
}

func (c *innerCache) loopSync() {
	ticker := time.NewTicker(c.conf.Interval)
	defer ticker.Stop()

	fn := func() {
		start := time.Now()
		if err := c.sync(); err != nil {
			logger.Errorf("failed to sync (%s) innerCache: %v", c.conf.URL, err)
			return
		}
		logger.Debugf("sync (%s) innerCache take %v", c.conf.URL, time.Since(start))
	}

	fn() // 启动即同步

	for {
		select {
		case <-c.done:
			return

		case <-ticker.C:
			fn()
		}
	}
}

func (c *innerCache) Clean() {
	close(c.done)
}

func (c *innerCache) Sync() {
	if c.synced.CompareAndSwap(false, true) {
		go c.loopSync()
	}
}

func (c *innerCache) Get(k string) (map[string]string, bool) {
	c.mut.RLock()
	defer c.mut.RUnlock()

	v, ok := c.cache[k]
	return v, ok
}

type podObject struct {
	Action    string `json:"action"`
	ClusterID string `json:"cluster"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	IP        string `json:"ip"`
}

type response struct {
	ResourceVersion int         `json:"resourceVersion"`
	Pods            []podObject `json:"pods"`
}

func (c *innerCache) sync() error {
	url := c.conf.URL + fmt.Sprintf("?resourceVersion=%d", c.lastRv)
	logger.Debugf("innercache request url: %s", url)

	req, err := http.NewRequest(http.MethodGet, url, &bytes.Buffer{})
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, resp.Body)
	if err != nil {
		return err
	}

	var ret response
	if err := json.Unmarshal(buf.Bytes(), &ret); err != nil {
		return err
	}
	c.lastRv = ret.ResourceVersion

	c.mut.Lock()
	defer c.mut.Unlock()

	for _, pod := range ret.Pods {
		switch pod.Action {
		case "Delete":
			delete(c.cache, pod.IP)

		case "CreateOrUpdate":
			c.cache[pod.IP] = map[string]string{
				"k8s.bcs.cluster.id": pod.ClusterID,
				"k8s.pod.name":       pod.Name,
				"k8s.namespace.name": pod.Namespace,
				"k8s.pod.ip":         pod.IP,
			}
		}
	}
	return nil
}
