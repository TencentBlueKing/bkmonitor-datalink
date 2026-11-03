// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package nodata projects the series a Slot saw onto the dimension groups
// no-data detection judges, and nothing else. Whether a group is absent, what
// the expected set is, and what an absence produces are decided elsewhere:
// this package answers only "which group is this series, and is it one at all".
package nodata

import (
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Dimension is one (name, value) pair of a group, with the value already a
// string. Python reduces its dimensions with format_dicts_value_to_str before
// anything hashes or compares them, so a group that reached here as an integer
// and a group that reached here as its decimal text are the same group.
type Dimension struct {
	Name  string
	Value string
}

// Group is a no-data dimension group: the series' dimensions reduced to the
// item's agg_dimension, in name order.
//
// The __NO_DATA_DIMENSION__ tag is not stored. It is the same for every group
// this package produces, so holding it in each one would make it possible to
// build a Group without it - and a group missing the tag is a different object
// from the one Python produces while looking identical in a debugger. Key and
// Dimensions add it on the way out instead, which is the only way out.
type Group struct {
	dimensions []Dimension
}

// WholeItemGroup is the group every series reduces to when agg_dimension is
// empty, and the one Python reports when it has no history and no data at all.
// It carries nothing but the tag.
func WholeItemGroup() Group { return Group{} }

// Dimensions returns the group's own pairs in name order, without the tag. The
// tag belongs to every group and is added by the identity, not carried here.
func (group Group) Dimensions() []Dimension {
	return append([]Dimension(nil), group.dimensions...)
}

// Key is the group's identity within one item: stable, comparable, and usable
// as a map key. It is not an anomaly_id and not a dimensions md5 - those are
// the output layer's, and are built from Dimensions plus the tag through the
// Python-compatible hash rather than from this text.
//
// The encoding escapes the separators so that two different groups cannot
// collide by containing them: a dimension value holding "=" or "," would
// otherwise let {a: "b,c=d"} and {a: "b", c: "d"} agree.
func (group Group) Key() string {
	if len(group.dimensions) == 0 {
		return contract.NoDataDimensionTag + "=true"
	}
	parts := make([]string, 0, len(group.dimensions)+1)
	for _, dimension := range group.dimensions {
		parts = append(parts, escapeGroupText(dimension.Name)+"="+escapeGroupText(dimension.Value))
	}
	parts = append(parts, contract.NoDataDimensionTag+"=true")
	return strings.Join(parts, ",")
}

var groupTextEscaper = strings.NewReplacer(`\`, `\\`, `=`, `\=`, `,`, `\,`)

func escapeGroupText(text string) string { return groupTextEscaper.Replace(text) }

// Project reduces one series' dimensions to its no-data group.
//
// It reports false for a series Python calls invalid: one whose dimensions do
// not carry every name in aggDimension. Python's test is
// set(no_data_dimensions) - set(dimensions.keys()), taken against the series'
// dimensions before the reduction, and it drops the point with a warning rather
// than treating the missing name as an empty value. Treating it as empty would
// be worse than dropping: every series missing the name would collapse into one
// group that no expected set contains, and that group would then look absent
// forever.
//
// aggDimension being empty is not the same as a series having no dimensions.
// Empty means the item asked for one group, so every series projects onto
// WholeItemGroup and none is invalid - Python's set difference against an empty
// set is empty for every input.
func Project(dimensions map[string]string, aggDimension []string) (Group, bool) {
	if len(aggDimension) == 0 {
		return WholeItemGroup(), true
	}
	reduced := make([]Dimension, 0, len(aggDimension))
	for _, name := range aggDimension {
		value, present := dimensions[name]
		if !present {
			return Group{}, false
		}
		reduced = append(reduced, Dimension{Name: name, Value: value})
	}
	sort.Slice(reduced, func(left, right int) bool { return reduced[left].Name < reduced[right].Name })
	return Group{dimensions: reduced}, true
}

// ProjectionTally is what one Slot's projection produced, for the facts the
// absence evaluation reports. Dropped is counted rather than logged per series:
// a strategy whose agg_dimension names a dimension its data does not carry
// drops every series every round, and one number per Slot says that while a
// line per series would only bury it.
type ProjectionTally struct {
	Groups  map[string]Group
	Dropped uint64
}

// ProjectSeries projects every series of one Slot. Series that reduce to the
// same group are one group: Python keeps the latest timestamp per group and
// discards the rest, and which series produced it does not survive into the
// judgement either way.
func ProjectSeries(series []map[string]string, aggDimension []string) ProjectionTally {
	tally := ProjectionTally{Groups: make(map[string]Group, len(series))}
	for _, dimensions := range series {
		group, ok := Project(dimensions, aggDimension)
		if !ok {
			tally.Dropped++
			continue
		}
		tally.Groups[group.Key()] = group
	}
	return tally
}

// ParseGroupKey is the exact inverse of Key.
//
// It exists because a group's memory is held under its key and the history
// roster has to expect the groups, not the keys. Storing the group beside the
// key would be the other way, and would put two representations of one identity
// in the state where they can disagree; a round trip that is checked cannot.
//
// It returns false for text this package did not write: a key with no tag, a
// trailing escape, or a pair with no separator. A caller that reaches those has
// state from somewhere else, and the safe reading of it is "not a group I know"
// rather than a group with a name made of whatever the text happened to hold.
func ParseGroupKey(key string) (Group, bool) {
	parts, ok := splitEscaped(key, ',')
	if !ok || len(parts) == 0 {
		return Group{}, false
	}
	tag := parts[len(parts)-1]
	if tag != contract.NoDataDimensionTag+"=true" {
		return Group{}, false
	}
	parts = parts[:len(parts)-1]
	dimensions := make([]Dimension, 0, len(parts))
	for _, part := range parts {
		pair, ok := splitEscaped(part, '=')
		if !ok || len(pair) != 2 {
			return Group{}, false
		}
		name, value := unescapeGroupText(pair[0]), unescapeGroupText(pair[1])
		if name == "" {
			return Group{}, false
		}
		dimensions = append(dimensions, Dimension{Name: name, Value: value})
	}
	if !sort.SliceIsSorted(dimensions, func(left, right int) bool {
		return dimensions[left].Name < dimensions[right].Name
	}) {
		return Group{}, false
	}
	return Group{dimensions: dimensions}, true
}

// splitEscaped splits on a separator that escapeGroupText escapes, so a
// separator inside a value does not split the text that holds it. It reports
// false for a trailing backslash, which cannot appear in text this package
// wrote and means the caller is parsing something else.
func splitEscaped(text string, separator byte) ([]string, bool) {
	parts := make([]string, 0, 4)
	var current []byte
	for index := 0; index < len(text); index++ {
		switch character := text[index]; character {
		case '\\':
			if index+1 >= len(text) {
				return nil, false
			}
			current = append(current, '\\', text[index+1])
			index++
		case separator:
			parts = append(parts, string(current))
			current = current[:0]
		default:
			current = append(current, character)
		}
	}
	return append(parts, string(current)), true
}

var groupTextUnescaper = strings.NewReplacer(`\\`, `\`, `\=`, `=`, `\,`, `,`)

func unescapeGroupText(text string) string { return groupTextUnescaper.Replace(text) }
