//go:build alarmd_measure

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import "time"

// SetWindowBoundPercentForMeasurement bounds every window at percent of the
// share, 1 to 99, or at the whole share for 0, and returns what it was. It
// is only in the alarmd_measure build: a measurement reads with it how the
// collector paces when the windows leave the soft limit room, and no
// configuration reaches it.
func SetWindowBoundPercentForMeasurement(percent uint32) uint32 {
	return windowBoundPercent.Swap(min(percent, 99))
}

// SetSliceReadAheadOffForMeasurement reads every slice on the Slot's
// goroutine, as before the read ahead, while off, and returns what it was.
// Only in the alarmd_measure build, for the reading against the read ahead.
func SetSliceReadAheadOffForMeasurement(off bool) bool {
	return sliceReadAheadOff.Swap(off)
}

// SetSliceReadWidthForMeasurement reads width slices of a Slot at once in
// place of the width the permits give, zero for theirs, and returns what it
// was. Only in the alarmd_measure build, for the reading of each width.
func SetSliceReadWidthForMeasurement(width int32) int32 {
	return sliceReadWidthForMeasurement.Swap(max(width, 0))
}

// SetWindowSizesForMeasurement has note told each window's bytes and series
// as the window is let go, nil for none, and returns what it was. Only in the
// alarmd_measure build, for the reading of the windows' sizes.
func SetWindowSizesForMeasurement(note func(bytes uint64, series int)) func(bytes uint64, series int) {
	var next *func(bytes uint64, series int)
	if note != nil {
		next = &note
	}
	if previous := windowSizesForMeasurement.Swap(next); previous != nil {
		return *previous
	}
	return nil
}

// SetReadAheadWaitsForMeasurement has note told how long each wait of a read
// ahead for room lasted, as it ends, nil for none, and returns what it was.
// Only in the alarmd_measure build, for the reading of how long reads ahead
// held their queries unread.
func SetReadAheadWaitsForMeasurement(note func(waited time.Duration)) func(waited time.Duration) {
	var next *func(waited time.Duration)
	if note != nil {
		next = &note
	}
	if previous := readAheadWaitsForMeasurement.Swap(next); previous != nil {
		return *previous
	}
	return nil
}
