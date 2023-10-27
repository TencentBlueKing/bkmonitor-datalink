// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package notifier

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifier(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "notifier")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		f, err := os.Create(filepath.Join(tmpDir, "file1.txt"))
		assert.NoError(t, err)
		f.Write([]byte("foo"))
		time.Sleep(time.Second * 2)
		f.Write([]byte("bar"))
		f.Close()
	}()

	notifier := New(time.Second, filepath.Join(tmpDir, "*.txt"))

	go func() {
		time.Sleep(time.Second * 5)
		notifier.Close()
	}()

	n := 0
	for range notifier.Ch() {
		n++
	}
	wg.Wait()
	assert.Equal(t, 2, n)
}
