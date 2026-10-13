// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"testing"
	"time"
)

// An execution's total in-place wait falls in the first bucket whose bound it
// does not pass: each bound belongs to its own bucket, and a millisecond past
// it to the next.
func TestAnInPlaceWaitIsBucketedByTheBoundItDoesNotPass(t *testing.T) {
	for _, test := range []struct {
		total  time.Duration
		bucket string
	}{
		{0, "le5s"},
		{5 * time.Second, "le5s"},
		{5*time.Second + time.Millisecond, "le15s"},
		{15 * time.Second, "le15s"},
		{15*time.Second + time.Millisecond, "le30s"},
		{30 * time.Second, "le30s"},
		{30*time.Second + time.Millisecond, "le60s"},
		{time.Minute, "le60s"},
		{time.Minute + time.Millisecond, "gt60s"},
	} {
		if got := InPlaceWaitBucket(test.total); got != test.bucket {
			t.Fatalf("InPlaceWaitBucket(%s) = %q, want %q", test.total, got, test.bucket)
		}
	}
}
