// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "time"

// SuggestedTimeDelaySeconds is the time_delay a group's data needs, in whole
// seconds rounded up to the unit its time_delay is written in (none for a
// unit under a second); zero when the current one already covers it.
//
// need is the time_delay at which the data would have been whole by the
// group's first read: how long after the window's end it was whole, less the
// settling wait every read waits beyond the time_delay. Each reader measures
// it its own way - from the window's arrival age, or from how long after a
// held first read the data or a late series came, plus the hold that read
// waited - and every suggestion is this one function of it, so a group gets
// one advice whichever row it is read from.
func SuggestedTimeDelaySeconds(need, current, unit time.Duration) int64 {
	if need <= current {
		return 0
	}
	seconds := int64((need + time.Second - 1) / time.Second)
	if step := int64(unit / time.Second); step > 0 {
		seconds = (seconds + step - 1) / step * step
	}
	return seconds
}
