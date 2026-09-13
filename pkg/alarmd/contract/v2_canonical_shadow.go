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
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// The four states a rollout of the single-pass canonical form passes through,
// held as two independent bits so that an unreachable combination cannot be
// named:
//
//	established    the established form answers, nothing compares
//	shadow         the established form answers, the single-pass form compares
//	stream_shadow  the single-pass form answers, the established form compares
//	stream         the single-pass form answers, nothing compares
//
// Both comparison directions have to be proven, and they are different claims.
// Forward says the two agree on what production sends today. Reverse says they
// agree on what production sends once stored digests are coming from the new
// path -- which is when the callers' own inputs start depending on it. A first
// cut of this change compared only forward and silently skipped the comparison
// whenever the new path was authoritative, which made the reverse claim
// unprovable while looking like it was covered.
const (
	canonicalBitServeStream uint32 = 1 << iota
	canonicalBitCompare
)

type canonicalMode uint32

func (m canonicalMode) servesStream() bool { return uint32(m)&canonicalBitServeStream != 0 }
func (m canonicalMode) compares() bool     { return uint32(m)&canonicalBitCompare != 0 }

func (m canonicalMode) String() string {
	switch {
	case m.servesStream() && m.compares():
		return CanonicalModeStreamShadow
	case m.servesStream():
		return CanonicalModeStream
	case m.compares():
		return CanonicalModeShadow
	default:
		return CanonicalModeEstablished
	}
}

// Mode names as they appear in configuration.
const (
	CanonicalModeEstablished  = "established"
	CanonicalModeShadow       = "shadow"
	CanonicalModeStreamShadow = "stream_shadow"
	CanonicalModeStream       = "stream"
)

var canonicalModeBits atomic.Uint32

func loadCanonicalMode() canonicalMode { return canonicalMode(canonicalModeBits.Load()) }

// CanonicalModeNames lists the accepted values, so that configuration
// validation and its error message cannot drift apart from this file.
func CanonicalModeNames() []string {
	return []string{CanonicalModeEstablished, CanonicalModeShadow, CanonicalModeStreamShadow, CanonicalModeStream}
}

// SetCanonicalMode selects a rollout state by name and reports the previous
// one. An unknown name changes nothing.
func SetCanonicalMode(name string) (string, error) {
	var bits uint32
	switch name {
	case CanonicalModeEstablished:
	case CanonicalModeShadow:
		bits = canonicalBitCompare
	case CanonicalModeStreamShadow:
		bits = canonicalBitServeStream | canonicalBitCompare
	case CanonicalModeStream:
		bits = canonicalBitServeStream
	default:
		return loadCanonicalMode().String(), errors.New("alarmd contract: unknown canonical mode " + name)
	}
	return canonicalMode(canonicalModeBits.Swap(bits)).String(), nil
}

// CanonicalMode reports the current rollout state by name.
func CanonicalMode() string { return loadCanonicalMode().String() }

func setCanonicalBit(bit uint32, on bool) bool {
	for {
		current := canonicalModeBits.Load()
		next := current &^ bit
		if on {
			next = current | bit
		}
		if canonicalModeBits.CompareAndSwap(current, next) {
			return current&bit != 0
		}
	}
}

// SetCanonicalStreamEnabled makes the single-pass form authoritative, and
// SetCanonicalStreamShadow turns the comparison on. They set the two bits
// independently, so setting both is the reverse direction rather than a
// contradiction.
func SetCanonicalStreamEnabled(enabled bool) bool {
	return setCanonicalBit(canonicalBitServeStream, enabled)
}

func SetCanonicalStreamShadow(enabled bool) bool {
	return setCanonicalBit(canonicalBitCompare, enabled)
}

// Sampling. Running both forms on every call doubles the work this change
// exists to remove, so the comparison covers one call in every stride. The
// counter is deterministic rather than random: a reproducible sample is worth
// more than an unbiased one here, because a divergence has to be findable
// again after it is reported.
//
// Zero means never compare, and is what a mode without comparison leaves it
// at. One means compare everything.
var (
	canonicalShadowStride atomic.Uint64
	canonicalShadowTick   atomic.Uint64
)

// SetCanonicalShadowStride sets how often the comparison runs: one call in
// every stride. It reports the previous value.
func SetCanonicalShadowStride(stride uint64) uint64 {
	return canonicalShadowStride.Swap(stride)
}

func shouldSampleCanonicalShadow() bool {
	stride := canonicalShadowStride.Load()
	switch stride {
	case 0:
		return false
	case 1:
		return true
	}
	return canonicalShadowTick.Add(1)%stride == 0
}

var (
	canonicalStreamServed   atomic.Uint64
	canonicalStreamDeclined atomic.Uint64

	canonicalShadowCompared atomic.Uint64
	canonicalShadowAgreed   atomic.Uint64
	canonicalShadowDeclined atomic.Uint64
	canonicalShadowBytes    atomic.Uint64
	canonicalShadowVerdict  atomic.Uint64
	canonicalShadowPanic    atomic.Uint64
)

