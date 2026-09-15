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
	"bytes"
	"math"
	"sort"
	"strconv"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// The canonical form of a JSON payload in one pass over its bytes.
//
// The path this replaces walks the same bytes three times: once to reject
// duplicate fields, once to decode into map[string]any with UseNumber, and once
// to re-encode with sorted keys. A production CPU profile put 50.74% of the
// process in that function and 61.04% of its allocation, with the two largest
// allocation sites being the decoder's own buffer refill and the boxing of
// every decoded value into an interface. Neither is inherent to the result: the
// output is a function of the input bytes, and nothing in between has to become
// a Go value first.
//
// This is an execution change only. The digest definition does not move, which
// is why the escape table below is transcribed from the encoder rather than
// designed, and why every branch of it is pinned by the generated table in
// canonical_branch_table_test.go before this file existed.
//
// Escaping follows encoding/json's appendString with escapeHTML false. Checked
// byte for byte against Go 1.23.12, which is what production runs, and Go
// 1.26.2, which is what this repository builds with locally: all ninety pinned
// branches agree across both. That agreement is a measurement, not a guarantee
// -- and pinning the table here is what turns the canonical form from a
// property of whichever toolchain compiled the binary into a property of this
// package.

// canonicalStreamV2 appends the canonical form of one JSON value to dst.
//
// It reports ok false for any input it does not fully accept, without having
// written anything the caller must undo, and without producing an error of its
// own. The caller falls back to the established path, which then produces the
// rejection exactly as it always did. Hand-matching encoding/json's error
// strings would be a second place for the two paths to disagree, in exchange
// for saving work on inputs that are supposed to be rare; a bail here costs one
// wasted pass over bytes that were about to be rejected anyway.
//
// Bails are counted by the caller, because a silent fallback and a genuine
// agreement look identical from outside, and a switch that quietly stopped
// doing anything would still report zero divergence.
func canonicalStreamV2(dst, src []byte) (out []byte, ok bool) {
	stream := canonicalStreamPool.Get().(*canonicalStream)
	stream.src, stream.pos = src, 0
	// Returned in every path. The buffers it carries are the point of the pool
	// and none of them alias the result: keys and values are copied into dst
	// as they are emitted, and the decoded scratch is copied by the escaper.
	defer func() {
		stream.src = nil
		canonicalStreamPool.Put(stream)
	}()
	stream.skipSpace()
	dst, ok = stream.value(dst, 0)
	if !ok {
		return nil, false
	}
	stream.skipSpace()
	if stream.pos != len(stream.src) {
		return nil, false
	}
	return dst, true
}

// canonicalStreamMaxDepth bounds recursion. The established path is bounded by
// the decoder's own nesting limit; this one has to say so itself.
const canonicalStreamMaxDepth = 512

type canonicalStream struct {
	src []byte
	pos int
	// scratch holds one decoded string value at a time. Keys do not use it:
	// they have to outlive each other to be sorted, so they go in the level
	// arena below.
	scratch []byte
	// levels is one working set per nesting depth, reused across sibling
	// objects and across calls. Parsing is depth first, so at any moment only
	// one object per depth is being built and the sets cannot collide.
	//
	// Indexed rather than held by pointer on purpose: a nested call can grow
	// this slice, which would leave a held pointer addressing the old array.
	levels []canonicalLevel
}

// canonicalLevel is the working set for one object: its members, an arena
// holding their decoded keys end to end, and a buffer holding their
// canonicalised values end to end.
type canonicalLevel struct {
	members []canonicalMember
	keys    []byte
	values  []byte
}

// canonicalStreamPool keeps those working sets alive between calls. Without it
// every call rebuilds them from nothing, and this runs once per series.
var canonicalStreamPool = sync.Pool{New: func() any { return new(canonicalStream) }}

func (s *canonicalStream) skipSpace() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}

func (s *canonicalStream) value(dst []byte, depth int) ([]byte, bool) {
	if depth > canonicalStreamMaxDepth || s.pos >= len(s.src) {
		return nil, false
	}
	switch s.src[s.pos] {
	case '{':
		return s.object(dst, depth)
	case '[':
		return s.array(dst, depth)
	case '"':
		decoded, ok := s.str()
		if !ok {
			return nil, false
		}
		return appendCanonicalStringV2(dst, decoded), true
	case 't':
		return s.literal(dst, "true")
	case 'f':
		return s.literal(dst, "false")
	case 'n':
		return s.literal(dst, "null")
	default:
		return s.number(dst)
	}
}

func (s *canonicalStream) literal(dst []byte, want string) ([]byte, bool) {
	if s.pos+len(want) > len(s.src) || string(s.src[s.pos:s.pos+len(want)]) != want {
		return nil, false
	}
	s.pos += len(want)
	return append(dst, want...), true
}

