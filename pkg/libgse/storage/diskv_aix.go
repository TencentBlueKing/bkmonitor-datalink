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
	"os"
	"path/filepath"
	"time"

	"github.com/peterbourgon/diskv"
)

type LocalStorage struct {
	path  string
	store *diskv.Diskv
}

// new dikv storage
func NewLocalStorage(path string) (*LocalStorage, error) {
	flatTransform := func(s string) []string { return []string{} }
	kvPath := filepath.Join(filepath.Dir(path), "dikv")
	store := diskv.New(diskv.Options{
		BasePath:     kvPath,
		Transform:    flatTransform,
		CacheSizeMax: 1024 * 1024,
	})
	return &LocalStorage{store: store, path: kvPath}, nil
}

// Flush : set flush interval
func (cli *LocalStorage) SetFlushInterval() {
	// do nothing: Because it is written in real time, there is no need to refresh
	return nil
}

// Close : close db
func (cli *LocalStorage) Close() error {
	return nil
}

// Set : set value
func (cli *LocalStorage) Set(key, value string, expire time.Duration) error {
	return cli.store.Write(key, []byte(value))
}

// Get : get value
// if not found, return ErrNotFound
func (cli *LocalStorage) Get(key string) (string, error) {
	buf, err := cli.store.Read(key)
	if len(buf) == 0 {
		err = ErrNotFound
	}
	return string(buf), err
}

// Del : delete key
func (cli *LocalStorage) Del(key string) error {
	return cli.store.Erase(key)
}

// Destroy remove db files
func (cli *LocalStorage) Destroy() error {
	cli.Close()
	return os.RemoveAll(cli.path)
}
