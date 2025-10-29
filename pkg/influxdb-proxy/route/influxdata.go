// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"bytes"
	"fmt"
	"sort"
)

// Tag represents a single key/value tag pair.
type Tag struct {
	Key   []byte
	Value []byte
}

// NewTag returns a new Tag.
func NewTag(key, value []byte) Tag {
	return Tag{
		Key:   key,
		Value: value,
	}
}

// Size returns the size of the key and value.
func (t Tag) Size() int { return len(t.Key) + len(t.Value) }

// Clone returns a shallow copy of Tag.
//
// Tags associated with a Point created by ParsePointsWithPrecision will hold references to the byte slice that was parsed.
// Use Clone to create a Tag with new byte slices that do not refer to the argument to ParsePointsWithPrecision.
func (t Tag) Clone() Tag {
	other := Tag{
		Key:   make([]byte, len(t.Key)),
		Value: make([]byte, len(t.Value)),
	}

	copy(other.Key, t.Key)
	copy(other.Value, t.Value)

	return other
}

// String returns the string reprsentation of the tag.
func (t *Tag) String() string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(string(t.Key))
	buf.WriteByte(' ')
	buf.WriteString(string(t.Value))
	buf.WriteByte('}')
	return buf.String()
}

// Tags represents a sorted list of tags.
type Tags []Tag

// NewTags returns a new Tags from a map.
func NewTags(m map[string]string) Tags {
	if len(m) == 0 {
		return nil
	}
	a := make(Tags, 0, len(m))
	for k, v := range m {
		a = append(a, NewTag([]byte(k), []byte(v)))
	}
	sort.Sort(a)
	return a
}

// Keys returns the list of keys for a tag set.
func (a Tags) Keys() []string {
	if len(a) == 0 {
		return nil
	}
	keys := make([]string, len(a))
	for i, tag := range a {
		keys[i] = string(tag.Key)
	}
	return keys
}

// Values returns the list of values for a tag set.
func (a Tags) Values() []string {
	if len(a) == 0 {
		return nil
	}
	values := make([]string, len(a))
	for i, tag := range a {
		values[i] = string(tag.Value)
	}
	return values
}

// String returns the string representation of the tags.
func (a Tags) String() string {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i := range a {
		buf.WriteString(a[i].String())
		if i < len(a)-1 {
			buf.WriteByte(' ')
		}
	}
	buf.WriteByte(']')
	return buf.String()
}

// Size returns the number of bytes needed to store all tags. Note, this is
// the number of bytes needed to store all keys and values and does not account
// for data structures or delimiters for example.
func (a Tags) Size() int {
	var total int
	for i := range a {
		total += a[i].Size()
	}
	return total
}

// Clone returns a copy of the slice where the elements are a result of calling `Clone` on the original elements
//
// Tags associated with a Point created by ParsePointsWithPrecision will hold references to the byte slice that was parsed.
// Use Clone to create Tags with new byte slices that do not refer to the argument to ParsePointsWithPrecision.
func (a Tags) Clone() Tags {
	if len(a) == 0 {
		return nil
	}

	others := make(Tags, len(a))
	for i := range a {
		others[i] = a[i].Clone()
	}

	return others
}

func (a Tags) Len() int { return len(a) }

func (a Tags) Less(i, j int) bool { return bytes.Compare(a[i].Key, a[j].Key) == -1 }

func (a Tags) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

// Equal returns true if a equals other.
func (a Tags) Equal(other Tags) bool {
	if len(a) != len(other) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Key, other[i].Key) || !bytes.Equal(a[i].Value, other[i].Value) {
			return false
		}
	}
	return true
}

// CompareTags returns -1 if a < b, 1 if a > b, and 0 if a == b.
func CompareTags(a, b Tags) int {
	// Compare each key & value until a mismatch.
	for i := 0; i < len(a) && i < len(b); i++ {
		if cmp := bytes.Compare(a[i].Key, b[i].Key); cmp != 0 {
			return cmp
		}
		if cmp := bytes.Compare(a[i].Value, b[i].Value); cmp != 0 {
			return cmp
		}
	}

	// If all tags are equal up to this point then return shorter tagset.
	if len(a) < len(b) {
		return -1
	} else if len(a) > len(b) {
		return 1
	}

	// All tags are equal.
	return 0
}

// Get returns the value for a key.
func (a Tags) Get(key []byte) []byte {
	// OPTIMIZE: Use sort.Search if tagset is large.

	for _, t := range a {
		if bytes.Equal(t.Key, key) {
			return t.Value
		}
	}
	return nil
}

