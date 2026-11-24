// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"testing"
	"time"
)

func TestFetchMaxWaitDefaultsAndBounds(t *testing.T) {
	c := testConfig()
	if c.FetchMaxWait != 100*time.Millisecond {
		t.Fatalf("default=%s", c.FetchMaxWait)
	}
	for _, wait := range []time.Duration{-time.Second, time.Millisecond, 5001 * time.Millisecond} {
		c.FetchMaxWait = wait
		if c.Validate() == nil {
			t.Errorf("accepted wait=%s", wait)
		}
	}
	for _, wait := range []time.Duration{10 * time.Millisecond, 100 * time.Millisecond, 5 * time.Second} {
		c.FetchMaxWait = wait
		if err := c.Validate(); err != nil {
			t.Errorf("wait=%s: %v", wait, err)
		}
	}
}
