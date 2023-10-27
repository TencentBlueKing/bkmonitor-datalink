// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build !aix
// +build !aix

package storage

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rapidloop/skv"
)

// LocalStorage
type LocalStorage struct {
	path   string
	store  *skv.KVStore
	cancel context.CancelFunc

	// skv 底层使用boltdb存储，每秒大约存储5K左右条数据，如果有频繁的读写相同的数据，需要增加缓存
	cacheMutex sync.Mutex
	cache      map[string]string // key <--> value

	// flush
	flushInterval time.Duration
}

// New : skv storage
func NewLocalStorage(path string) (*LocalStorage, error) {
	// 判断目录是否存在
	parentDirs := filepath.Dir(path)
	_, err := os.Stat(parentDirs)
	if err != nil {
		if !os.IsExist(err) {
			os.MkdirAll(parentDirs, 0o755)
		}
	}

	local, err := skv.Open(path)

	storage := &LocalStorage{
		store: local,
		path:  path,
		cache: make(map[string]string),

		flushInterval: 1 * time.Second,
	}

	// flush every second
	ctx, cancel := context.WithCancel(context.Background())
	storage.cancel = cancel
	go func(ctx context.Context) {
		interval := storage.flushInterval
		ticker := time.NewTicker(interval)
		for {
			select {
			case <-ticker.C:
				if interval != storage.flushInterval {
					interval = storage.flushInterval

					ticker.Stop()
					ticker = time.NewTicker(interval)
				}
				storage.Flush()
			case <-ctx.Done():
				return
			}
		}
	}(ctx)

	return storage, err
}

// SetFlushInterval : set flush interval
func (cli *LocalStorage) SetFlushInterval(interval time.Duration) {
	if cli.flushInterval != interval && interval > (1*time.Second) {
		cli.flushInterval = interval
	}
}

// Close : close db
func (cli *LocalStorage) Close() error {
	cli.cancel()
	cli.Flush()
	return cli.store.Close()
}

// Set : set value
func (cli *LocalStorage) Set(key, value string, expire time.Duration) error {
	cli.cacheMutex.Lock()
	defer cli.cacheMutex.Unlock()

	cli.cache[key] = value
	return nil
	// return cli.store.Put(key, value)
}

// Get : get value
func (cli *LocalStorage) Get(key string) (val string, err error) {
	cli.cacheMutex.Lock()
	defer cli.cacheMutex.Unlock()

	// search cache first
	if val, ok := cli.cache[key]; ok {
		return val, err
	}

	err = cli.store.Get(key, &val)
	if err == skv.ErrNotFound {
		err = ErrNotFound
	}
	return val, err
}

// Del : delete key
func (cli *LocalStorage) Del(key string) error {
	cli.cacheMutex.Lock()
	defer cli.cacheMutex.Unlock()

	// remove from cache
	delete(cli.cache, key)

	err := cli.store.Delete(key)
	if err != nil && err != skv.ErrNotFound {
		return err
	}
	return nil
}

// Destory remove db files
func (cli *LocalStorage) Destory() error {
	cli.Close()
	return os.RemoveAll(cli.path)
}

// Flush flush and clean cache
func (cli *LocalStorage) Flush() {
	cli.cacheMutex.Lock()
	defer cli.cacheMutex.Unlock()

	for key, value := range cli.cache {
		cli.store.Put(key, value)
	}
	cli.cache = make(map[string]string)
}
