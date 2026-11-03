// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package progress

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The paths below name a JSON field by where it sits in a stored progress
// record: object fields joined by ".", "[]" for an element of an array and
// "{}" for a value of a map, whose keys are data, not fields. A path ending in
// ".*" is a value decoded by its own UnmarshalJSON, which DisallowUnknownFields
// does not reach: anything under it is accepted.

var (
	jsonMarshaler   = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	jsonUnmarshaler = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	rawMessageType  = reflect.TypeOf(json.RawMessage(nil))
)

// jsonField is one field of a struct as encoding/json sees it: its name and
// type, with embedded structs without a name of their own flattened.
type jsonField struct {
	name string
	typ  reflect.Type
}

func jsonFieldsOf(typ reflect.Type) []jsonField {
	var fields []jsonField
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				fields = append(fields, jsonFieldsOf(embedded)...)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields = append(fields, jsonField{name: name, typ: field.Type})
	}
	return fields
}

// acceptedJSONPaths is every path a strict decoder of typ accepts, read off
// the type: the previous upstream snapshot's range record is described by
// running it over that release's envelope type.
func acceptedJSONPaths(typ reflect.Type) map[string]bool {
	paths := map[string]bool{}
	var walk func(reflect.Type, string, map[reflect.Type]bool)
	walk = func(typ reflect.Type, path string, onPath map[reflect.Type]bool) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ == rawMessageType || reflect.PointerTo(typ).Implements(jsonUnmarshaler) {
			paths[path+".*"] = true
			return
		}
		if onPath[typ] {
			return
		}
		switch typ.Kind() {
		case reflect.Struct:
			onPath[typ] = true
			for _, field := range jsonFieldsOf(typ) {
				child := strings.TrimPrefix(path+"."+field.name, ".")
				paths[child] = true
				walk(field.typ, child, onPath)
			}
			delete(onPath, typ)
		case reflect.Slice, reflect.Array:
			if typ.Elem().Kind() != reflect.Uint8 {
				walk(typ.Elem(), path+"[]", onPath)
			}
		case reflect.Map:
			walk(typ.Elem(), path+"{}", onPath)
		}
	}
	walk(typ, "", map[reflect.Type]bool{})
	return paths
}

// emittedJSONPaths is every field path the encoded bytes of a value of typ
// carry, read with the type so that a map's keys are not taken for fields.
func emittedJSONPaths(t *testing.T, typ reflect.Type, raw []byte) []string {
	t.Helper()
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func(reflect.Type, string, any)
	walk = func(typ reflect.Type, path string, value any) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ == rawMessageType || typ.Implements(jsonMarshaler) || reflect.PointerTo(typ).Implements(jsonMarshaler) {
			return
		}
		switch typ.Kind() {
		case reflect.Struct:
			object, _ := value.(map[string]any)
			fields := map[string]reflect.Type{}
			for _, field := range jsonFieldsOf(typ) {
				fields[field.name] = field.typ
			}
			for name, child := range object {
				childPath := strings.TrimPrefix(path+"."+name, ".")
				seen[childPath] = true
				if fieldType, known := fields[name]; known {
					walk(fieldType, childPath, child)
				}
			}
		case reflect.Slice, reflect.Array:
			items, _ := value.([]any)
			for _, item := range items {
				walk(typ.Elem(), path+"[]", item)
			}
		case reflect.Map:
			object, _ := value.(map[string]any)
			for _, child := range object {
				walk(typ.Elem(), path+"{}", child)
			}
		}
	}
	walk(typ, "", document)
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// fillEveryField sets every field reachable from value to something other
// than its zero value - one element in each slice and map - so that every
// omitempty field is encoded. A type with its own MarshalJSON and an
// interface are left as they are: what such a value encodes is its own.
func fillEveryField(value reflect.Value, onPath map[reflect.Type]bool) {
	typ := value.Type()
	if typ != rawMessageType && (typ.Implements(jsonMarshaler) || reflect.PointerTo(typ).Implements(jsonMarshaler)) {
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		if onPath[typ.Elem()] {
			return
		}
		value.Set(reflect.New(typ.Elem()))
		fillEveryField(value.Elem(), onPath)
	case reflect.Struct:
		onPath[typ] = true
		for index := 0; index < value.NumField(); index++ {
			if typ.Field(index).IsExported() {
				fillEveryField(value.Field(index), onPath)
			}
		}
		delete(onPath, typ)
	case reflect.Slice:
		if typ == rawMessageType {
			value.Set(reflect.ValueOf(json.RawMessage(`{}`)))
			return
		}
		item := reflect.New(typ.Elem()).Elem()
		fillEveryField(item, onPath)
		value.Set(reflect.Append(reflect.MakeSlice(typ, 0, 1), item))
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			fillEveryField(value.Index(index), onPath)
		}
	case reflect.Map:
		key, item := reflect.New(typ.Key()).Elem(), reflect.New(typ.Elem()).Elem()
		fillEveryField(key, onPath)
		fillEveryField(item, onPath)
		value.Set(reflect.MakeMapWithSize(typ, 1))
		value.SetMapIndex(key, item)
	case reflect.String:
		value.SetString("x")
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(1)
	case reflect.Float32, reflect.Float64:
		value.SetFloat(1)
	}
}

