//go:build !alarmd_measure

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import "time"

// windowMeasureSetWindowBound is without the knob outside the alarmd_measure
// build: it sets nothing and says so.
func windowMeasureSetWindowBound(uint32) (previous uint32, ok bool) { return 0, false }

// windowMeasureSetSliceReadAheadOff is without the knob outside the
// alarmd_measure build.
func windowMeasureSetSliceReadAheadOff(bool) (previous bool, ok bool) { return false, false }

// windowMeasureSetSliceReadWidth is without the knob outside the
// alarmd_measure build.
func windowMeasureSetSliceReadWidth(int32) (previous int32, ok bool) { return 0, false }

// windowMeasureSetWindowSizes is without the hook outside the alarmd_measure
// build.
func windowMeasureSetWindowSizes(func(uint64, int)) (previous func(uint64, int), ok bool) {
	return nil, false
}

// windowMeasureSetReadAheadWaits is without the hook outside the
// alarmd_measure build.
func windowMeasureSetReadAheadWaits(func(time.Duration)) (previous func(time.Duration), ok bool) {
	return nil, false
}