// GetString returns the string value for a string key.
func (a Tags) GetString(key string) string {
	return string(a.Get([]byte(key)))
}

// Set sets the value for a key.
func (a *Tags) Set(key, value []byte) {
	for i, t := range *a {
		if bytes.Equal(t.Key, key) {
			(*a)[i].Value = value
			return
		}
	}
	*a = append(*a, Tag{Key: key, Value: value})
	sort.Sort(*a)
}

// SetString sets the string value for a string key.
func (a *Tags) SetString(key, value string) {
	a.Set([]byte(key), []byte(value))
}

// Delete removes a tag by key.
func (a *Tags) Delete(key []byte) {
	for i, t := range *a {
		if bytes.Equal(t.Key, key) {
			copy((*a)[i:], (*a)[i+1:])
			(*a)[len(*a)-1] = Tag{}
			*a = (*a)[:len(*a)-1]
			return
		}
	}
}

// Map returns a map representation of the tags.
func (a Tags) Map() map[string]string {
	m := make(map[string]string, len(a))
	for _, t := range a {
		m[string(t.Key)] = string(t.Value)
	}
	return m
}

// Merge merges the tags combining the two. If both define a tag with the
// same key, the merged value overwrites the old value.
// A new map is returned.
func (a Tags) Merge(other map[string]string) Tags {
	merged := make(map[string]string, len(a)+len(other))
	for _, t := range a {
		merged[string(t.Key)] = string(t.Value)
	}
	for k, v := range other {
		merged[k] = v
	}
	return NewTags(merged)
}

// HashKey hashes all of a tag's keys.
func (a Tags) HashKey() []byte {
	return a.AppendHashKey(nil)
}

func (a Tags) needsEscape() bool {
	for i := range a {
		t := &a[i]
		for j := range tagEscapeCodes {
			c := &tagEscapeCodes[j]
			if bytes.IndexByte(t.Key, c.k[0]) != -1 || bytes.IndexByte(t.Value, c.k[0]) != -1 {
				return true
			}
		}
	}
	return false
}

// AppendHashKey appends the result of hashing all of a tag's keys and values to dst and returns the extended buffer.
func (a Tags) AppendHashKey(dst []byte) []byte {
	// Empty maps marshal to empty bytes.
	if len(a) == 0 {
		return dst
	}

	// Type invariant: Tags are sorted

	sz := 0
	var escaped Tags
	if a.needsEscape() {
		var tmp [20]Tag
		if len(a) < len(tmp) {
			escaped = tmp[:len(a)]
		} else {
			escaped = make(Tags, len(a))
		}

		for i := range a {
			t := &a[i]
			nt := &escaped[i]
			nt.Key = escapeTag(t.Key)
			nt.Value = escapeTag(t.Value)
			sz += len(nt.Key) + len(nt.Value)
		}
	} else {
		sz = a.Size()
		escaped = a
	}

	sz += len(escaped) + (len(escaped) * 2) // separators

	// Generate marshaled bytes.
	if cap(dst)-len(dst) < sz {
		nd := make([]byte, len(dst), len(dst)+sz)
		copy(nd, dst)
		dst = nd
	}
	buf := dst[len(dst) : len(dst)+sz]
	idx := 0
	for i := range escaped {
		k := &escaped[i]
		if len(k.Value) == 0 {
			continue
		}
		buf[idx] = ','
		idx++
		copy(buf[idx:], k.Key)
		idx += len(k.Key)
		buf[idx] = '='
		idx++
		copy(buf[idx:], k.Value)
		idx += len(k.Value)
	}
	return dst[:len(dst)+idx]
}

// skipWhitespace returns the end position within buf, starting at i after
// scanning over spaces in tags.
func skipWhitespace(buf []byte, i int) int {
	for i < len(buf) {
		if buf[i] != ' ' && buf[i] != '\t' && buf[i] != 0 {
			break
		}
		i++
	}
	return i
}

func scanLineSimple(buf []byte, i int) (int, []byte) {
	idx := bytes.Index(buf[i:], []byte("\n"))
	if idx == -1 {
		return idx, buf[i:]
	}
	return i + idx, buf[i : i+idx+1]
}

