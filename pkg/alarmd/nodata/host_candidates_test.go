// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"errors"
	"testing"
)

func hostDimensions() []string { return []string{HostIPDimension, HostCloudDimension} }

// A static target's declared hosts are what gets resolved, keyed by the group
// each one will be judged as.
func TestHostCandidatesAreTheStaticTargetsOwnHosts(t *testing.T) {
	scope := hostScope("10.0.0.2|0", "10.0.0.1|0")
	candidates, err := HostCandidates(RosterRequest{AggDimension: hostDimensions(), Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want one per declared host", candidates)
	}
	for index := 1; index < len(candidates); index++ {
		if candidates[index-1].GroupKey >= candidates[index].GroupKey {
			t.Fatalf("candidates are not in a stable order: %+v", candidates)
		}
	}
	for _, candidate := range candidates {
		want := hostTargetGroup(candidate.Host).Key()
		if candidate.GroupKey != want {
			t.Fatalf("candidate %+v is keyed %q, want the group it will be judged as, %q",
				candidate, candidate.GroupKey, want)
		}
	}

	// The same classification the roster is built from, so the hosts asked
	// about and the hosts expected cannot drift apart.
	roster, err := BuildRoster(RosterRequest{
		AggDimension: hostDimensions(), Scope: scope,
		KnownHosts: knownHosts("10.0.0.1|0", "10.0.0.2|0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if _, expected := roster.Groups[candidate.GroupKey]; !expected {
			t.Fatalf("candidate %q is not a group the roster expects", candidate.GroupKey)
		}
	}
}

// A history roster's hosts are resolved too, and that is where it matters
// most: the roster only grows, so a host that left the business would stay in
// the expected set and be reported absent for as long as the strategy lives.
func TestHostCandidatesCoverARememberedHistoryRoster(t *testing.T) {
	present := hostTargetGroup(HostIdentity{IP: "10.0.0.1", CloudID: "0"})
	other := Group{dimensions: []Dimension{{Name: "device", Value: "eth0"}}}
	memory := map[string]GroupMemory{
		present.Key(): {LastSeen: 940},
		other.Key():   {FirstAbsent: 900},
	}

	candidates, err := HostCandidates(RosterRequest{AggDimension: hostDimensions(), Memory: memory})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want only the remembered group that names a host", candidates)
	}
	if candidates[0].GroupKey != present.Key() {
		t.Fatalf("candidate = %q, want %q", candidates[0].GroupKey, present.Key())
	}
	if candidates[0].Key() != "10.0.0.1|0" {
		t.Fatalf("lookup key = %q, want the CMDB identity", candidates[0].Key())
	}
}

// An item judged as a whole has no host to resolve, and asking about one would
// be asking about a group that is not addressed by host at all.
func TestHostCandidatesAreEmptyForAWholeItem(t *testing.T) {
	candidates, err := HostCandidates(RosterRequest{
		AggDimension: []string{"device"}, Scope: hostScope("10.0.0.1|0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none for an item judged as a whole", candidates)
	}
}

// A Plan whose roster cannot be derived is refused here for the same reason it
// is refused there, and by the same call: resolving hosts for a roster that
// will not be built would be work toward an answer nobody can give.
func TestHostCandidatesRefuseARosterThatCannotBeDerived(t *testing.T) {
	_, err := HostCandidates(RosterRequest{
		AggDimension: []string{HostIPDimension, HostCloudDimension, "device"},
		Scope:        hostScope("10.0.0.1|0"),
	})
	var unsupported *RosterUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("HostCandidates() = %v, want a RosterUnsupportedError", err)
	}
}
