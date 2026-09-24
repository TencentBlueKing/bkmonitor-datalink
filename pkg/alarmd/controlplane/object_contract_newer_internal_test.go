// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"errors"
	"testing"
)

// An object of a later version of a contract this build reads is refused as
// newer, an object of a version this build reads is read, and bytes that
// name no version of the contract are refused as not an object. The three
// answers are kept apart because a reader acts on them in opposite ways: a
// newer object is a rollout the replica is the old side of, and it clears
// by itself; a corrupt object is a defect in the store. The v3 bump for the
// effective-time snapshot is the case in hand: every v2 replica meets v3
// objects for as long as the rollout takes.
func TestALaterVersionOfTheContractIsNewerNotCorrupt(t *testing.T) {
	for name, tt := range map[string]struct {
		payload  string
		domain   func([]byte) (string, error)
		wantName string
		newer    bool
		invalid  bool
	}{
		"query group v3 is read":      {`{"object_contract_version":"alarmd-query-group-object-v3"}`, queryGroupObjectDomain, queryGroupObjectContractVersionV3, false, false},
		"query group v4 is newer":     {`{"object_contract_version":"alarmd-query-group-object-v4"}`, queryGroupObjectDomain, "", true, false},
		"query group v999 is newer":   {`{"object_contract_version":"alarmd-query-group-object-v999"}`, queryGroupObjectDomain, "", true, false},
		"query group v0 is invalid":   {`{"object_contract_version":"alarmd-query-group-object-v0"}`, queryGroupObjectDomain, "", false, true},
		"query group vx is invalid":   {`{"object_contract_version":"alarmd-query-group-object-vx"}`, queryGroupObjectDomain, "", false, true},
		"another contract is invalid": {`{"object_contract_version":"alarmd-output-context-v9"}`, queryGroupObjectDomain, "", false, true},
		"no version is invalid":       {`{}`, queryGroupObjectDomain, "", false, true},
		"output context v1 is read":   {`{"output_context_contract_version":"alarmd-output-context-v1"}`, outputContextDomain, outputContextContractVersion, false, false},
		"output context v2 is newer":  {`{"output_context_contract_version":"alarmd-output-context-v2"}`, outputContextDomain, "", true, false},
		"output context v1x invalid":  {`{"output_context_contract_version":"alarmd-output-context-v1x"}`, outputContextDomain, "", false, true},
	} {
		t.Run(name, func(t *testing.T) {
			domain, err := tt.domain([]byte(tt.payload))
			switch {
			case tt.newer:
				if !errors.Is(err, ErrCatalogObjectContractNewer) {
					t.Fatalf("want newer, got domain %q err %v", domain, err)
				}
			case tt.invalid:
				if err == nil || errors.Is(err, ErrCatalogObjectContractNewer) {
					t.Fatalf("want not an object, got domain %q err %v", domain, err)
				}
			default:
				if err != nil || domain != tt.wantName {
					t.Fatalf("want domain %q, got %q err %v", tt.wantName, domain, err)
				}
			}
		})
	}
	// A cutover that meets a newer object names it as such rather than as a
	// digest mismatch, and the word is in the closed list the metric
	// pre-creates.
	if reason := cutoverFailureReason(ErrCatalogObjectContractNewer); reason != CutoverReasonObjectNewer {
		t.Fatalf("a newer object cut over as %q", reason)
	}
	found := false
	for _, reason := range CutoverReasons {
		found = found || reason == CutoverReasonObjectNewer
	}
	if !found {
		t.Fatal("object_newer is not in the cutover reason list the metric pre-creates")
	}
}