// scanLine returns the end position in buf and the next line found within
// buf.
func scanLine(buf []byte, i int) (int, []byte) {
	start := i
	quoted := false
	fields := false

	// tracks how many '=' and commas we've seen
	// this duplicates some of the functionality in scanFields
	equals := 0
	commas := 0
	for {
		// reached the end of buf?
		if i >= len(buf) {
			break
		}

		// skip past escaped characters
		if buf[i] == '\\' && i+2 < len(buf) {
			i += 2
			continue
		}

		if buf[i] == ' ' {
			fields = true
		}

		// If we see a double quote, makes sure it is not escaped
		if fields {
			if !quoted && buf[i] == '=' {
				i++
				equals++
				continue
			} else if !quoted && buf[i] == ',' {
				i++
				commas++
				continue
			} else if buf[i] == '"' && equals > commas {
				i++
				quoted = !quoted
				continue
			}
		}

		if buf[i] == '\n' && !quoted {
			break
		}

		i++
	}

	return i, buf[start:i]
}

func scanKeySimple(buf []byte, i int) (int, []byte, error) {
	idx := bytes.Index(buf, []byte(" "))
	if idx == -1 {
		return idx, nil, fmt.Errorf("wrong key:%s", buf)
	}
	return idx, buf[i:idx], nil
}

// scanKey scans buf starting at i for the measurement and tag portion of the point.
// It returns the ending position and the byte slice of key within buf.  If there
// are tags, they will be sorted if they are not already.
func scanKey(buf []byte, i int) (int, []byte, error) {
	start := skipWhitespace(buf, i)

	i = start

	// Determines whether the tags are sort, assume they are
	sorted := true

	// indices holds the indexes within buf of the start of each tag.  For example,
	// a buf of 'cpu,host=a,region=b,zone=c' would have indices slice of [4,11,20]
	// which indicates that the first tag starts at buf[4], seconds at buf[11], and
	// last at buf[20]
	indices := make([]int, 100)

	// tracks how many commas we've seen so we know how many values are indices.
	// Since indices is an arbitrarily large slice,
	// we need to know how many values in the buffer are in use.
	commas := 0

	// First scan the Point's measurement.
	state, i, err := scanMeasurement(buf, i)
	if err != nil {
		return i, buf[start:i], err
	}

	// Optionally scan tags if needed.
	if state == tagKeyState {
		i, commas, indices, err = scanTags(buf, i, indices)
		if err != nil {
			return i, buf[start:i], err
		}
	}

	// Now we know where the key region is within buf, and the location of tags, we
	// need to determine if duplicate tags exist and if the tags are sorted. This iterates
	// over the list comparing each tag in the sequence with each other.
	for j := 0; j < commas-1; j++ {
		// get the left and right tags
		_, left := scanTo(buf[indices[j]:indices[j+1]-1], 0, '=')
		_, right := scanTo(buf[indices[j+1]:indices[j+2]-1], 0, '=')

		// If left is greater than right, the tags are not sorted. We do not have to
		// continue because the short path no longer works.
		// If the tags are equal, then there are duplicate tags, and we should abort.
		// If the tags are not sorted, this pass may not find duplicate tags and we
		// need to do a more exhaustive search later.
		if cmp := bytes.Compare(left, right); cmp > 0 {
			sorted = false
			break
		} else if cmp == 0 {
			return i, buf[start:i], fmt.Errorf("duplicate tags")
		}
	}

	// If the tags are not sorted, then sort them.  This sort is inline and
	// uses the tag indices we created earlier.  The actual buffer is not sorted, the
	// indices are using the buffer for value comparison.  After the indices are sorted,
	// the buffer is reconstructed from the sorted indices.
	if !sorted && commas > 0 {
		// Get the measurement name for later
		measurement := buf[start : indices[0]-1]

		// Sort the indices
		indices := indices[:commas]
		insertionSort(0, commas, buf, indices)

		// Create a new key using the measurement and sorted indices
		b := make([]byte, len(buf[start:i]))
		pos := copy(b, measurement)
		for _, i := range indices {
			b[pos] = ','
			pos++
			_, v := scanToSpaceOr(buf, i, ',')
			pos += copy(b[pos:], v)
		}

		// Check again for duplicate tags now that the tags are sorted.
		for j := 0; j < commas-1; j++ {
			// get the left and right tags
			_, left := scanTo(buf[indices[j]:], 0, '=')
			_, right := scanTo(buf[indices[j+1]:], 0, '=')

			// If the tags are equal, then there are duplicate tags, and we should abort.
			// If the tags are not sorted, this pass may not find duplicate tags and we
			// need to do a more exhaustive search later.
			if bytes.Equal(left, right) {
				return i, b, fmt.Errorf("duplicate tags")
			}
		}

		return i, b, nil
	}

	return i, buf[start:i], nil
}

