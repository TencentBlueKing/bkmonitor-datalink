// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"encoding/base64"
	"math/bits"
)

// SelectorIndexViewV2 iterates indexes into the shared RecordBatch. It never
// copies record bodies; BITMAP uses the wire contract's LSB-first bit order.
type SelectorIndexViewV2 struct {
	selector    SelectorV2
	recordCount int
	bitmap      []byte
	length      int
}

func NewSelectorIndexViewV2(selector SelectorV2, recordCount int) (SelectorIndexViewV2, error) {
	if recordCount < 0 || !validSelectorV2(selector, recordCount) {
		return SelectorIndexViewV2{}, invalid("selector", "does not select valid RecordBatch indexes")
	}
	view := SelectorIndexViewV2{selector: selector, recordCount: recordCount}
	if selector.Kind == SelectorKindRanges {
		for _, item := range *selector.Ranges {
			view.length += int(item.End - item.Start)
		}
		return view, nil
	}
	view.bitmap, _ = base64.StdEncoding.Strict().DecodeString(selector.BitmapB64)
	for _, value := range view.bitmap {
		view.length += bits.OnesCount8(value)
	}
	return view, nil
}

func (view SelectorIndexViewV2) Len() int {
	return view.length
}

func (view SelectorIndexViewV2) ForEach(visitor func(recordIndex uint32) bool) {
	if view.selector.Kind == SelectorKindRanges {
		for _, item := range *view.selector.Ranges {
			for index := item.Start; index < item.End; index++ {
				if !visitor(index) {
					return
				}
			}
		}
		return
	}
	for index := 0; index < view.recordCount; index++ {
		if view.bitmap[index/8]&(1<<uint(index%8)) != 0 && !visitor(uint32(index)) {
			return
		}
	}
}
