// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package sourcereads lists every key of a strategy document alarmd reads,
// for the tests that hold the operator evidence's source view to it. Only
// tests import it: the list gathers the reads of packages the production
// code does not otherwise join.
package sourcereads

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
)

// Paths is every key of a strategy document this build reads, as a dotted
// path with arrays left out (items.query_configs.filter_dict): the union of
// Reads, each struct walked by its json tags.
func Paths() []string {
	paths := map[string]bool{}
	for _, read := range Reads() {
		if read.Path != "" {
			paths[read.Path] = true
		}
		switch decoded := read.Decoded.(type) {
		case reflect.Type:
			walk(decoded, read.Path, paths)
		case []string:
			for _, key := range decoded {
				paths[join(read.Path, key)] = true
			}
		}
	}
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// Reads is every place a strategy document, or the frozen copy of it a plan
// carries to its outputs, is read: the control plane's own reads
// (controlplane.LegacySourceReads) and those of the other packages that read
// it. A package that starts reading the document is added here.
func Reads() []controlplane.SourceRead {
	reads := controlplane.LegacySourceReads()
	// The frozen copy: the frozen output's validation and the legacy output
	// converter's decodes.
	reads = append(reads,
		controlplane.SourceRead{Path: "", Decoded: []string{"id", "bk_biz_id", "update_time"}}, // contract.LegacyOutputContext.Validate
	)
	for _, source := range legacyoutput.FrozenStrategySources() {
		reads = append(reads, controlplane.SourceRead{Path: "", Decoded: source})
	}
	return reads
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

func walk(kind reflect.Type, prefix string, paths map[string]bool) {
	for kind.Kind() == reflect.Pointer || kind.Kind() == reflect.Slice {
		if kind == rawMessageType {
			return
		}
		kind = kind.Elem()
	}
	if kind.Kind() != reflect.Struct {
		return
	}
	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}
		path := join(prefix, name)
		paths[path] = true
		walk(field.Type, path, paths)
	}
}