// previousSnapshotRangePaths is the range record fields the previous upstream
// snapshot's decoder accepts, from testdata: acceptedJSONPaths run over that
// release's envelope type, not over this one's.
func previousSnapshotRangePaths(t *testing.T) map[string]bool {
	t.Helper()
	file, err := os.Open("testdata/range_record_paths_previous_snapshot.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	paths := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" && !strings.HasPrefix(line, "#") {
			paths[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no accepted paths in the previous snapshot's list")
	}
	return paths
}

func acceptedBy(paths map[string]bool, path string) bool {
	if paths[path] {
		return true
	}
	for prefix := path; ; {
		cut := strings.LastIndexAny(prefix, ".[{")
		if cut < 0 {
			return false
		}
		prefix = prefix[:cut]
		if paths[prefix+".*"] {
			return true
		}
	}
}

// A range record carries no field the previous upstream snapshot's range
// decoder rejects, whatever the record holds. Range records are decoded with
// DisallowUnknownFields, so during a rolling update from that snapshot a
// field its decoder does not know makes the whole record corrupt to an older
// replica. The record here has every field of every nested type set, so a
// field added to any of them later is encoded unless the range writer leaves
// it out, and then fails here with its path. The accepted fields are the
// previous snapshot's own, generated from its envelope type, so the
// expectation does not come from the type under test.
func TestARangeRecordCarriesOnlyFieldsThePreviousSnapshotDecodes(t *testing.T) {
	var progress execution.ScheduleProgress
	fillEveryField(reflect.ValueOf(&progress).Elem(), map[reflect.Type]bool{})
	if progress.UnfinishedRange == nil || progress.LastCompletion == nil || progress.LastCompletion.Empty == nil {
		t.Fatal("the filled record has no range or no last completion to carry")
	}
	raw, err := encodeValidated(progress)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &header); err != nil || header.Schema != schemaRangeV2 {
		t.Fatalf("the filled record encoded as schema %q (%v), want the range schema %q", header.Schema, err, schemaRangeV2)
	}
	accepted := previousSnapshotRangePaths(t)
	emitted := emittedJSONPaths(t, reflect.TypeOf(envelope{}), raw)
	if len(emitted) < 50 {
		t.Fatalf("the filled range record carries only %d fields: %v", len(emitted), emitted)
	}
	for _, path := range emitted {
		if !acceptedBy(accepted, path) {
			t.Errorf("a range record carries %s, which the previous upstream snapshot's range decoder rejects as unknown", path)
		}
	}
}
