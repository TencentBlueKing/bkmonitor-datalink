// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewScrollSession(t *testing.T) {
	type Case struct {
		name         string
		maxSlice     int
		maxFailedNum int
		limit        int

		session string
	}

	timeout := time.Minute

	for _, c := range []Case{
		{
			name: "test_1",
			session: `{
  "session_key" : "scroll:session:test_1",
  "lock_key" : "scroll:lock:test_1",
  "scroll_window_timeout" : 60000000000,
  "scroll_lock_timeout" : 60000000000,
  "max_slice" : 0,
  "slice_max_failed_num" : 0,
  "limit" : 0,
  "scroll_ids" : [ {
    "slice_idx" : 0,
    "slice_max" : 1,
    "scroll_id" : "",
    "offset" : 0,
    "status" : "completed",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 0
  } ]
}`,
		},
		{
			name:     "test_2",
			maxSlice: 1,
			limit:    100,
			session: `{
  "session_key" : "scroll:session:test_2",
  "lock_key" : "scroll:lock:test_2",
  "scroll_window_timeout" : 60000000000,
  "scroll_lock_timeout" : 60000000000,
  "max_slice" : 1,
  "slice_max_failed_num" : 0,
  "limit" : 100,
  "scroll_ids" : [ {
    "slice_idx" : 0,
    "slice_max" : 1,
    "scroll_id" : "",
    "offset" : 0,
    "status" : "pending",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 100
  } ]
}`,
		},
		{
			name:     "test_3",
			maxSlice: 3,
			limit:    100,
			session: `{
  "session_key" : "scroll:session:test_3",
  "lock_key" : "scroll:lock:test_3",
  "scroll_window_timeout" : 60000000000,
  "scroll_lock_timeout" : 60000000000,
  "max_slice" : 3,
  "slice_max_failed_num" : 0,
  "limit" : 100,
  "scroll_ids" : [ {
    "slice_idx" : 0,
    "slice_max" : 3,
    "scroll_id" : "",
    "offset" : 0,
    "status" : "pending",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 34
  }, {
    "slice_idx" : 1,
    "slice_max" : 3,
    "scroll_id" : "",
    "offset" : 34,
    "status" : "pending",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 33
  }, {
    "slice_idx" : 2,
    "slice_max" : 3,
    "scroll_id" : "",
    "offset" : 67,
    "status" : "pending",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 33
  } ]
}`,
		},
		{
			name:     "test_4",
			maxSlice: 3,
			limit:    2,
			session: `{
  "session_key" : "scroll:session:test_4",
  "lock_key" : "scroll:lock:test_4",
  "scroll_window_timeout" : 60000000000,
  "scroll_lock_timeout" : 60000000000,
  "max_slice" : 3,
  "slice_max_failed_num" : 0,
  "limit" : 2,
  "scroll_ids" : [ {
    "slice_idx" : 0,
    "slice_max" : 3,
    "scroll_id" : "",
    "offset" : 0,
    "status" : "pending",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 1
  }, {
    "slice_idx" : 1,
    "slice_max" : 3,
    "scroll_id" : "",
    "offset" : 1,
    "status" : "pending",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 1
  }, {
    "slice_idx" : 2,
    "slice_max" : 3,
    "scroll_id" : "",
    "offset" : 2,
    "status" : "completed",
    "failed_num" : 0,
    "max_failed_num" : 0,
    "limit" : 0
  } ]
}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			session := NewScrollSession(c.name, timeout, timeout, c.maxSlice, c.maxFailedNum, c.limit)

			actual, _ := json.Marshal(session)
			assert.JSONEq(t, c.session, string(actual))
		})
	}

}
