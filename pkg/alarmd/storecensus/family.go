// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storecensus

import "strings"

const (
	// maxSegments is how many of a key's segments name its family; the rest
	// are one "*". Every key this process writes is named within them, and
	// so is the platform's longest cache key: a prefix of up to three
	// segments, three of kind and its instances.
	maxSegments = 8
	// maxFamilyName bounds a family's name, which is a metric label.
	maxFamilyName = 96
)

// FamilyOf is the family a key belongs to, by the built-in words alone;
// Vocabulary.FamilyOf adds a deployment's configured prefixes.
func FamilyOf(key string) string { return defaultVocabulary.FamilyOf(key) }

// FamilyOf is the family a key belongs to. A key is segments between its
// separators - ":" as alarmd and most Redis users write them, "." as the
// platform's Python cache writes its keys - and within a segment, parts
// between "_" and "-". A part is kept only when it is a word of the code
// that writes the store (Vocabulary) and does not name one thing; every
// other part - a number, a digest, a hash tag, a UUID, and any word that is
// data, a receiver's or a strategy's name, a mail address, text in any
// language - is written "*". A mail address is split by its own dots, so
// from a segment with an "@" on, every segment joined to it by "." is its
// domain and is written "*" too, whatever words of the code it happens to
// spell. The separators are kept, so a family reads as its keys do: keys
// that differ only in which Query Group, series, strategy or receiver they
// are about are one family.
func (vocabulary *Vocabulary) FamilyOf(key string) string {
	var name strings.Builder
	segments, start := 0, 0
	address := false
	for index := 0; index <= len(key); index++ {
		if index < len(key) && key[index] == '{' {
			// A hash tag is one segment whatever it holds.
			if closing := strings.IndexByte(key[index:], '}'); closing > 0 {
				index += closing
			}
			continue
		}
		if index < len(key) && key[index] != ':' && key[index] != '.' {
			continue
		}
		if segments == maxSegments {
			name.WriteString("*")
			break
		}
		segment := key[start:index]
		if start == 0 || key[start-1] != '.' {
			address = false
		}
		if strings.Contains(segment, "@") {
			address = true
		}
		if address {
			name.WriteString("*")
		} else {
			name.WriteString(vocabulary.kind(segment))
		}
		segments++
		if index < len(key) {
			name.WriteByte(key[index])
		}
		start = index + 1
	}
	family := name.String()
	if len(family) > maxFamilyName {
		family = family[:maxFamilyName]
	}
	return family
}

// kind is a segment with everything but the code's words written "*": a
// segment of a configured prefix as it is, a hash tag whole, the prefix of
// a UUID it ends in (celery-task-meta-<uuid> is celery-task-meta-*), and
// otherwise part by part.
func (vocabulary *Vocabulary) kind(segment string) string {
	if _, configured := vocabulary.segments[segment]; configured {
		return segment
	}
	if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
		return "*"
	}
	if prefix, found := beforeTrailingUUID(segment); found {
		if named := vocabulary.parts(prefix); named != "*" {
			return named + "*"
		}
		return "*"
	}
	return vocabulary.parts(segment)
}

// parts writes each part of a segment between "_" and "-" as itself when it
// is a word of the code, and "*" when it names one thing (a number, a
// digest); at the first part that is neither - a word of data - the rest of
// the segment is that data's, a user's table name or a pod's, and is one
// "*". A segment with no word of the code is "*" whole. Empty parts - a
// leading "_", a trailing "-" - stay empty.
func (vocabulary *Vocabulary) parts(segment string) string {
	var name strings.Builder
	known, start := false, 0
	for index := 0; index <= len(segment); index++ {
		if index < len(segment) && segment[index] != '_' && segment[index] != '-' {
			continue
		}
		part := segment[start:index]
		switch {
		case part == "":
		case instance(part):
			name.WriteString("*")
		case vocabulary.knows(part):
			name.WriteString(part)
			known = true
		default:
			name.WriteString("*")
			index = len(segment)
		}
		if index < len(segment) {
			name.WriteByte(segment[index])
		}
		start = index + 1
	}
	if !known {
		return "*"
	}
	return name.String()
}

// instance reports a segment that names one thing rather than a kind.
func instance(segment string) bool {
	switch {
	case segment == "":
		return false
	case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
		return true
	case len(segment) > 40:
		return true
	}
	digits, hexadecimal, hyphens := 0, 0, 0
	for index := 0; index < len(segment); index++ {
		char := segment[index]
		switch {
		case char >= '0' && char <= '9':
			digits++
			hexadecimal++
		case char >= 'a' && char <= 'f', char >= 'A' && char <= 'F':
			hexadecimal++
		case char == '-':
			hyphens++
		default:
			return false
		}
	}
	signed := segment[0] == '-' && hyphens == 1
	switch {
	case digits > 0 && digits+hyphens == len(segment) && (hyphens == 0 || signed):
		// A number of any length, signed or not.
		return true
	case segment[0] == '-' || segment[len(segment)-1] == '-':
		return false
	default:
		// A hexadecimal token long enough to be a digest or an identifier
		// rather than a word made of a to f, whole or in hyphenated groups
		// as a UUID is written.
		return hexadecimal >= 16
	}
}

// uuidGroups is how many hexadecimal digits each hyphen-separated group of a
// UUID has.
var uuidGroups = [...]int{8, 4, 4, 4, 12}

// beforeTrailingUUID is what precedes a UUID a segment ends in, when it ends
// in one after some prefix: a name made of a word and one instance of it.
func beforeTrailingUUID(segment string) (string, bool) {
	const length = 36
	if len(segment) <= length {
		return "", false
	}
	tail := segment[len(segment)-length:]
	position := 0
	for group, digits := range uuidGroups {
		if group > 0 {
			if tail[position] != '-' {
				return "", false
			}
			position++
		}
		for end := position + digits; position < end; position++ {
			char := tail[position]
			if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
				return "", false
			}
		}
	}
	return segment[:len(segment)-length], true
}
