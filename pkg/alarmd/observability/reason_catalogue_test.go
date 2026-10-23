// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every reason a site hands an observation by name -- a literal, or one of
// the contract's constants -- is a word the catalogue keeps. One it does not
// is normalized to _other and its lines read as an unnamed failure: the
// shape a no-data stall's outcome and a permit timeout had. Reasons computed
// at run time are held by the closed-list cases beside the lists they come
// from (the worker's no-data and ownership cases, the permit words here).
func TestEveryReasonASiteNamesIsInTheCatalogue(t *testing.T) {
	contractValues := map[string]string{}
	constant := regexp.MustCompile(`(?m)^\s*(Reason[A-Za-z0-9]+)\s*=\s*"([^"]+)"`)
	contractFiles, _ := filepath.Glob(filepath.Join("..", "contract", "*.go"))
	for _, path := range contractFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range constant.FindAllStringSubmatch(string(body), -1) {
			contractValues[match[1]] = match[2]
		}
	}
	if len(contractValues) < 100 {
		t.Fatalf("read %d contract reasons: the scan is not reading the contract", len(contractValues))
	}
	// A conversion to an observation's or an execution's reason type, by
	// literal or by contract constant, and a constant declared of either
	// type; the execution's reasons reach observations as completion
	// reasons.
	named := regexp.MustCompile(`\bReasonCode\(\s*(?:"([^"]+)"|contract\.(Reason[A-Za-z0-9]+))\s*\)|\bReasonCode\s*=\s*"([^"]*)"()`)
	known := map[ReasonCode]bool{ReasonNone: true}
	for _, reason := range AllLogReasons() {
		known[reason] = true
	}
	checked := 0
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range named.FindAllStringSubmatch(string(body), -1) {
			word := match[1] + match[3]
			if match[2] != "" {
				value, found := contractValues[match[2]]
				if !found {
					t.Errorf("%s names contract.%s, which the scan could not read", path, match[2])
					continue
				}
				word = value
			}
			checked++
			if !known[ReasonCode(word)] {
				t.Errorf("%s hands an observation %q, which the catalogue does not keep: its lines would read _other", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("checked %d named reasons: the scan is not reading the sites", checked)
	}
}