// number emits the token exactly as written. Preserving the token rather than a
// parsed value is the whole reason the established path decodes with
// UseNumber: a large integer or a trailing zero survives only as text.
func (s *canonicalStream) number(dst []byte) ([]byte, bool) {
	start := s.pos
	if s.pos < len(s.src) && s.src[s.pos] == '-' {
		s.pos++
	}
	// Integer part: a single zero, or a non-zero digit followed by digits.
	// A leading zero is rejected, which is what makes "01" invalid JSON.
	if s.pos >= len(s.src) {
		return nil, false
	}
	if s.src[s.pos] == '0' {
		s.pos++
	} else if s.src[s.pos] >= '1' && s.src[s.pos] <= '9' {
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
		}
	} else {
		return nil, false
	}
	inexact := false
	if s.pos < len(s.src) && s.src[s.pos] == '.' {
		inexact = true
		s.pos++
		digits := 0
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
			digits++
		}
		if digits == 0 {
			return nil, false
		}
	}
	if s.pos < len(s.src) && (s.src[s.pos] == 'e' || s.src[s.pos] == 'E') {
		inexact = true
		s.pos++
		if s.pos < len(s.src) && (s.src[s.pos] == '+' || s.src[s.pos] == '-') {
			s.pos++
		}
		digits := 0
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
			digits++
		}
		if digits == 0 {
			return nil, false
		}
	}
	token := s.src[start:s.pos]
	// The established path rejects a non-finite number, but only for a token
	// that carries a decimal point or an exponent: its guard is the presence of
	// one of . e E, so an integer of any length never reaches the check. That
	// asymmetry is behaviour, not an accident to tidy up here.
	if inexact {
		parsed, err := strconv.ParseFloat(string(token), 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return nil, false
		}
	}
	return append(dst, token...), true
}

func (s *canonicalStream) array(dst []byte, depth int) ([]byte, bool) {
	s.pos++ // consume [
	dst = append(dst, '[')
	s.skipSpace()
	if s.pos < len(s.src) && s.src[s.pos] == ']' {
		s.pos++
		return append(dst, ']'), true
	}
	for {
		s.skipSpace()
		var ok bool
		dst, ok = s.value(dst, depth+1)
		if !ok {
			return nil, false
		}
		s.skipSpace()
		if s.pos >= len(s.src) {
			return nil, false
		}
		switch s.src[s.pos] {
		case ',':
			s.pos++
			dst = append(dst, ',')
		case ']':
			s.pos++
			return append(dst, ']'), true
		default:
			return nil, false
		}
	}
}

// canonicalMember is one object entry, held as spans rather than values: the
// key inside the level's key arena, the canonical value inside its value
// buffer. Spans rather than a string because the previous shape allocated
// twice for every key, once turning the decoded bytes into a string and once
// turning that string back into bytes to emit it, and keys are the term that
// scales with the map-heavy records this actually runs on.
//
// Keys are still compared and ordered after decoding, so a key written as an
// escape and the same key written literally remain one key, for duplicate
// detection and for sorting alike.
type canonicalMember struct {
	keyStart, keyEnd int
	valStart, valEnd int
}

func (s *canonicalStream) object(dst []byte, depth int) ([]byte, bool) {
	s.pos++ // consume {
	s.skipSpace()
	if s.pos < len(s.src) && s.src[s.pos] == '}' {
		s.pos++
		return append(dst, '{', '}'), true
	}
	for depth >= len(s.levels) {
		s.levels = append(s.levels, canonicalLevel{})
	}
	// Taken into locals and written back at the end. They may be reallocated
	// by append, and a nested call may reallocate s.levels itself, so nothing
	// here may hold a pointer into either across the recursion.
	members := s.levels[depth].members[:0]
	keys := s.levels[depth].keys[:0]
	// Values are canonicalised into one buffer for this object as they are
	// parsed, and copied out in key order afterwards. The alternative, decoding
	// the whole subtree into Go values so it can be re-encoded sorted, is what
	// this file exists to remove.
	values := s.levels[depth].values[:0]
	defer func() {
		s.levels[depth].members = members
		s.levels[depth].keys = keys
		s.levels[depth].values = values
	}()
	for {
		s.skipSpace()
		if s.pos >= len(s.src) || s.src[s.pos] != '"' {
			return nil, false
		}
		keyStart := len(keys)
		var ok bool
		keys, ok = s.strInto(keys)
		if !ok {
			return nil, false
		}
		keyEnd := len(keys)
		s.skipSpace()
		if s.pos >= len(s.src) || s.src[s.pos] != ':' {
			return nil, false
		}
		s.pos++
		s.skipSpace()
		valStart := len(values)
		values, ok = s.value(values, depth+1)
		if !ok {
			return nil, false
		}
		members = append(members, canonicalMember{
			keyStart: keyStart, keyEnd: keyEnd, valStart: valStart, valEnd: len(values)})
		s.skipSpace()
		if s.pos >= len(s.src) {
			return nil, false
		}
		switch s.src[s.pos] {
		case ',':
			s.pos++
			continue
		case '}':
			s.pos++
		default:
			return nil, false
		}
		break
	}
	key := func(m canonicalMember) []byte { return keys[m.keyStart:m.keyEnd] }
	sort.Slice(members, func(i, j int) bool {
		return bytes.Compare(key(members[i]), key(members[j])) < 0
	})
	dst = append(dst, '{')
	for index, member := range members {
		if index > 0 {
			if bytes.Equal(key(member), key(members[index-1])) {
				return nil, false // duplicate field; the established path names it
			}
			dst = append(dst, ',')
		}
		dst = appendCanonicalStringV2(dst, key(member))
		dst = append(dst, ':')
		dst = append(dst, values[member.valStart:member.valEnd]...)
	}
	return append(dst, '}'), true
}

