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
	"math"
	"strings"
	"testing"
)

// The deriver is DeriveRecordIDV2 with the series fixed: the same bytes for
// every time, including each width a time's decimal text can have, and after
// any number of earlier points -- the hash state it resumes from must not
// carry anything from the point before.
func TestTheRecordIDDeriverGivesTheOneShotDerivationsIDs(t *testing.T) {
	series := []string{
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	}
	times := []int64{0, 1, 9, 10, 59, 60, 99, 100, 1790129340, 1<<31 - 1, 1 << 31, 1<<62 + 7, math.MaxInt64}
	for _, digest := range series {
		deriver, err := NewRecordIDDeriverV2(digest)
		if err != nil {
			t.Fatal(err)
		}
		// Twice over, forwards then backwards, on the one deriver.
		for pass := 0; pass < 2; pass++ {
			for index := range times {
				at := times[index]
				if pass == 1 {
					at = times[len(times)-1-index]
				}
				want, err := DeriveRecordIDV2(digest, at)
				if err != nil {
					t.Fatal(err)
				}
				got, err := deriver.Derive(at)
				if err != nil || got != want {
					t.Fatalf("series %s at %d: deriver gave (%s, %v), one-shot gave %s", digest[:8], at, got, err, want)
				}
			}
		}
	}
}

// Refused as the one-shot derivation refuses: a series that is not a digest
// when the deriver is built, a negative time when it is asked.
func TestTheRecordIDDeriverRefusesWhatTheOneShotDerivationRefuses(t *testing.T) {
	for _, digest := range []string{"", "ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789", "0123"} {
		if _, oneShot := DeriveRecordIDV2(digest, 60); oneShot == nil {
			t.Fatalf("setup: the one-shot derivation accepts %q", digest)
		}
		if _, err := NewRecordIDDeriverV2(digest); err == nil {
			t.Fatalf("the deriver accepted series %q the one-shot derivation refuses", digest)
		}
	}
	deriver, err := NewRecordIDDeriverV2("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deriver.Derive(-1); err == nil {
		t.Fatal("the deriver accepted a negative source time")
	}
}

func BenchmarkRecordIDOneShot(b *testing.B) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_, _ = DeriveRecordIDV2(digest, int64(1790000000+60*index))
	}
}

func BenchmarkRecordIDDeriver(b *testing.B) {
	deriver, err := NewRecordIDDeriverV2("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_, _ = deriver.Derive(int64(1790000000 + 60*index))
	}
}

// A point's id costs the id and nothing else: the hash's buffers belong to
// the deriver, not to each call. Declared per call they escaped through the
// hash interface and were allocated for every point of every record read.
func TestDerivingAPointsIDAllocatesOnlyTheID(t *testing.T) {
	deriver, err := NewRecordIDDeriverV2(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	at := int64(1756684800)
	if allocs := testing.AllocsPerRun(100, func() {
		at += 60
		if _, err := deriver.Derive(at); err != nil {
			t.Fatal(err)
		}
	}); allocs > 1 {
		t.Fatalf("Derive allocated %.0f times a point, want only the id", allocs)
	}
}
