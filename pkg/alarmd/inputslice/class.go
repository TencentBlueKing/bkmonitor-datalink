// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package inputslice

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// classesPerSplit is m: a split by the next character from the end makes
	// at most this many character classes, m−1 explicit ones and the
	// complement of their union.
	classesPerSplit = 4
	// classWidth is w, the most bytes one explicit class may take written
	// out, brackets included, so that a level of the path never costs more
	// than levelBytes whichever class a slice took there.
	classWidth = 16
	// levelBytes is (m−1)·w, what one level of a path is charged in the
	// condition budget: the complement, the widest class of a level, is
	// written as the union of the explicit ones and never exceeds it.
	levelBytes = (classesPerSplit - 1) * classWidth
	// dimensionBytes is what a dimension's condition costs beyond its
	// classes: `^(?s:.*)` and `$` are 9 bytes, `^…$` 2, `^(?s:.+)$` 9.
	dimensionBytes = 10
)

// fixedAlphabet is what a split without a sample divides evenly. Lower-case
// letters and digits are what most dimension values end in; everything else
// falls to the complement, which is as correct and only less even.
const fixedAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// class is one character class of a path. An explicit class is the runes in
// set; a negated one is every rune not in set, which is how the last class of
// a split covers all characters no sample showed, so that the classes of a
// split are a partition of every rune whatever the sample was.
type class struct {
	set     []rune
	negated bool
}

// contains is whether r is in the class.
func (c class) contains(r rune) bool {
	_, found := slices.BinarySearch(c.set, r)
	return found != c.negated
}

// render writes the class as a Go RE2 bracket expression.
func (c class) render() string {
	if c.negated {
		return "[^" + renderSet(c.set) + "]"
	}
	return "[" + renderSet(c.set) + "]"
}

// renderSet writes a sorted set of runes as the inside of a bracket
// expression, three or more consecutive runes as a range. Characters that
// mean something inside brackets, and characters that are not printable, are
// written as `\x{…}` so that no value of the dimension can change how the
// class parses; `[` is escaped too, because `[:` opens a POSIX class in RE2.
func renderSet(set []rune) string {
	var builder strings.Builder
	for index := 0; index < len(set); {
		end := index
		for end+1 < len(set) && set[end+1] == set[end]+1 {
			end++
		}
		if end-index >= 2 {
			builder.WriteString(escapeRune(set[index]))
			builder.WriteByte('-')
			builder.WriteString(escapeRune(set[end]))
		} else {
			for _, r := range set[index : end+1] {
				builder.WriteString(escapeRune(r))
			}
		}
		index = end + 1
	}
	return builder.String()
}

func escapeRune(r rune) string {
	switch {
	case r == ']' || r == '\\' || r == '^' || r == '-' || r == '[':
		return fmt.Sprintf(`\x{%x}`, r)
	case !unicode.IsPrint(r):
		return fmt.Sprintf(`\x{%x}`, r)
	default:
		return string(r)
	}
}

// splitClasses picks the classes of one split from the next characters the
// sample showed, counted per rune: the sorted runes are cut into m groups of
// roughly equal count, the first m−1 written out and the last left to the
// complement, which also takes every rune the sample did not show. A group
// that would be wider than classWidth is closed early, and what does not fit
// in the explicit classes goes to the complement. Fewer distinct runes than
// m give fewer classes: an empty explicit class would be a child that holds
// nothing. With no runes at all the fixed alphabet is divided instead.
//
// The choice only decides how many splits the tree needs. Whatever it is,
// the classes returned are disjoint and cover every rune.
func splitClasses(histogram map[rune]int) []class {
	if len(histogram) == 0 {
		return fixedClasses()
	}
	runes := make([]rune, 0, len(histogram))
	total := 0
	for r, count := range histogram {
		runes = append(runes, r)
		total += count
	}
	slices.Sort(runes)
	explicit := make([][]rune, 0, classesPerSplit-1)
	var current []rune
	seen := 0
	closeCurrent := func() {
		if len(current) > 0 {
			explicit = append(explicit, current)
			current = nil
		}
	}
	for _, r := range runes {
		if len(explicit) == classesPerSplit-1 {
			break
		}
		// The group for this rune is the one its cumulative count falls in,
		// so that a heavy rune sits alone and light ones share.
		if len(current) > 0 && seen*classesPerSplit >= (len(explicit)+1)*total {
			closeCurrent()
			if len(explicit) == classesPerSplit-1 {
				break
			}
		}
		candidate := append(slices.Clone(current), r)
		if len(class{set: candidate}.render()) > classWidth {
			closeCurrent()
			if len(explicit) == classesPerSplit-1 {
				break
			}
			candidate = []rune{r}
		}
		current = candidate
		seen += histogram[r]
	}
	closeCurrent()
	// The last group of the even split is the complement's: when every
	// distinct rune landed in an explicit class, give the last one back, so
	// that the complement child is not empty of the sample by construction.
	// A single rune stays explicit, since `[^]` is no class.
	covered := 0
	for _, group := range explicit {
		covered += len(group)
	}
	if covered == len(runes) && len(runes) > 1 {
		last := explicit[len(explicit)-1]
		if len(last) == 1 {
			explicit = explicit[:len(explicit)-1]
		} else {
			explicit[len(explicit)-1] = last[:len(last)-1]
		}
	}
	return withComplement(explicit)
}

// cutClass is the parts of c that the classes of a split cut it into, empty
// ones left out. The classes partition every rune, so the parts partition c.
func cutClass(c class, classes []class) []class {
	parts := make([]class, 0, len(classes))
	for _, other := range classes {
		if part, ok := intersect(c, other); ok {
			parts = append(parts, part)
		}
	}
	return parts
}

// intersect is the runes in both classes, and false when there are none. A
// negated class is never empty: no set lists every rune.
func intersect(left, right class) (class, bool) {
	switch {
	case left.negated && right.negated:
		union := append(slices.Clone(left.set), right.set...)
		slices.Sort(union)
		return class{set: slices.Compact(union), negated: true}, true
	case left.negated:
		left, right = right, left
	}
	var set []rune
	for _, r := range left.set {
		if right.contains(r) {
			set = append(set, r)
		}
	}
	return class{set: set}, len(set) > 0
}

// fixedClasses divides fixedAlphabet into m equal groups, the last one left
// to the complement.
func fixedClasses() []class {
	alphabet := []rune(fixedAlphabet)
	size := len(alphabet) / classesPerSplit
	explicit := make([][]rune, 0, classesPerSplit-1)
	for group := 0; group < classesPerSplit-1; group++ {
		explicit = append(explicit, alphabet[group*size:(group+1)*size])
	}
	return withComplement(explicit)
}

func withComplement(explicit [][]rune) []class {
	classes := make([]class, 0, len(explicit)+1)
	var union []rune
	for _, group := range explicit {
		set := slices.Clone(group)
		slices.Sort(set)
		classes = append(classes, class{set: set})
		union = append(union, set...)
	}
	slices.Sort(union)
	union = slices.Compact(union)
	return append(classes, class{set: union, negated: true})
}

// nextRune reads value's character d+1 from the end the way Go's regexp
// reads it: invalid UTF-8 is one U+FFFD per byte. It reports false when the
// value has d characters or fewer.
func nextRune(value string, depth int) (rune, bool) {
	for step := 0; step < depth; step++ {
		if value == "" {
			return 0, false
		}
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	if value == "" {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(value)
	return r, true
}
