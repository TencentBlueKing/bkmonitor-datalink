// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import "testing"

// BenchmarkBuildStateMutationByPoints measures the cost of deriving one
// series' mutation digest against how many points the mutation names.
//
// It is here because that number is the whole reason the mutation stopped
// naming the window: a Plan retaining 1469 points derived its digest over all
// 1469 of them every round, for every series, and then three more contract
// checks asked for it again. The canonical encoder is the expensive part - it
// encodes, rescans, decodes into generic values and re-encodes with sorted
// keys - and it is paid per point.
//
// Run as: go test ./execution/ -run '^$' -bench BuildStateMutationByPoints
// -benchmem. The interesting pair is 1469 against 1, which is what a round
// costs before and after: the same Plan, the same retained window, one naming
// the window and one naming what the round added.
func BenchmarkBuildStateMutationByPoints(b *testing.B) {
	for _, points := range []int{1, 2, 30, 1469} {
		b.Run(benchName(points), func(b *testing.B) {
			mutation := sealTestMutation(points)
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				built, err := BuildStateMutation(mutation)
				if err != nil {
					b.Fatalf("build: %v", err)
				}
				if built.MutationDigest == "" {
					b.Fatal("no digest")
				}
			}
		})
	}
}

func benchName(points int) string {
	switch points {
	case 1:
		return "one_point_the_round_added"
	case 1469:
		return "the_whole_retained_window"
	default:
		return "points_" + itoa(points)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
