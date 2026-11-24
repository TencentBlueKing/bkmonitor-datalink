// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskstate

import (
	"sync"
	"testing"
)

func TestActivityRequiresEnabledTaskAndTracksActualOwners(t *testing.T) {
	r := testRegistry()
	for _, id := range []string{"missing", "off"} {
		if _, err := r.ObserveActivity(id); err == nil {
			t.Fatal("unregistered/disabled activity", id)
		}
	}
	a, err := r.ObserveActivity("periodic")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.ObserveActivity("periodic")
	if err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().Tasks[0].Active {
		t.Fatal("constructing observer activated task")
	}
	a.SetActive(false)
	a.SetActive(true)
	a.SetActive(true)
	b.SetActive(true)
	a.SetActive(false)
	a.SetActive(false)
	if !r.Snapshot().Tasks[0].Active {
		t.Fatal("one owner cleared another")
	}
	b.SetActive(false)
	if r.Snapshot().Tasks[0].Active {
		t.Fatal("last owner did not clear")
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				a.SetActive(true)
				_ = r.Snapshot()
				a.SetActive(false)
			}
		})
	}
	wg.Wait()
	a.SetActive(false)
	if r.Snapshot().Tasks[0].Active {
		t.Fatal("concurrent activity leaked")
	}
}
