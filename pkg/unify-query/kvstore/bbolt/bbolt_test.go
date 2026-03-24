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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func NewTestClient(t *testing.T) (*Client, func(), error) {
	c, closeFn, err := newTestClient(t)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Open(); err != nil {
		return nil, nil, err
	}

	return c, closeFn, nil
}

func newTestClient(t *testing.T) (*Client, func(), error) {
	f, err := os.CreateTemp("", "unify-query-bolt-")
	if err != nil {
		return nil, nil, errors.New("unable to open temporary boltdb file")
	}
	f.Close()

	c := NewClient(f.Name(), "")

	close := func() {
		c.Close()
		os.Remove(c.Path)
	}

	return c, close, nil
}

func TestClientOpen(t *testing.T) {
	tmpDir, path, err := newTmpFile()
	if err != nil {
		t.Fatalf("unable to create temporary test directory %s: %v", tmpDir, err)
	}

	defer func() {
		if err := removeTmpFile(tmpDir); err != nil {
			t.Fatalf("unable to delete temporary test directory %s: %v", tmpDir, err)
		}
	}()

	c := NewClient(path, "")

	if err := c.Open(); err != nil {
		t.Fatalf("unable to create database %s: %v", path, err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("unable to close database %s: %v", path, err)
	}
}

func TestClientPutAndGet(t *testing.T) {
	tmpDir, path, err := newTmpFile()
	if err != nil {
		t.Fatalf("unable to create temporary test directory %s: %v", tmpDir, err)
	}

	defer func() {
		if err := removeTmpFile(tmpDir); err != nil {
			t.Fatalf("unable to delete temporary test directory %s: %v", tmpDir, err)
		}
	}()

	c := NewClient(path, "")

	if err := c.Open(); err != nil {
		t.Fatalf("unable to create database %s: %v", path, err)
	}

	// key and value
	key := "unify-query-test-key"
	val := "unify-query-test-value"

	if err := c.Put([]byte(key), []byte(val)); err != nil {
		t.Fatalf("unable to write data: %v", err)
	}
	valFromDB, err := c.Get([]byte(key))
	if err != nil {
		t.Fatalf("unable to read data of key: %s, err: %v", key, err)
	}
	assert.Equal(t, val, string(valFromDB))

	if err := c.Close(); err != nil {
		t.Fatalf("unable to close database %s: %v", path, err)
	}
}

func TestClientBatchPutAndGetAll(t *testing.T) {
	tmpDir, path, err := newTmpFile()
	if err != nil {
		t.Fatalf("unable to create temporary test directory %s: %v", tmpDir, err)
	}

	defer func() {
		if err := removeTmpFile(tmpDir); err != nil {
			t.Fatalf("unable to delete temporary test directory %s: %v", tmpDir, err)
		}
	}()

	c := NewClient(path, "")

	if err := c.Open(); err != nil {
		t.Fatalf("unable to create database %s: %v", path, err)
	}
	keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3")}
	vals := [][]byte{[]byte("val1"), []byte("val2"), []byte("val3")}

	// batch write
	if err := c.BatchWrite(keys, vals); err != nil {
		t.Fatalf("unable to batch write, err: %v", err)
	}

	// get all values
	keysFromDB := make([][]byte, 0)
	valsFromDB := make([][]byte, 0)
	keysFromDB, valsFromDB, err = c.GetAll()
	if err != nil {
		t.Fatalf("unable to read all db data, err:%v", err)
	}
	assert.Equal(t, keys, keysFromDB)
	assert.Equal(t, vals, valsFromDB)

	if err := c.Close(); err != nil {
		t.Fatalf("unable to close database %s: %v", path, err)
	}
}

func newTmpFile() (string, string, error) {
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return "", "", err
	}
	boltFile := filepath.Join(tmpDir, "unify-query", "bolt.db")
	return tmpDir, boltFile, nil
}

func removeTmpFile(tmpDir string) error {
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	return nil
}