// The following constants allow us to specify which state to move to
// next, when scanning sections of a Point.
const (
	tagKeyState = iota
	tagValueState
	fieldsState
)

// scanMeasurement examines the measurement part of a Point, returning
// the next state to move to, and the current location in the buffer.
func scanMeasurement(buf []byte, i int) (int, int, error) {
	// Check first byte of measurement, anything except a comma is fine.
	// It can't be a space, since whitespace is stripped prior to this
	// function call.
	if i >= len(buf) || buf[i] == ',' {
		return -1, i, fmt.Errorf("missing measurement")
	}

	for {
		i++
		if i >= len(buf) {
			// cpu
			return -1, i, fmt.Errorf("missing fields")
		}

		if buf[i-1] == '\\' {
			// Skip character (it's escaped).
			continue
		}

		// Unescaped comma; move onto scanning the tags.
		if buf[i] == ',' {
			return tagKeyState, i + 1, nil
		}

		// Unescaped space; move onto scanning the fields.
		if buf[i] == ' ' {
			// cpu value=1.0
			return fieldsState, i, nil
		}
	}
}

// scanTags examines all the tags in a Point, keeping track of and
// returning the updated indices slice, number of commas and location
// in buf where to start examining the Point fields.
func scanTags(buf []byte, i int, indices []int) (int, int, []int, error) {
	var (
		err    error
		commas int
		state  = tagKeyState
	)

	for {
		switch state {
		case tagKeyState:
			// Grow our indices slice if we have too many tags.
			if commas >= len(indices) {
				newIndics := make([]int, cap(indices)*2)
				copy(newIndics, indices)
				indices = newIndics
			}
			indices[commas] = i
			commas++

			i, err = scanTagsKey(buf, i)
			state = tagValueState // tag value always follows a tag key
		case tagValueState:
			state, i, err = scanTagsValue(buf, i)
		case fieldsState:
			indices[commas] = i + 1
			return i, commas, indices, nil
		}

		if err != nil {
			return i, commas, indices, err
		}
	}
}

// scanTagsKey scans each character in a tag key.
func scanTagsKey(buf []byte, i int) (int, error) {
	// First character of the key.
	if i >= len(buf) || buf[i] == ' ' || buf[i] == ',' || buf[i] == '=' {
		// cpu,{'', ' ', ',', '='}
		return i, fmt.Errorf("missing tag key")
	}

	// Examine each character in the tag key until we hit an unescaped
	// equals (the tag value), or we hit an error (i.e., unescaped
	// space or comma).
	for {
		i++

		// Either we reached the end of the buffer or we hit an
		// unescaped comma or space.
		if i >= len(buf) ||
			((buf[i] == ' ' || buf[i] == ',') && buf[i-1] != '\\') {
			// cpu,tag{'', ' ', ','}
			return i, fmt.Errorf("missing tag value")
		}

		if buf[i] == '=' && buf[i-1] != '\\' {
			// cpu,tag=
			return i + 1, nil
		}
	}
}

// scanTagsValue scans each character in a tag value.
func scanTagsValue(buf []byte, i int) (int, int, error) {
	// ImageTag value cannot be empty.
	if i >= len(buf) || buf[i] == ',' || buf[i] == ' ' {
		// cpu,tag={',', ' '}
		return -1, i, fmt.Errorf("missing tag value")
	}

	// Examine each character in the tag value until we hit an unescaped
	// comma (move onto next tag key), an unescaped space (move onto
	// fields), or we error out.
	for {
		i++
		if i >= len(buf) {
			// cpu,tag=value
			return -1, i, fmt.Errorf("missing fields")
		}

		// An unescaped equals sign is an invalid tag value.
		if buf[i] == '=' && buf[i-1] != '\\' {
			// cpu,tag={'=', 'fo=o'}
			return -1, i, fmt.Errorf("invalid tag format")
		}

		if buf[i] == ',' && buf[i-1] != '\\' {
			// cpu,tag=foo,
			return tagKeyState, i + 1, nil
		}

		// cpu,tag=foo value=1.0
		// cpu, tag=foo\= value=1.0
		if buf[i] == ' ' && buf[i-1] != '\\' {
			return fieldsState, i, nil
		}
	}
}

func insertionSort(l, r int, buf []byte, indices []int) {
	for i := l + 1; i < r; i++ {
		for j := i; j > l && less(buf, indices, j, j-1); j-- {
			indices[j], indices[j-1] = indices[j-1], indices[j]
		}
	}
}

