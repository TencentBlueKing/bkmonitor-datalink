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
	"regexp"
	"sort"
	"strings"

	elastic "github.com/olivere/elastic/v7"
)

type collapseIndexMetadata struct {
	fields          map[string]map[string]bool
	directQuerySafe map[string]bool
}

func cloneCollapseIndexMetadata(source *collapseIndexMetadata) *collapseIndexMetadata {
	if source == nil {
		return nil
	}
	cloned := &collapseIndexMetadata{fields: cloneIndexFields(source.fields), directQuerySafe: make(map[string]bool, len(source.directQuerySafe))}
	for index, safe := range source.directQuerySafe {
		cloned.directQuerySafe[index] = safe
	}
	return cloned
}

// Only convert aliases after proving that direct access cannot bypass an alias
// filter or search routing. Mapping-only responses cannot establish that proof.
func collapseDirectQuerySafe(index string, aliases map[string]any, targets []string) bool {
	matchedAlias := false
	for _, target := range targets {
		if strings.HasPrefix(target, "-") || strings.HasPrefix(target, "<") {
			return false
		}
	}
	for _, target := range targets {
		if collapseTargetMatches(target, index) {
			return true
		}
	}
	for alias, value := range aliases {
		matched := false
		for _, target := range targets {
			if collapseTargetMatches(target, alias) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		options, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, key := range []string{"filter", "routing", "search_routing"} {
			if value, exists := options[key]; exists && value != nil && value != "" {
				return false
			}
		}
		matchedAlias = true
	}
	return matchedAlias
}

func collapseTargetMatches(pattern, name string) bool {
	expression := regexp.QuoteMeta(pattern)
	expression = strings.ReplaceAll(expression, `\*`, ".*")
	expression = strings.ReplaceAll(expression, `\?`, ".")
	matched, _ := regexp.MatchString("^"+expression+"$", name)
	return matched
}

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

func filterCollapseIndexes(qo *queryOption, indexFields *collapseIndexMetadata, field string, source *elastic.SearchSource) error {
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
		if !indexFields.fields[index][field] {
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
	// Use only the mapped concrete snapshot. Alias exclusion expressions are
	// not sufficient: the deployed backend can still resolve excluded shards.
	targets := make([]string, 0, len(qo.physicalIndexes)-len(missing))
	for _, index := range qo.physicalIndexes {
		if !indexFields.fields[index][field] {
			continue
		}
		safe := indexFields.directQuerySafe[index]
		// Explicit index targets are also safe when only GetMapping is available.
		for _, target := range qo.indexes {
			if target == index {
				safe = true
			}
		}
		if !safe {
			return fmt.Errorf("cannot safely select concrete collapse indices: alias scope is restricted or unknown for %q", index)
		}
		targets = append(targets, index)
	}
	sort.Strings(targets)
	qo.indexes = targets
	qo.physicalIndexes = append([]string(nil), targets...)
	return nil
}
