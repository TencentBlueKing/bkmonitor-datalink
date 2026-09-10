// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ui

import (
	"regexp"
	"strings"
	"testing"
)

// The page is embedded in the binary and nothing renders it in a test, so a
// section whose renderer was defined and never wired appeared in production as
// a blank panel with build, vet and every unit test green. That is what
// happened to the capacity panel: the edit that was supposed to add the call
// did not match, changed nothing, and nothing noticed.
//
// This is deliberately a weak check. It cannot say the page is correct, only
// that nothing in it is dead code -- which is the exact shape of that failure.
func TestNoPageFunctionIsDefinedWithoutACallSite(t *testing.T) {
	body := string(page)
	declaration := regexp.MustCompile(`(?m)^function ([A-Za-z0-9_]+)\(`)
	matches := declaration.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no page functions found; the check would pass vacuously")
	}
	for _, match := range matches {
		name := match[1]
		// One occurrence is the declaration itself. A renderer nobody calls
		// renders nothing, and a panel that renders nothing looks exactly like
		// a deployment with no data.
		if strings.Count(body, name+"(") < 2 {
			t.Errorf("page function %q is defined but never called: its section would render blank", name)
		}
	}
}

// Every panel the page declares has to be filled by something. An element id
// that no script ever writes is a heading with nothing under it.
func TestEveryPanelContainerIsWrittenBySomeScript(t *testing.T) {
	body := string(page)
	container := regexp.MustCompile(`id="([A-Za-z0-9_]+)"`)
	seen := map[string]bool{}
	for _, match := range container.FindAllStringSubmatch(body, -1) {
		id := match[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		// The declaration plus at least one script reference.
		if strings.Count(body, id) < 2 {
			t.Errorf("element %q is declared but never written by any script", id)
		}
	}
}
