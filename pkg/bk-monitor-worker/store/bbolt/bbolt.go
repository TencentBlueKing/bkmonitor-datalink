// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bbolt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store"
)

var mutex *sync.RWMutex

// Client bbolt client struct
type Instance struct {
	Path       string
	DB         *bolt.DB
	BucketName []byte
}

var instance *Instance

// NewInstance create a new client instance
func NewInstance(path string, bucketName string) (*Instance, error) {
	if path == "" {
		path = config.StorageBboltDefaultPath
	}
	if bucketName == "" {
		bucketName = config.StorageBboltDefaultBucketName
	}

	return &Instance{Path: path, BucketName: store.String2byte(bucketName)}, nil
}

// GetInstance get a bolt instance
func GetInstance(path string, bucketName string) (*Instance, error) {
	if instance != nil {
		return instance, nil
	}
	return NewInstance(path, bucketName)
}

// Open create boltDB file.
func (c *Instance) Open() error {
	mutex.Lock()
	defer mutex.Unlock()

	// 如果 db 已经存在，则不需要创建
	if c.DB != nil {
		return nil
	}

	// 判断是否是绝对路径
	var path string
	if c.Path != "" && string(c.Path[0]) == "/" {
		path = c.Path
	} else {
		// 如果是相对路径则需要获取当前路径进行拼装
		// 获取当前路径
		baseDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			return err
		}
		path = strings.Join([]string{baseDir, c.Path}, "/")
	}

	// check dir exist
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("unable to create directory %s: %v", c.Path, err)
	}

	// check db file detail
	if _, err := os.Stat(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Open database file.
	db, err := bolt.Open(path, 0o666, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("unable to open boltdb: %w", err)
	}

	c.DB = db
	// 提高数据库写入性能
	c.DB.NoFreelistSync = true
	// set db noSync
	c.DB.NoSync = !config.StorageBboltDefaultSync

	return nil
}

// Close the connection to the bolt database
func (c *Instance) Close() error {
	if c.DB != nil {
		return c.DB.Close()
	}
	return nil
}

// Put a key value pair into the db
func (c *Instance) Put(key, val string, expiration time.Duration) error {
	err := c.DB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(c.BucketName)
		if err != nil {
			return fmt.Errorf("create bucket error: %v", err)
		}
		// convert string to bytes
		keyByte := store.String2byte(key)
		valByte := store.String2byte(val)
		return b.Put(keyByte, valByte)
	})
	return err
}

// Get a value by key from db
func (c *Instance) Get(key string) ([]byte, error) {
	var ret []byte
	err := c.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b == nil {
			return errors.New("bucket not found")
		}
		// get the val by key
		keyByte := store.String2byte(key)
		ret = b.Get(keyByte)
		if ret == nil {
			return errors.New("keyNotFound")
		}
		return nil
	})
	return ret, err
}

// Delete a key
func (c *Instance) Delete(key string) error {
	err := c.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b != nil {
			keyByte := store.String2byte(key)
			return b.Delete(keyByte)
		}
		return nil
	})
	return err
}

// BatchWrite batch write key value pairs
func (c *Instance) BatchWrite(keys [][]byte, vals [][]byte) error {
	err := c.DB.Batch(func(tx *bolt.Tx) error {
		// check bucket
		b, err := tx.CreateBucketIfNotExists(c.BucketName)
		if err != nil {
			return fmt.Errorf("create bucket error: %v", err)
		}

		// write the k-v pairs
		for i := 0; i < len(keys); i++ {
			if err := b.Put(keys[i], vals[i]); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (c *Instance) GetAll() ([][]byte, [][]byte, error) {
	keys := make([][]byte, 0)
	vals := make([][]byte, 0)
	err := c.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b == nil {
			return errors.New("bucket not found")
		}
		cursor := b.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			keys = append(keys, k)
			vals = append(vals, v)
		}

		return nil
	})
	return keys, vals, err
}
