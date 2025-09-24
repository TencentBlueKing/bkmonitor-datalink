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

	"github.com/spf13/viper"
	bolt "go.etcd.io/bbolt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/kvstore"
)

const (
	BboltDefaultPathConfigPath = "bbolt.default_path"
	BboltDefaultBucketNamePath = "bbolt.default_bucket_name"
	BboltDefaultSyncPath       = "bbolt.default_sync"
	BboltDefaultBatchTimeSleep = "bbolt.default_time_sleep"
)

var mutex *sync.RWMutex

const (
	KeyNotFound    = "keyNotFound"
	BucketNotFount = "bucket not found"
)

func init() {
	mutex = new(sync.RWMutex)
	viper.SetDefault(BboltDefaultPathConfigPath, "bolt.db")
	viper.SetDefault(BboltDefaultSyncPath, false)
	viper.SetDefault(BboltDefaultBucketNamePath, "spaceBucket")
	viper.SetDefault(BboltDefaultBatchTimeSleep, "1ms")
}

// Client bbolt client struct
type Client struct {
	Path       string
	DB         *bolt.DB
	BucketName []byte
}

// NewClient create a new client instance
func NewClient(path string, bucketName string) *Client {
	if path == "" {
		path = viper.GetString(BboltDefaultPathConfigPath)
	}
	if bucketName == "" {
		bucketName = viper.GetString(BboltDefaultBucketNamePath)
	}
	return &Client{Path: path, BucketName: kvstore.String2byte(bucketName)}
}

// Open create boltDB file.
func (c *Client) Open() error {
	mutex.Lock()
	defer mutex.Unlock()

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

	if db == nil {
		return fmt.Errorf("open boltdb is empty")
	}

	// New Bucket
	err = db.Update(func(tx *bolt.Tx) error {
		_, bucketErr := tx.CreateBucketIfNotExists(c.BucketName)
		return bucketErr
	})
	if err != nil {
		return fmt.Errorf("create bucket error: %v", err)
	}

	c.DB = db
	// set db noSync
	c.DB.NoSync = !viper.GetBool(BboltDefaultSyncPath)

	return nil
}

// Close the connection to the bolt database
func (c *Client) Close() error {
	if c.DB != nil {
		return c.DB.Close()
	}
	return nil
}

// Put a key value pair into the db
func (c *Client) Put(key, val []byte) error {
	err := c.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b == nil {
			return errors.New(BucketNotFount)
		}
		return b.Put(key, val)
	})
	return err
}

// Get a value by key from db
func (c *Client) Get(key []byte) ([]byte, error) {
	var ret []byte
	err := c.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b == nil {
			return errors.New(BucketNotFount)
		}
		// get the val by key
		ret = b.Get(key)
		if ret == nil {
			return errors.New(KeyNotFound)
		}
		return nil
	})
	return ret, err
}

// Delete a key
func (c *Client) Delete(key []byte) error {
	err := c.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b != nil {
			return b.Delete(key)
		}
		return nil
	})
	return err
}

// BatchWrite batch write key value pairs
func (c *Client) BatchWrite(keys [][]byte, vals [][]byte) error {
	err := c.DB.Batch(func(tx *bolt.Tx) error {
		// check bucket
		b := tx.Bucket(c.BucketName)
		if b == nil {
			return errors.New(BucketNotFount)
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

func (c *Client) GetAll() ([][]byte, [][]byte, error) {
	keys := make([][]byte, 0)
	vals := make([][]byte, 0)
	err := c.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(c.BucketName)
		if b == nil {
			return errors.New(BucketNotFount)
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
