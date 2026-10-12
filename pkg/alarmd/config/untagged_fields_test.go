// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"reflect"
	"strings"
	"testing"
)

// Every exported field the decoder can reach names its key or says it has
// none. An untagged field is decoded under its lowercased Go name, so a value
// the program derives - the query permits, the replay window, the queue
// capacities - could be overwritten by a values file that spells it run
// together, and the strict decoder would accept it: a configuration surface
// nobody declared. A derived field is tagged yaml:"-" and is then refused by
// name like any key that does not exist.
func TestEveryDecodedFieldNamesItsKeyOrHasNone(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(path string, kind reflect.Type)
	walk = func(path string, kind reflect.Type) {
		for kind.Kind() == reflect.Pointer || kind.Kind() == reflect.Slice || kind.Kind() == reflect.Map {
			kind = kind.Elem()
		}
		if kind.Kind() != reflect.Struct || seen[kind] {
			return
		}
		seen[kind] = true
		for index := 0; index < kind.NumField(); index++ {
			field := kind.Field(index)
			if !field.IsExported() {
				continue
			}
			tag, tagged := field.Tag.Lookup("yaml")
			if !tagged {
				t.Errorf("%s.%s has no yaml tag: values can set it as %q", path, field.Name, strings.ToLower(field.Name))
				continue
			}
			if tag == "-" {
				continue
			}
			walk(path+"."+field.Name, field.Type)
		}
	}
	walk("Config", reflect.TypeOf(Config{}))
}
