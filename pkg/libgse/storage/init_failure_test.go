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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewLocalStorageOpenFailureDoesNotStartFlusher verifies that a failed
// database open does not construct a LocalStorage with a nil store. The old
// implementation returned a non-nil storage with a background flusher whose
// periodic Flush panicked on the nil store after the caller handled the error.
func TestNewLocalStorageOpenFailureDoesNotStartFlusher(t *testing.T) {
	// Opening a directory as the bolt database file fails on all platforms.
	storage, err := NewLocalStorage(t.TempDir())
	require.Error(t, err)
	if storage != nil {
		t.Fatalf("NewLocalStorage() returned a non-nil storage on error: %#v", storage)
	}

	// Wait past the old 1s flush ticker so a leaked flusher would panic here.
	time.Sleep(1200 * time.Millisecond)
}

// TestInitFailureLeavesStorageUnset verifies that a failed Init does not leave
// a half-initialized storage registered for later cleanup paths.
func TestInitFailureLeavesStorageUnset(t *testing.T) {
	old := _storage
	_storage = nil
	t.Cleanup(func() { _storage = old })

	err := Init(t.TempDir(), nil)
	require.Error(t, err)
	assert.Nil(t, _storage)

	// Cleanup after a failed initialization must be safe.
	Close()
}
