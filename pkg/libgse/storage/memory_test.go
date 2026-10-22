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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorageNormal(t *testing.T) {
	s := NewMemoryStorage()

	_, err := s.Get("missing")
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, s.Set("key", "value", 0))
	v, err := s.Get("key")
	require.NoError(t, err)
	assert.Equal(t, "value", v)

	// Set overwrites the existing value.
	require.NoError(t, s.Set("key", "value2", time.Minute))
	v, err = s.Get("key")
	require.NoError(t, err)
	assert.Equal(t, "value2", v)

	// Deleting a missing key succeeds, deleting an existing key takes effect
	// immediately without any flush.
	require.NoError(t, s.Del("missing"))
	require.NoError(t, s.Del("key"))
	_, err = s.Get("key")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryStorageIgnoreEmpty(t *testing.T) {
	s := NewMemoryStorage()

	require.NoError(t, s.Set("", "value", 0))
	require.NoError(t, s.Set("key", "", 0))

	_, err := s.Get("")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.Get("key")
	assert.ErrorIs(t, err, ErrNotFound)

	values, err := s.List("")
	require.NoError(t, err)
	assert.Empty(t, values)
}

func TestMemoryStorageList(t *testing.T) {
	s := NewMemoryStorage()
	require.NoError(t, s.Set("state:1|2", "v1", 0))
	require.NoError(t, s.Set("state:2|3", "v2", 0))
	require.NoError(t, s.Set("others:test", "v3", 0))

	values, err := s.List("state")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"state:1|2": "v1",
		"state:2|3": "v2",
	}, values)

	// The returned map is a snapshot: mutating it must not affect the storage.
	values["state:1|2"] = "changed"
	delete(values, "state:2|3")
	values["state:new"] = "new"

	values, err = s.List("state")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"state:1|2": "v1",
		"state:2|3": "v2",
	}, values)

	values, err = s.List("notexist")
	require.NoError(t, err)
	assert.Empty(t, values)
}

func TestMemoryStorageClose(t *testing.T) {
	s := NewMemoryStorage()
	require.NoError(t, s.Set("key", "value", 0))

	require.NoError(t, s.Close())
	// Repeated Close succeeds.
	require.NoError(t, s.Close())

	_, err := s.Get("key")
	assert.ErrorIs(t, err, ErrStorageClosed)
	assert.ErrorIs(t, s.Set("key", "value", 0), ErrStorageClosed)
	// Empty values are ignored only while the storage is open; a closed storage
	// must still report the closed state.
	assert.ErrorIs(t, s.Set("", "value", 0), ErrStorageClosed)
	assert.ErrorIs(t, s.Set("key", "", 0), ErrStorageClosed)
	_, err = s.List("")
	assert.ErrorIs(t, err, ErrStorageClosed)
	assert.ErrorIs(t, s.Del("key"), ErrStorageClosed)

	// Flush interval setting has no return value and must stay safe.
	s.SetFlushInterval(time.Second)

	// Destroy remains safe and never touches files.
	require.NoError(t, s.Destroy())
}

func TestMemoryStorageDestroy(t *testing.T) {
	s := NewMemoryStorage()
	require.NoError(t, s.Set("key", "value", 0))
	require.NoError(t, s.Destroy())

	_, err := s.Get("key")
	assert.ErrorIs(t, err, ErrStorageClosed)
}

func TestMemoryStorageConcurrent(t *testing.T) {
	s := NewMemoryStorage()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = s.Set("key", "value", 0)
				_, _ = s.Get("key")
				_, _ = s.List("key")
				_ = s.Del("key")
				s.SetFlushInterval(time.Second)
			}
		}()
	}
	wg.Wait()

	require.NoError(t, s.Close())
}

func TestStorageInitMemory(t *testing.T) {
	old := _storage
	t.Cleanup(func() {
		Close()
		_storage = old
	})

	InitMemory()

	require.NoError(t, Set("key", "value", 0))
	v, err := Get("key")
	require.NoError(t, err)
	assert.Equal(t, "value", v)

	values, err := List("key")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"key": "value"}, values)

	require.NoError(t, Del("key"))
	_, err = Get("key")
	assert.ErrorIs(t, err, ErrNotFound)

	Close()
	Close()
}

func TestStorageInitMemoryClosesPrevious(t *testing.T) {
	old := _storage
	t.Cleanup(func() {
		Close()
		_storage = old
	})

	previous := NewMemoryStorage()
	_storage = previous
	InitMemory()

	// The previous package storage must be closed instead of leaked.
	assert.ErrorIs(t, previous.Set("key", "value", 0), ErrStorageClosed)

	require.NoError(t, Set("key", "value", 0))
	v, err := Get("key")
	require.NoError(t, err)
	assert.Equal(t, "value", v)
}

func TestStorageCloseWithoutInit(t *testing.T) {
	old := _storage
	_storage = nil
	defer func() { _storage = old }()

	// Cleanup must be safe before Init succeeded (partial initialization).
	Close()
	require.NoError(t, Destroy())
}
