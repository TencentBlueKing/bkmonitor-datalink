// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Shadow mode runs the single-pass form alongside the established one and
// reports where they differ, while the established one keeps answering. It is
// the only way to learn what production inputs actually look like: the pinned
// table and the fuzz corpus both cover what somebody thought of, and the
// rejection boundary is exactly where an unthought-of input would land.
//
// The comparison serves the established bytes in every case, including when
// the single-pass form panics, so turning shadow on cannot change an answer.
// The reverse direction -- new authoritative, old shadow -- is the same switch
// with canonicalStreamEnabled on, and has to be proven separately, because
// agreeing on the inputs production happens to send is not the same claim as
// agreeing on the inputs it sends after the switch changes which of the two is
// deciding what gets stored.

var canonicalStreamShadow atomic.Bool

// SetCanonicalStreamShadow turns the comparison on or off and reports what it
// was.
func SetCanonicalStreamShadow(enabled bool) bool {
	return canonicalStreamShadow.Swap(enabled)
}

// Divergence classes. Kept as three separate counters rather than one with a
// label so that a class nobody has seen still reads as zero rather than as a
// missing series.
var (
	canonicalShadowCompared atomic.Uint64
	canonicalShadowAgreed   atomic.Uint64
	canonicalShadowDeclined atomic.Uint64
	canonicalShadowBytes    atomic.Uint64
	canonicalShadowVerdict  atomic.Uint64
	canonicalShadowPanic    atomic.Uint64
)

// CanonicalShadowCounts reports the comparison tallies for the observation
// layer. Declined is reported beside agreed on purpose: an input the
// single-pass form hands back is not evidence that the two agree on it, and
// counting it as agreement is how a switch that had stopped doing anything
// would still show a clean sheet.
type CanonicalShadowCounts struct {
	Compared, Agreed, Declined uint64
	BytesDiffer, VerdictDiffer uint64
	PanicDiffer                uint64
}

func ReadCanonicalShadowCounts() CanonicalShadowCounts {
	return CanonicalShadowCounts{
		Compared:      canonicalShadowCompared.Load(),
		Agreed:        canonicalShadowAgreed.Load(),
		Declined:      canonicalShadowDeclined.Load(),
		BytesDiffer:   canonicalShadowBytes.Load(),
		VerdictDiffer: canonicalShadowVerdict.Load(),
		PanicDiffer:   canonicalShadowPanic.Load(),
	}
}

// canonicalShadowFingerprint identifies a divergence without carrying any
// payload out of the process. There are no key names and no values: the
// container chain records only the kinds of container the offset sits inside,
// which is enough to find the code path and not enough to leak a dimension.
type canonicalShadowFingerprint struct {
	Class      string
	GoType     string
	Chain      string
	OffsetKind string
}

type canonicalShadowSample struct {
	canonicalShadowFingerprint
	InputLen int
	Offset   int
	Old, New string
	Count    uint64
}

const canonicalShadowSampleCap = 64

var (
	canonicalShadowMu      sync.Mutex
	canonicalShadowSamples = map[canonicalShadowFingerprint]*canonicalShadowSample{}
)

