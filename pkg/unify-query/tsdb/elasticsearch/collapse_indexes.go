// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"sort"
	"strings"

	elastic "github.com/olivere/elastic/v7"
)

// mappingFieldNames retains per-index existence, including ES field aliases and
// multi-fields which the merged display field map need not expose.
func mappingFieldNames(mapping map[string]any) map[string]bool {
	fields := make(map[string]bool)
	var visit func(map[string]any, string)
	visit = func(node map[string]any, prefix string) {
		for _, key := range []string{"properties", "fields", "runtime"} {
			children, ok := node[key].(map[string]any)
			if !ok {
				continue
			}
			for name, value := range children {
				child, ok := value.(map[string]any)
				if !ok {
					continue
				}
				full := name
				if prefix != "" {
					full = prefix + "." + name
				}
				fields[full] = true
				visit(child, full)
			}
		}
	}
	var unwrap func(map[string]any)
	unwrap = func(node map[string]any) {
		if _, ok := node["properties"]; ok {
			visit(node, "")
			return
		}
		if _, ok := node["runtime"]; ok {
			visit(node, "")
			return
		}
		for _, value := range node {
			if child, ok := value.(map[string]any); ok {
				unwrap(child)
			}
		}
	}
	unwrap(mapping)
	return fields
}

func cloneIndexFields(source map[string]map[string]bool) map[string]map[string]bool {
	if source == nil {
		return nil
	}
	result := make(map[string]map[string]bool, len(source))
	for index, fields := range source {
		result[index] = make(map[string]bool, len(fields))
		for field, exists := range fields {
			result[index][field] = exists
		}
	}
	return result
}

func filterCollapseIndexes(qo *queryOption, indexFields map[string]map[string]bool, field string, source *elastic.SearchSource) error {
	if field == "" {
		return nil
	}
	// Scroll does not support collapse; preserve ES validation of that request.
	if qo.query != nil && (qo.query.Scroll != "" || (qo.query.ResultTableOption != nil && qo.query.ResultTableOption.ScrollID != "")) {
		return nil
	}
	if indexFields == nil {
		return fmt.Errorf("collapse requires per-index field metadata")
	}
	missing := make([]string, 0)
	for _, index := range qo.physicalIndexes {
		if !indexFields[index][field] {
			missing = append(missing, index)
		}
	}
	if len(qo.physicalIndexes) == 0 || len(missing) == len(qo.physicalIndexes) {
		// Keep the original target scope and explicit match_none. Empty targets
		// mean all ES indices; collapse and sort must not run on unmapped shards.
		*source = *elastic.NewSearchSource().Query(elastic.NewMatchNoneQuery()).Size(0)
		return nil
	}
	if len(missing) == 0 {
		return nil
	}
	missingSet := make(map[string]bool, len(missing))
	known := make(map[string]bool, len(qo.physicalIndexes))
	for _, index := range missing {
		missingSet[index] = true
	}
	for _, index := range qo.physicalIndexes {
		known[index] = true
	}
	targets := make([]string, 0, len(qo.indexes)+len(missing))
	wildcard := false
	for _, target := range qo.indexes {
		switch {
		case strings.ContainsAny(target, "*?"):
			targets = append(targets, target)
			wildcard = true
		case known[target]:
			if !missingSet[target] {
				targets = append(targets, target)
			}
		default:
			// Exact aliases expand after index exclusions and reintroduce missing
			// indices. Replacing them with physical indices bypasses filter/routing.
			return fmt.Errorf("cannot safely exclude unmapped collapse indices from exact alias %q", target)
		}
	}
	if wildcard {
		sort.Strings(missing)
		for _, index := range missing {
			targets = append(targets, "-"+index)
		}
	}
	qo.indexes = targets
	return nil
}
