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
	"bytes"
	"encoding/gob"
	"errors"
	"sort"
	"time"

	"github.com/boltdb/bolt"
)

// KVStore represents the key value store. Use the Open() method to create
// one, and Close() it when done.
type KVStore struct {
	db *bolt.DB
}

var (
	// ErrKeyNotFound is returned when the key supplied to a Get or Delete
	// method does not exist in the database.
	ErrKeyNotFound = errors.New("skv: key not found")

	// ErrBadValue is returned when the value supplied to the Put method
	// is nil.
	ErrBadValue = errors.New("skv: bad value")

	bucketName = []byte("kv")
)

// Open a key-value store. "path" is the full path to the database file, any
// leading directories must have been created already. File is created with
// mode 0640 if needed.
//
// Because of BoltDB restrictions, only one process may open the file at a
// time. Attempts to open the file from another process will fail with a
// timeout error.
func Open(path string) (*KVStore, error) {
	opts := &bolt.Options{
		Timeout: 50 * time.Millisecond,
	}
	if db, err := bolt.Open(path, 0640, opts); err != nil {
		return nil, err
	} else {
		err := db.Update(func(tx *bolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists(bucketName)
			return err
		})
		if err != nil {
			return nil, err
		} else {
			return &KVStore{db: db}, nil
		}
	}
}

// Put an entry into the store. The passed value is gob-encoded and stored.
// The key can be an empty string, but the value cannot be nil - if it is,
// Put() returns ErrBadValue.
//
//	err := store.Put("key42", 156)
//	err := store.Put("key42", "this is a string")
//	m := map[string]int{
//	    "harry": 100,
//	    "emma":  101,
//	}
//	err := store.Put("key43", m)
func (kvs *KVStore) Put(key string, value string) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return err
	}
	return kvs.db.Update(func(tx *bolt.Tx) error {
		if value == "" {
			return tx.Bucket(bucketName).Delete([]byte(key))
		}
		return tx.Bucket(bucketName).Put([]byte(key), buf.Bytes())
	})
}

func (kvs *KVStore) BulkPut(kvParis map[string]string) error {
	if len(kvParis) == 0 {
		return nil
	}
	var err error

	keys := make([]string, 0, len(kvParis))
	for k := range kvParis {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return kvs.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketName)
		for _, key := range keys {
			if kvParis[key] == "" {
				bucket.Delete([]byte(key))
			} else {
				var buf bytes.Buffer
				if err = gob.NewEncoder(&buf).Encode(kvParis[key]); err != nil {
					continue
				}
				bucket.Put([]byte(key), buf.Bytes())
			}
		}
		return err
	})
}

// Get an entry from the store. "value" must be a pointer-typed. If the key
// is not present in the store, Get returns ErrNotFound.
//
//	type MyStruct struct {
//	    Numbers []int
//	}
//	var val MyStruct
//	if err := store.Get("key42", &val); err == skv.ErrNotFound {
//	    // "key42" not found
//	} else if err != nil {
//	    // an error occurred
//	} else {
//	    // ok
//	}
//
// The value passed to Get() can be nil, in which case any value read from
// the store is silently discarded.
//
//	if err := store.Get("key42", nil); err == nil {
//	    fmt.Println("entry is present")
//	}
func (kvs *KVStore) Get(key string) (string, error) {
	var value string
	err := kvs.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketName).Cursor()
		if k, v := c.Seek([]byte(key)); k == nil || string(k) != key {
			return ErrKeyNotFound
		} else {
			d := gob.NewDecoder(bytes.NewReader(v))
			return d.Decode(&value)
		}
	})
	return value, err
}

// List entries with prefix
func (kvs *KVStore) List(prefix string) (map[string]string, error) {
	var err error
	values := make(map[string]string)
	err = kvs.db.View(func(tx *bolt.Tx) error {
		prefixBytes := []byte(prefix)
		c := tx.Bucket(bucketName).Cursor()
		for k, v := c.Seek(prefixBytes); k != nil && bytes.HasPrefix(k, prefixBytes); k, v = c.Next() {
			var value string
			d := gob.NewDecoder(bytes.NewReader(v))
			err = d.Decode(&value)
			if err == nil {
				values[string(k)] = value
			}
		}
		return err
	})
	return values, err
}

// Delete the entry with the given key. If no such key is present in the store,
// it returns ErrKeyNotFound.
//
//	store.Delete("key42")
func (kvs *KVStore) Delete(key string) error {
	return kvs.db.Update(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketName).Cursor()
		if k, _ := c.Seek([]byte(key)); k == nil || string(k) != key {
			return ErrKeyNotFound
		} else {
			return c.Delete()
		}
	})
}

// Close closes the key-value store file.
func (kvs *KVStore) Close() error {
	return kvs.db.Close()
}
