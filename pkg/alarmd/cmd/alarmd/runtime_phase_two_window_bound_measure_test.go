//go:build alarmd_measure

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// windowMeasureSetWindowBound bounds the windows at percent of the share for
// the shapes that follow, and says it could: the build has the knob.
func windowMeasureSetWindowBound(percent uint32) (previous uint32, ok bool) {
	return worker.SetWindowBoundPercentForMeasurement(percent), true
}

// windowMeasureSetSliceReadAheadOff reads every slice on the Slot's goroutine
// while off, for the reading against the read ahead.
func windowMeasureSetSliceReadAheadOff(off bool) (previous bool, ok bool) {
	return worker.SetSliceReadAheadOffForMeasurement(off), true
}

// windowMeasureSetSliceReadWidth reads width slices at once, zero for the
// permits' width.
func windowMeasureSetSliceReadWidth(width int32) (previous int32, ok bool) {
	return worker.SetSliceReadWidthForMeasurement(width), true
}

// windowMeasureSetWindowSizes has note told each window's bytes and series
// as it is let go, nil for none.
func windowMeasureSetWindowSizes(note func(bytes uint64, series int)) (previous func(bytes uint64, series int), ok bool) {
	return worker.SetWindowSizesForMeasurement(note), true
}

// windowMeasureSetReadAheadWaits has note told how long each wait of a read
// ahead for room lasted, nil for none.
func windowMeasureSetReadAheadWaits(note func(waited time.Duration)) (previous func(waited time.Duration), ok bool) {
	return worker.SetReadAheadWaitsForMeasurement(note), true
}
