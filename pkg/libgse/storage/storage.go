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
	"errors"
	"time"
)

var (
	_storage    Storage
	ErrNotFound = errors.New("key not found")
)

// StorageConfig
type StorageConfig struct{}

// Storage : key value storage
type Storage interface {
	Set(key, value string, expire time.Duration) error
	Get(key string) (string, error)
	List(prefix string) (values map[string]string, err error)
	Del(key string) error
	Close() error
	Destroy() error // clean files

	SetFlushInterval(interval time.Duration) // set flush interval
}

// Init : init storage
// path is a filepath
func Init(path string, config *StorageConfig) error {
	var err error
	_storage, err = NewLocalStorage(path)
	return err
}

// Get get value. will return error=ErrNotFound if key not exist
func Get(key string) (string, error) {
	return _storage.Get(key)
}

// List values with key prefix
func List(prefix string) (values map[string]string, err error) {
	return _storage.List(prefix)
}

// Set kv to storage, expire not used now
func Set(key, value string, expire time.Duration) error {
	if len(key) == 0 || len(value) == 0 {
		return nil
	}
	return _storage.Set(key, value, expire)
}

// Del delete key, will do nothing if key not exist
func Del(key string) error {
	return _storage.Del(key)
}

// SetFlushInterval : set flush interval
func SetFlushInterval(interval time.Duration) {
	_storage.SetFlushInterval(interval)
}

func Close() {
	_storage.Close()
}

// Destroy remove files
func Destroy() error {
	return _storage.Destroy()
}