// str decodes the string starting at the current position into s.scratch and
// returns it. The result is only valid until the next call, which is why an
// object key is copied before the next member is read.
func (s *canonicalStream) str() ([]byte, bool) {
	out, ok := s.strInto(s.scratch[:0])
	s.scratch = out
	return out, ok
}

// strInto decodes the string at the current position by appending to dst. Keys
// pass their level's arena so that every key of one object stays readable at
// once, which sorting requires; values pass the shared scratch, which only has
// to survive until it is emitted.
func (s *canonicalStream) strInto(dst []byte) ([]byte, bool) {
	s.pos++ // consume opening quote
	for {
		if s.pos >= len(s.src) {
			return nil, false
		}
		c := s.src[s.pos]
		switch {
		case c == '"':
			s.pos++
			return dst, true
		case c == '\\':
			s.pos++
			if s.pos >= len(s.src) {
				return nil, false
			}
			switch s.src[s.pos] {
			case '"', '\\', '/':
				dst = append(dst, s.src[s.pos])
				s.pos++
			case 'b':
				dst = append(dst, '\b')
				s.pos++
			case 'f':
				dst = append(dst, '\f')
				s.pos++
			case 'n':
				dst = append(dst, '\n')
				s.pos++
			case 'r':
				dst = append(dst, '\r')
				s.pos++
			case 't':
				dst = append(dst, '\t')
				s.pos++
			case 'u':
				unit, ok := decodeJSONHexQuad(s.src, s.pos+1)
				if !ok {
					return nil, false
				}
				s.pos += 5
				if utf16.IsSurrogate(rune(unit)) {
					// A high surrogate must be followed by its low half. The
					// established path validates this before decoding, so a
					// lone half never reaches here in the accepting direction;
					// bailing keeps that true if it ever does.
					if s.pos+6 > len(s.src) || s.src[s.pos] != '\\' || s.src[s.pos+1] != 'u' {
						return nil, false
					}
					low, ok := decodeJSONHexQuad(s.src, s.pos+2)
					if !ok {
						return nil, false
					}
					combined := utf16.DecodeRune(rune(unit), rune(low))
					if combined == utf8.RuneError {
						return nil, false
					}
					s.pos += 6
					dst = utf8.AppendRune(dst, combined)
					continue
				}
				dst = utf8.AppendRune(dst, rune(unit))
			default:
				return nil, false
			}
		case c < 0x20:
			return nil, false // a raw control character is not valid inside a JSON string
		default:
			dst = append(dst, c)
			s.pos++
		}
	}
}

// appendCanonicalStringV2 writes one JSON string, escaped exactly as
// encoding/json's appendString does with escapeHTML false.
//
// The set of literal ASCII bytes is that function's safeSet: everything from
// 0x20 up except the quote and the backslash. That leaves <, >, & and / and the
// delete character literal, which is why an input written as < comes back
// as a bare < and one written as \/ comes back as a bare /. Both read like
// mistakes against the contract's wording and both are the established
// behaviour; they are pinned as branches "html chars escaped in input" and
// "solidus escaped in input".
//
// U+2028 and U+2029 are written literally. The established path escapes them
// with everything else and then walks the finished output undoing exactly those
// two, counting preceding backslashes so that a string whose text happens to
// read as the escape stays escaped. Emitting from decoded runes has no such
// ambiguity to resolve: a backslash in the text is already its own escape here.
func appendCanonicalStringV2(dst, decoded []byte) []byte {
	dst = append(dst, '"')
	start := 0
	for index := 0; index < len(decoded); {
		if b := decoded[index]; b < utf8.RuneSelf {
			if b >= 0x20 && b != '"' && b != '\\' {
				index++
				continue
			}
			dst = append(dst, decoded[start:index]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				const hexDigits = "0123456789abcdef"
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[b>>4], hexDigits[b&0xf])
			}
			index++
			start = index
			continue
		}
		size := utf8.RuneLen(rune(0))
		_, size = utf8.DecodeRune(decoded[index:])
		if size == 1 {
			// Invalid UTF-8. The encoder writes the replacement character
			// escaped, and the decode that follows it in the established path
			// turns that back into the character itself.
			dst = append(dst, decoded[start:index]...)
			dst = utf8.AppendRune(dst, utf8.RuneError)
			index++
			start = index
			continue
		}
		index += size
	}
	dst = append(dst, decoded[start:]...)
	return append(dst, '"')
}