// scanTo returns the end position in buf and the next consecutive block
// of bytes, starting from i and ending with stop byte, where stop byte
// has not been escaped.
//
// If there are leading spaces, they are skipped.
func scanTo(buf []byte, i int, stop byte) (int, []byte) {
	start := i
	for {
		// reached the end of buf?
		if i >= len(buf) {
			break
		}

		// Reached unescaped stop value?
		if buf[i] == stop && (i == 0 || buf[i-1] != '\\') {
			break
		}
		i++
	}

	return i, buf[start:i]
}

// scanTo returns the end position in buf and the next consecutive block
// of bytes, starting from i and ending with stop byte.  If there are leading
// spaces, they are skipped.
func scanToSpaceOr(buf []byte, i int, stop byte) (int, []byte) {
	start := i
	if buf[i] == stop || buf[i] == ' ' {
		return i, buf[start:i]
	}

	for {
		i++
		if buf[i-1] == '\\' {
			continue
		}

		// reached the end of buf?
		if i >= len(buf) {
			return i, buf[start:i]
		}

		// reached end of block?
		if buf[i] == stop || buf[i] == ' ' {
			return i, buf[start:i]
		}
	}
}

func less(buf []byte, indices []int, i, j int) bool {
	// This grabs the tag names for i & j, it ignores the values
	_, a := scanTo(buf, indices[i], '=')
	_, b := scanTo(buf, indices[j], '=')
	return bytes.Compare(a, b) < 0
}

// parseTags parses buf into the provided destination tags, returning destination
// Tags, which may have a different length and capacity.
func parseTags(buf []byte, dst Tags) Tags {
	if len(buf) == 0 {
		return nil
	}

	n := bytes.Count(buf, []byte(","))
	if cap(dst) < n {
		dst = make(Tags, n)
	} else {
		dst = dst[:n]
	}

	// Ensure existing behaviour when point has no tags and nil slice passed in.
	if dst == nil {
		dst = Tags{}
	}

	// Series keys can contain escaped commas, therefore the number of commas
	// in a series key only gives an estimation of the upper bound on the number
	// of tags.
	var i int
	walkTags(buf, func(key, value []byte) bool {
		dst[i].Key, dst[i].Value = key, value
		i++
		return true
	})
	return dst[:i]
}

func walkTags(buf []byte, fn func(key, value []byte) bool) {
	if len(buf) == 0 {
		return
	}

	pos, name := scanTo(buf, 0, ',')

	// it's an empty key, so there are no tags
	if len(name) == 0 {
		return
	}

	hasEscape := bytes.IndexByte(buf, '\\') != -1
	i := pos + 1
	var key, value []byte
	for {
		if i >= len(buf) {
			break
		}
		i, key = scanTo(buf, i, '=')
		i, value = scanTagValue(buf, i+1)

		if len(value) == 0 {
			continue
		}

		if hasEscape {
			if !fn(unescapeTag(key), unescapeTag(value)) {
				return
			}
		} else {
			if !fn(key, value) {
				return
			}
		}

		i++
	}
}

func scanTagValue(buf []byte, i int) (int, []byte) {
	start := i
	for {
		if i >= len(buf) {
			break
		}

		if buf[i] == ',' && buf[i-1] != '\\' {
			break
		}
		i++
	}
	if i > len(buf) {
		return i, nil
	}
	return i, buf[start:i]
}

type escapeSet struct {
	k   [1]byte
	esc [2]byte
}

var tagEscapeCodes = [...]escapeSet{
	{k: [1]byte{','}, esc: [2]byte{'\\', ','}},
	{k: [1]byte{' '}, esc: [2]byte{'\\', ' '}},
	{k: [1]byte{'='}, esc: [2]byte{'\\', '='}},
}

func unescapeTag(in []byte) []byte {
	if bytes.IndexByte(in, '\\') == -1 {
		return in
	}

	for i := range tagEscapeCodes {
		c := &tagEscapeCodes[i]
		if bytes.IndexByte(in, c.k[0]) != -1 {
			in = bytes.Replace(in, c.esc[:], c.k[:], -1)
		}
	}
	return in
}

func escapeTag(in []byte) []byte {
	for i := range tagEscapeCodes {
		c := &tagEscapeCodes[i]
		if bytes.IndexByte(in, c.k[0]) != -1 {
			in = bytes.Replace(in, c.k[:], c.esc[:], -1)
		}
	}
	return in
}
