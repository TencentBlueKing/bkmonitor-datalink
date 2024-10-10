// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dimscache

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
	Key      string        `config:"key" mapstructure:"key"`
}

func (c *Config) Validate() bool {
	if c.URL == "" || c.Key == "" {
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

type Cache struct {
	mut    sync.RWMutex
	cache  map[string]map[string]string
	conf   *Config
	client *http.Client
	done   chan struct{}
	synced atomic.Bool
}

func New(conf *Config) (*Cache, error) {
	if conf == nil || !conf.Validate() {
		return nil, fmt.Errorf("invalid config %#v", conf)
	}

	tr := &http.Transport{
		MaxIdleConns:    10,
		IdleConnTimeout: time.Minute * 5,
	}

	return &Cache{
		cache: make(map[string]map[string]string),
		conf:  conf,
		done:  make(chan struct{}),
		client: &http.Client{
			Transport: tr,
			Timeout:   conf.Timeout,
		},
	}, nil
}

func (c *Cache) loopSync() {
	ticker := time.NewTicker(c.conf.Interval)
	defer ticker.Stop()

	fn := func() {
		start := time.Now()
		if err := c.sync(); err != nil {
			logger.Errorf("failed to sync (%s) cache: %v", c.conf.URL, err)
			return
		}
		logger.Debugf("sync (%s) cache take %v", c.conf.URL, time.Since(start))
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

func (c *Cache) Clean() {
	close(c.done)
}

func (c *Cache) Sync() {
	if c.synced.CompareAndSwap(false, true) {
		go c.loopSync()
	}
}

func (c *Cache) Get(k string) (map[string]string, bool) {
	c.mut.RLock()
	defer c.mut.RUnlock()

	v, ok := c.cache[k]
	return v, ok
}

func (c *Cache) sync() error {
	req, err := http.NewRequest(http.MethodGet, c.conf.URL, &bytes.Buffer{})
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

	var dims []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &dims); err != nil {
		return err
	}
	logger.Debugf("cache (%s) load %d items", c.conf.URL, len(dims))

	newCache := make(map[string]map[string]string)
	for i := 0; i < len(dims); i++ {
		dim := dims[i]
		newCache[dim[c.conf.Key]] = dim
	}

	c.mut.Lock()
	c.cache = newCache
	c.mut.Unlock()

	return nil
}
