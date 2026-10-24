// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var updateCheckCodes = flag.Bool("update-check-codes", false, "rewrite check_codes.json from the check tables")

// checkCodesFile is the checks a diagnosis or strategy answer names by code
// -- the vocabulary an agent reads in CLI output -- with the state and action
// each folds to and whose it is. A deployment pins it beside an agent-facing
// reference that explains every code; this file is how that repository knows
// the codes without reading this code.
const checkCodesFile = "check_codes.json"

type checkCodeExport struct {
	Check  Check      `json:"check"`
	State  StateWord  `json:"state"`
	Action ActionWord `json:"action"`
	Owner  Owner      `json:"owner"`
}

func checkCodesExportBytes(t *testing.T) []byte {
	t.Helper()
	export := make([]checkCodeExport, 0, len(checkOrder))
	for _, check := range checkOrder {
		pair := checkWords[check]
		export = append(export, checkCodeExport{Check: check, State: pair.State, Action: pair.Action, Owner: checkAnswers[check].Owner})
	}
	encoded, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

// The exported file is the check tables, code for code and in their order: a
// check added, renamed or re-paired without regenerating the file fails.
func TestCheckCodesFileMatchesTheCode(t *testing.T) {
	want := checkCodesExportBytes(t)
	if *updateCheckCodes {
		if err := os.WriteFile(checkCodesFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(checkCodesFile)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate: go test ./fleet -run TestCheckCodesFileMatchesTheCode -update-check-codes)", checkCodesFile, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from the check tables; regenerate: go test ./fleet -run TestCheckCodesFileMatchesTheCode -update-check-codes", checkCodesFile)
	}
}

// Every check in the order has its pair and its owner in the file: a code an
// agent can meet is never missing from the reference's source.
func TestCheckCodesFileCoversEveryCheck(t *testing.T) {
	raw, err := os.ReadFile(checkCodesFile)
	if err != nil {
		t.Fatal(err)
	}
	var file []checkCodeExport
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	listed := map[Check]checkCodeExport{}
	for _, entry := range file {
		listed[entry.Check] = entry
	}
	for _, check := range checkOrder {
		entry, ok := listed[check]
		if !ok || entry.State == "" || entry.Action == "" || entry.Owner == "" {
			t.Errorf("check %s is missing or incomplete in %s: %+v", check, checkCodesFile, entry)
		}
	}
}
