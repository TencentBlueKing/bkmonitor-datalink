// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "sync/atomic"

// canonicalStreamServed and canonicalStreamDeclined count canonical
// encodings by what the single-pass form did with them: answered, or handed
// back to the established path. A rising declined count is the single-pass
// form refusing inputs it used to take.
var (
	canonicalStreamServed   atomic.Uint64
	canonicalStreamDeclined atomic.Uint64
)

// CanonicalStreamCounts reports how many calls the single-pass form answered
// and how many it handed back.
func CanonicalStreamCounts() (served, declined uint64) {
	return canonicalStreamServed.Load(), canonicalStreamDeclined.Load()
}