// CanonicalStreamCounts reports how many calls the single-pass form answered
// and how many it handed back.
func CanonicalStreamCounts() (served, declined uint64) {
	return canonicalStreamServed.Load(), canonicalStreamDeclined.Load()
}

// CanonicalShadowCounts reports the comparison tallies.
//
// Declined is reported beside Agreed on purpose. An input the shadow hands
// back is not evidence that the two agree on it, and folding it into agreement
// is how a comparison that had stopped covering anything would still show a
// clean sheet. The pair Compared and Agreed answers "how much was checked";
// the three divergence counters answer "what was wrong"; neither question can
// be answered from the other's numbers.
type CanonicalShadowCounts struct {
	Mode                       string
	Stride                     uint64
	StreamServed               uint64
	StreamDeclined             uint64
	Compared, Agreed, Declined uint64
	BytesDiffer, VerdictDiffer uint64
	PanicDiffer                uint64
}

func ReadCanonicalShadowCounts() CanonicalShadowCounts {
	served, declined := CanonicalStreamCounts()
	return CanonicalShadowCounts{
		Mode:           CanonicalMode(),
		Stride:         canonicalShadowStride.Load(),
		StreamServed:   served,
		StreamDeclined: declined,
		Compared:       canonicalShadowCompared.Load(),
		Agreed:         canonicalShadowAgreed.Load(),
		Declined:       canonicalShadowDeclined.Load(),
		BytesDiffer:    canonicalShadowBytes.Load(),
		VerdictDiffer:  canonicalShadowVerdict.Load(),
		PanicDiffer:    canonicalShadowPanic.Load(),
	}
}

// canonicalShadowFingerprint identifies a divergence without carrying any
// payload out of the process. There are no key names and no values: the
// container chain records only the kinds of container the offset sits inside,
// which is enough to find the code path and not enough to leak a dimension.
type canonicalShadowFingerprint struct {
	Class      string
	Direction  string
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

// canonicalShadowInput carries one comparison. Served is the answer the caller
// is about to receive; Compared is the other form's answer, which is discarded
// whatever it says.
type canonicalShadowInput struct {
	GoType           string
	Direction        string
	Raw              []byte
	Served           []byte
	ServedErr        error
	Compared         []byte
	ComparedErr      error
	ComparedDeclined bool
}

// compareCanonicalShadow records how the two forms differed on one input. It
// returns nothing: the caller's answer is already decided and is not touched
// here whatever happens.
//
// A panic in the comparison is caught and counted. A shadow that could take
// the process down would be a strictly worse trade than not running it, since
// the point is to learn without risking the answer.
func compareCanonicalShadow(in canonicalShadowInput) {
	defer func() {
		if recovered := recover(); recovered != nil {
			canonicalShadowPanic.Add(1)
			recordCanonicalShadowSample(canonicalShadowSample{
				canonicalShadowFingerprint: canonicalShadowFingerprint{
					Class: "panic_differ", Direction: in.Direction, GoType: in.GoType, OffsetKind: "n/a",
				},
				InputLen: len(in.Raw),
				Old:      fmt.Sprint(recovered),
			})
		}
	}()
	canonicalShadowCompared.Add(1)
	if in.ComparedDeclined {
		// The single-pass form handed the input back. That is neither
		// agreement nor divergence: the established path stayed in charge,
		// which it would have done anyway.
		canonicalShadowDeclined.Add(1)
		return
	}
	servedRejected := in.ServedErr != nil
	comparedRejected := in.ComparedErr != nil
	if servedRejected != comparedRejected {
		canonicalShadowVerdict.Add(1)
		kind := "rejected-what-was-accepted"
		if comparedRejected {
			// The served answer accepted input the other form refuses. In the
			// reverse direction that is the silent one: the query succeeds and
			// nobody reports it.
			kind = "accepted-what-was-rejected"
		}
		recordCanonicalShadowSample(canonicalShadowSample{
			canonicalShadowFingerprint: canonicalShadowFingerprint{
				Class: "verdict_differ", Direction: in.Direction, GoType: in.GoType, OffsetKind: kind,
			},
			InputLen: len(in.Raw),
		})
		return
	}
	if servedRejected {
		canonicalShadowAgreed.Add(1) // both refuse it
		return
	}
	if string(in.Served) == string(in.Compared) {
		canonicalShadowAgreed.Add(1)
		return
	}
	canonicalShadowBytes.Add(1)
	offset := firstDifferingOffset(in.Served, in.Compared)
	recordCanonicalShadowSample(canonicalShadowSample{
		canonicalShadowFingerprint: canonicalShadowFingerprint{
			Class:      "bytes_differ",
			Direction:  in.Direction,
			GoType:     in.GoType,
			Chain:      canonicalContainerChain(in.Served, offset),
			OffsetKind: canonicalOffsetKind(in.Served, offset),
		},
		InputLen: len(in.Raw),
		Offset:   offset,
		Old:      byteAt(in.Served, offset),
		New:      byteAt(in.Compared, offset),
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
// hit it, which is what keeps the sample table bounded.
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
