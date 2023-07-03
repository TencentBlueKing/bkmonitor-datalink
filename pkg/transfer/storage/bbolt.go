// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build bbolt
// +build bbolt

package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"go.etcd.io/bbolt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfBBoltStorageBucket = "storage.bbolt.bucket"
)

// BBoltStore :
type BBoltStore struct {
	*define.BaseStore
	bucket    []byte
	db        *bbolt.DB
	Cache     map[string]StoreCache
	cacheSize int
}

// var maxCacheSize int

// NewBBoltStoreFromContext :
func NewBBoltStoreFromContext(ctx context.Context) (*BBoltStore, error) {
	conf := config.FromContext(ctx)
	perm, err := utils.StringToFilePerm(conf.GetString(ConfStoragePerm))
	if err != nil {
		return nil, err
	}

	targetPath := conf.GetString(ConfStorageTaget)
	path := filepath.Join(conf.GetString(ConfStorageDataDir), fmt.Sprintf("%s-%s.db", define.AppName, define.ServiceID))
	bucket := conf.GetString(ConfBBoltStorageBucket)
	maxCacheSize := conf.GetInt(ConfCcCacheSize)
	logging.Infof("maxcachesize： [%d]", maxCacheSize)

	// 如果指向一个文件，则使用文件地址为目标
	if targetPath != "" {
		path = targetPath
	}

	logging.Infof("bbolt target path:[%s]", path)
	store, err := NewBBoltStore(bucket, path, perm, nil)
	store.cacheSize = maxCacheSize
	if err != nil {
		return store, err
	}

	// 判断是否需要 “不”启动 同步cmdb缓存 动作
	isStopSyncData := conf.GetBool(ConfStopCcCache)
	if isStopSyncData {
		return store, err
	}
	// 初始化goroutine将缓存通道中数据刷入map
	InitBboltChanStore(ctx, store)
	return store, err
}

// NewBBoltStore :
func NewBBoltStore(bucket string, path string, perm os.FileMode, opts *bbolt.Options) (*BBoltStore, error) {
	db, err := bbolt.Open(path, perm, opts)
	if err != nil {
		return nil, err
	}

	store := &BBoltStore{
		bucket:    []byte(bucket),
		db:        db,
		BaseStore: define.NewBaseStore(),
		Cache:     make(map[string]StoreCache),
	}
	return store, store.init()
}

func (s *BBoltStore) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			_, err := tx.CreateBucket(s.bucket)
			return err
		}
		return nil
	})
}

func (s *BBoltStore) getItem(data []byte) (*define.StoreItem, error) {
	item := new(define.StoreItem)
	err := json.Unmarshal(data, item)
	if err != nil {
		return nil, err
	}

	return item, err
}

// Set :
func (s *BBoltStore) Set(key string, data []byte, expires time.Duration) error {
	item := define.NewStoreItem(data, expires)
	value, err := json.Marshal(item)
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		return bucket.Put([]byte(key), value)
	})
}

// Get :
func (s *BBoltStore) Get(key string) ([]byte, error) {
	var result []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		result = bucket.Get([]byte(key))
		return nil
	})
	if err != nil {
		return nil, err
	}

	if result == nil {
		return nil, define.ErrItemNotFound
	}

	item, err := s.getItem(result)
	if err != nil {
		return nil, err
	}

	data := item.GetData(false)
	if data == nil {
		return nil, define.ErrItemNotFound
	}

	return data, nil
}

// Exists :
func (s *BBoltStore) Exists(key string) (bool, error) {
	data, err := s.Get(key)
	if err == define.ErrItemNotFound {
		return false, nil
	}
	return data != nil, err
}

// Delete :
func (s *BBoltStore) Delete(key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		return bucket.Delete([]byte(key))
	})
}

// Close :
func (s *BBoltStore) Close() error {
	return s.db.Close()
}

// Commit :
func (s *BBoltStore) Commit() error {
	// 发送信号，将缓存通道中的数据刷盘
	UpdateSignal <- struct{}{}
	return nil
}

// clean : 之前的Commit
func (s *BBoltStore) clean() error {
	keys := make([][]byte, 0)
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		return bucket.ForEach(func(k, v []byte) error {
			item, err := s.getItem(v)
			if err != nil {
				return err
			}
			if item.IsExpired() {
				keys = append(keys, k)
			}
			return nil
		})
	})
	if err != nil {
		return errors.Wrapf(err, "filter expired keys failed")
	}

	err = s.db.Batch(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		for _, key := range keys {
			err := bucket.Delete(key)
			if err != nil {
				logging.Warnf("delete expired key->[%s] failed for->[%s]", key, err)
				return err
			}
			logging.Infof("delete expired key->[%s]", key)
		}
		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "delete expired keys failed")
	}

	return s.db.Sync()
}

// Scan :
func (s *BBoltStore) Scan(p string, callback define.StoreScanCallback, withTime ...bool) error {
	prefix := []byte(p)
	var withExpiresAt bool
	if len(withTime) != 0 {
		withExpiresAt = withTime[0]
	}

	return s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		cursor := bucket.Cursor()
		for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {

			// 判断是否需要带上过期数据
			if withExpiresAt {
				if !callback(string(k), v) {
					break
				}
				continue
			}

			item, err := s.getItem(v)
			if err != nil {
				return err
			}

			data := item.GetData(false)
			if data != nil {
				if !callback(string(k), data) {
					break
				}
			}
		}
		return nil
	})
}

// PutCache :
func (s *BBoltStore) PutCache(key string, data []byte, expires time.Duration) error {
	CacheChan <- CacheItem{key, data, expires}
	return nil
}

// Batch :
func (s *BBoltStore) Batch() error {
	cacheCopy := s.Cache
	if len(cacheCopy) == 0 {
		return nil
	}
	s.Cache = make(map[string]StoreCache)
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		for key, bc := range cacheCopy {
			item := define.NewStoreItem(bc.data, bc.expires)
			value, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte(key), value); err != nil {
				return err
			}
		}
		return nil
	})
}

// 初始化缓存通道
func InitBboltChanStore(ctx context.Context, s *BBoltStore) {
	CacheChan = make(chan CacheItem)
	UpdateSignal = make(chan struct{}, 1)
	go func() {
	loop:
		for {
			select {
			case item := <-CacheChan:
				s.Cache[item.key] = StoreCache{
					data:    item.data,
					expires: item.expires,
				}
				if len(s.Cache) >= s.cacheSize {
					if err := s.Batch(); err != nil {
						logging.Errorf("batch store error : %s", err)
					}
				}
			case <-UpdateSignal:
				if err := s.Batch(); err != nil {
					logging.Errorf("batch store error : %s", err)
				}
				if err := s.clean(); err != nil {
					logging.Errorf("commit store error : %s", err)
				}
			case <-ctx.Done():
				logging.Info("ctx done, store chan close")
				break loop
			}
		}
	}()
}

func initBBoltConfiguration(c define.Configuration) {
	c.SetDefault(ConfBBoltStorageBucket, "default")
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initBBoltConfiguration))
	define.RegisterStore("bbolt", func(ctx context.Context, name string) (define.Store, error) {
		return NewBBoltStoreFromContext(ctx)
	})
}
