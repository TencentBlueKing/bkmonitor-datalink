// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

func TestRecoverForPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer utils.RecoverFor(func(err error) {
			if err == nil {
				t.Errorf("catch error failed")
			}
			wg.Done()
		})
		panic(fmt.Errorf("test"))
	}()
	wg.Wait()
}

func TestRecoverForSuccess(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer utils.RecoverFor(func(err error) {
			t.Errorf("catch error: %v", err)
		})
		wg.Done()
	}()
	wg.Wait()
}

func panicFunc(i int) {
	if i <= 0 {
		panic(fmt.Errorf("test"))
	}
	panicFunc(i - 1)
}

func TestRecoverForSubCall(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer utils.RecoverFor(func(err error) {
			if err == nil {
				t.Errorf("catch error failed")
			}
			wg.Done()
		})
		panicFunc(10)
	}()
	wg.Wait()
}
