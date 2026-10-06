// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"fmt"
	"testing"
)

func BenchmarkCodecWindow(b *testing.B) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 8, MaxPoints: 256, MaxEncodedBytes: 64 << 10})
	if err != nil {
		b.Fatal(err)
	}
	window := benchmarkWindow(b, 8, 120)
	blob, err := codec.Encode(window)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(blob)), "blob_bytes")
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, encodeErr := codec.Encode(window); encodeErr != nil {
				b.Fatal(encodeErr)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, decodeErr := codec.Decode(blob); decodeErr != nil {
				b.Fatal(decodeErr)
			}
		}
	})
}

func BenchmarkWindowApplyAndSummarize(b *testing.B) {
	requirement := requirement(5, "5", 30, 60)
	points := make([]StatePoint, 60)
	for position := range points {
		points[position] = StatePoint{
			RecordID: fmt.Sprintf("%064x", position+1), SourceTime: int64(100 + position*60),
			Levels: []PointLevelFact{fact(requirement, LevelFactResult(position%2+1))},
		}
	}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		window, err := NewWindow([]LevelRequirement{requirement})
		if err != nil {
			b.Fatal(err)
		}
		mustApply(b, window, points)
		history, _ := window.History(5)
		_ = history.Summarize(100+59*60, 30)
	}
}

func benchmarkWindow(b *testing.B, levels, points int) *Window {
	b.Helper()
	window := &Window{levels: make([]levelState, levels), points: make([]pointState, points)}
	bitmapBytes := (levels + 7) / 8
	for index := 0; index < levels; index++ {
		fingerprint, err := decodeDigest32(fmt.Sprintf("%064x", index+1))
		if err != nil {
			b.Fatal(err)
		}
		window.levels[index] = levelState{levelID: uint32(index + 1), detectFingerprint: fingerprint}
	}
	for index := 0; index < points; index++ {
		digest, err := decodeDigest32(fmt.Sprintf("%064x", index+1))
		if err != nil {
			b.Fatal(err)
		}
		window.points[index] = pointState{
			sourceTime: int64(100 + index*60), valid: make([]byte, bitmapBytes), anomalous: make([]byte, bitmapBytes),
		}
		copy(window.points[index].recordDigest[:], digest[:16])
		for level := 0; level < levels; level++ {
			setBit(window.points[index].valid, level, true)
			setBit(window.points[index].anomalous, level, (index+level)%3 == 0)
		}
	}
	return window
}