// ReadCanonicalShadowSamples returns the deduplicated divergences seen so far,
// most frequent first.
func ReadCanonicalShadowSamples() []canonicalShadowSample {
	canonicalShadowMu.Lock()
	defer canonicalShadowMu.Unlock()
	out := make([]canonicalShadowSample, 0, len(canonicalShadowSamples))
	for _, sample := range canonicalShadowSamples {
		out = append(out, *sample)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func recordCanonicalShadowSample(sample canonicalShadowSample) {
	canonicalShadowMu.Lock()
	defer canonicalShadowMu.Unlock()
	if existing, ok := canonicalShadowSamples[sample.canonicalShadowFingerprint]; ok {
		existing.Count++
		return
	}
	if len(canonicalShadowSamples) >= canonicalShadowSampleCap {
		return
	}
	sample.Count = 1
	canonicalShadowSamples[sample.canonicalShadowFingerprint] = &sample
}

// compareCanonicalShadow runs the single-pass form over the same bytes and
// records how it differed from the answer already produced. It never returns
// anything: the caller's result is unchanged whatever happens here.
//
// A panic in the shadow is caught and counted. A shadow that could take the
// process down would be a strictly worse trade than not running it, since the
// whole point is to learn without risking the answer.
func compareCanonicalShadow(goType string, raw []byte, established []byte, establishedErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			canonicalShadowPanic.Add(1)
			recordCanonicalShadowSample(canonicalShadowSample{
				canonicalShadowFingerprint: canonicalShadowFingerprint{
					Class: "panic_differ", GoType: goType, OffsetKind: "n/a",
				},
				InputLen: len(raw),
				Old:      fmt.Sprint(recovered),
			})
		}
	}()
	canonicalShadowCompared.Add(1)
	out, ok := canonicalStreamV2(make([]byte, 0, len(raw)), raw)
	if !ok {
		// Declined. That is neither agreement nor divergence: it means the
		// established path stayed in charge, which it would have done anyway.
		// Counted separately so the coverage figure cannot be read off the
		// agreement figure alone.
		canonicalShadowDeclined.Add(1)
		return
	}
	if establishedErr != nil {
		// The single-pass form produced bytes for input the established path
		// refuses. This is the direction that is silent in production: the
		// query succeeds and nobody reports it.
		canonicalShadowVerdict.Add(1)
		recordCanonicalShadowSample(canonicalShadowSample{
			canonicalShadowFingerprint: canonicalShadowFingerprint{
				Class: "verdict_differ", GoType: goType, OffsetKind: "accepted-what-was-rejected",
			},
			InputLen: len(raw),
		})
		return
	}
	if string(out) == string(established) {
		canonicalShadowAgreed.Add(1)
		return
	}
	canonicalShadowBytes.Add(1)
	offset := firstDifferingOffset(established, out)
	recordCanonicalShadowSample(canonicalShadowSample{
		canonicalShadowFingerprint: canonicalShadowFingerprint{
			Class:      "bytes_differ",
			GoType:     goType,
			Chain:      canonicalContainerChain(established, offset),
			OffsetKind: canonicalOffsetKind(established, offset),
		},
		InputLen: len(raw),
		Offset:   offset,
		Old:      byteAt(established, offset),
		New:      byteAt(out, offset),
	})
}

func firstDifferingOffset(left, right []byte) int {
	limit := min(len(left), len(right))
	for index := range limit {
		if left[index] != right[index] {
			return index
		}
	}
	return limit
}

func byteAt(payload []byte, offset int) string {
	if offset >= len(payload) {
		return "eof"
	}
	return fmt.Sprintf("%02x", payload[offset])
}

// canonicalContainerChain describes the containers the offset sits inside, as
// a string of o and a with no key names in it. A divergence at the same depth
// in the same shape of container is the same finding however many Query Groups
// hit it, which is what makes the sample table bounded.
func canonicalContainerChain(payload []byte, offset int) string {
	chain := make([]byte, 0, 16)
	inString := false
	for index := 0; index < offset && index < len(payload); index++ {
		switch character := payload[index]; {
		case inString:
			if character == '\\' {
				index++
			} else if character == '"' {
				inString = false
			}
		case character == '"':
			inString = true
		case character == '{':
			chain = append(chain, 'o')
		case character == '[':
			chain = append(chain, 'a')
		case character == '}' || character == ']':
			if len(chain) > 0 {
				chain = chain[:len(chain)-1]
			}
		}
	}
	if len(chain) == 0 {
		return "root"
	}
	return string(chain)
}

// canonicalOffsetKind says what sort of position diverged, so that "the
// escaping of a string" and "the spelling of a number" do not collapse into
// one finding just because they happened at the same depth.
func canonicalOffsetKind(payload []byte, offset int) string {
	if offset >= len(payload) {
		return "length"
	}
	switch payload[offset] {
	case '{', '}', '[', ']', ',', ':':
		return "structure"
	case '"':
		return "string-boundary"
	case '\\':
		return "escape"
	case 't', 'f', 'n':
		return "literal"
	}
	if payload[offset] >= '0' && payload[offset] <= '9' ||
		payload[offset] == '-' || payload[offset] == '+' ||
		payload[offset] == '.' || payload[offset] == 'e' || payload[offset] == 'E' {
		return "number"
	}
	return "string-content"
}
