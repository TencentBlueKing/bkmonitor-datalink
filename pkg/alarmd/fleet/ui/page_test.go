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
	"reflect"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
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

// A control the page draws but never wires is the same failure as a renderer
// nobody calls, seen from the other side: the operator clicks it and the page
// does nothing, with every test green. The capacity panel was the renderer
// half of this; this is the control half.
func TestEveryIdentifiedButtonIsWired(t *testing.T) {
	body := string(page)
	button := regexp.MustCompile(`<button[^>]*\bid="([A-Za-z0-9_]+)"`)
	matches := button.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no identified buttons found; the check would pass vacuously")
	}
	for _, match := range matches {
		id := match[1]
		// The page wires every identified button the same way, so that exact
		// form is what is required. A button wired some other way fails this
		// and gets looked at, which is the right outcome: a check that accepts
		// any shape accepts a button with no handler at all.
		if !strings.Contains(body, `getElementById('`+id+`').addEventListener`) {
			t.Errorf("button %q is drawn but nothing listens to it: clicking it does nothing", id)
		}
	}
}

// Every field the page reads off the capacity object has to be a field the API
// actually sends. A name that does not match reads as undefined, the guard in
// front of its cell is then false, and the cell never appears -- with the
// numbers present in every response the whole time. That is how the memory cell
// was missing: the JSON carries memory_used_bytes and the page asked for
// memory_used.
//
// Nothing else catches it. The renderer is called, the container is written,
// build and vet are clean, and the only symptom is a cell that is not there,
// which looks exactly like a deployment that has no such data.
func TestEveryCapacityFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "cap", reflect.TypeOf(fleet.CapacityView{}))
}

// The same failure on the other response, and it shipped too: the records panel
// promised "保留 0 分钟" because the JSON carries retention_seconds and the page
// asked for retention. A missing field is not an error in JavaScript, so the
// page renders the zero and reads as a deployment whose records expire
// instantly.
//
// This is why the detail response is the one response body in that package that
// is exported.
func TestEveryDetailFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "objectDetail", reflect.TypeOf(fleet.DetailResponse{}))
}

// A kind the page has no wording for does not render as an error. It renders as
// whichever word the fallback reaches for, so a new classification appears as an
// ordinary row of a familiar kind -- and the page was doing exactly that, asking
// "is it BLOCKED_RUN, otherwise it is degraded", which quietly renamed every
// kind that came after those two.
func TestThePageHasWordingForEveryAnomalyKind(t *testing.T) {
	if len(fleet.AnomalyKinds) == 0 {
		t.Fatal("no anomaly kinds declared; the check would pass vacuously")
	}
	for _, kind := range fleet.AnomalyKinds {
		if !strings.Contains(string(page), kind+":") {
			t.Errorf("the page has no wording for anomaly kind %q: it would render as another kind's word", kind)
		}
	}
}

// The verdict route was the one response this check could not cover, because it
// answered with a map and a map has no fields to reflect over. That is where it
// went wrong: four columns were added to the view and to the page in one change,
// the map in between was not, and the four new cells rendered "undefined" on a
// live deployment for as long as it took someone to read the JSON by hand.
//
// It answers with a type now, so this is the same check as the other two.
func TestEveryVerdictFieldThePageReadsExistsInTheAPI(t *testing.T) {
	assertFieldsExist(t, "deployment", reflect.TypeOf(fleet.HealthResponse{}))
}

// assertFieldsExist checks every `<object>.<field>` the page reads against the
// JSON the Go type actually sends.
func assertFieldsExist(t *testing.T, object string, response reflect.Type) {
	t.Helper()
	sent := map[string]bool{}
	for index := 0; index < response.NumField(); index++ {
		tag := response.Field(index).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			sent[name] = true
		}
	}
	if len(sent) == 0 {
		t.Fatalf("no JSON fields found on %s; the check would pass vacuously", response.Name())
	}

	read := regexp.MustCompile(`\b` + object + `\.([a-z0-9_]+)`)
	matches := read.FindAllStringSubmatch(string(page), -1)
	if len(matches) == 0 {
		t.Fatalf("the page reads no %s fields; the check would pass vacuously", object)
	}
	for _, match := range matches {
		if !sent[match[1]] {
			t.Errorf("the page reads %s.%s, which %s does not send: it reads as undefined and renders nothing",
				object, match[1], response.Name())
		}
	}
}
