// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import "sort"

// HostCandidate is one group this round may expect together with the host it
// names, so a caller that resolves the host can answer keyed by the group.
//
// The two keys are different and both are needed. A CMDB lookup is by host
// identity; the answers the evaluation reads - the expected set and the groups
// whose host left the business - are by group key. Returning the pair is what
// keeps the caller from deriving one from the other and getting it wrong for
// the groups where the mapping is not obvious.
type HostCandidate struct {
	GroupKey string
	Host     HostIdentity
}

// Key is the CMDB identity, in the shape the index is keyed by.
func (candidate HostCandidate) Key() string {
	return candidate.Host.IP + "|" + candidate.Host.CloudID
}

// HostCandidates is every host this round needs resolved, derived from the same
// classification the roster is built from.
//
// Both sources matter and for different reasons. A static target's hosts have
// to be resolved because the expected set is the declared hosts intersected
// with the business's - a host in the list that CMDB does not hold is not
// expected, which is what the backend does. A history roster's hosts have to be
// resolved because that roster only grows: a host that left the business would
// otherwise stay in the expected set and be reported absent forever, which is
// the failure the out-of-business rule exists to stop, and the one place it
// matters most.
//
// It reads the classification rather than repeating it, so the set of hosts
// asked about and the set the roster expects cannot drift apart. A caller that
// walked the target itself would be deriving the same thing a second time, and
// the two would agree until a target shape came along where they did not.
func HostCandidates(request RosterRequest) ([]HostCandidate, error) {
	class, err := ClassifyRoster(request.Scope, request.AggDimension)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	candidates := make([]HostCandidate, 0, len(class.Hosts))
	add := func(groupKey string, host HostIdentity) {
		if host.IP == "" || host.CloudID == "" {
			return
		}
		if _, duplicate := seen[groupKey]; duplicate {
			return
		}
		seen[groupKey] = struct{}{}
		candidates = append(candidates, HostCandidate{GroupKey: groupKey, Host: host})
	}
	switch class.Source {
	case RosterTargetStatic:
		for _, host := range class.Hosts {
			add(hostTargetGroup(host).Key(), host)
		}
	case RosterHistory:
		for key, group := range historyGroups(request.Memory) {
			host, ok := groupHostIdentity(group)
			if !ok {
				// A remembered group whose dimensions do not name a host. There
				// is no identity to resolve, so there is nothing CMDB can say
				// about it - which is not the same as it being fine, only that
				// this is not the question CMDB answers.
				continue
			}
			add(key, host)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].GroupKey < candidates[right].GroupKey
	})
	return candidates, nil
}

// groupHostIdentity reads the host pair out of a group's own dimensions, and
// says so when the group does not carry one.
func groupHostIdentity(group Group) (HostIdentity, bool) {
	var host HostIdentity
	for _, dimension := range group.Dimensions() {
		switch dimension.Name {
		case HostIPDimension:
			host.IP = dimension.Value
		case HostCloudDimension:
			host.CloudID = dimension.Value
		}
	}
	return host, host.IP != "" && host.CloudID != ""
}

// HostResolution is what a CMDB pass over the candidates produced.
//
// The two sets answer different questions and a host can be in neither: one
// CMDB does not hold at all is not expected and has not left the business, it
// is simply not a host of this deployment as far as the index knows.
type HostResolution struct {
	// Resolved says the pass had an index to answer from. Without it, Known
	// being empty has two meanings that call for opposite behaviour: a target
	// whose hosts are all gone -- a real, empty expected set -- and a process
	// whose host index has not been built, which knows nothing about the
	// target either way. Acting on the second as though it were the first
	// turns every static-target item into one that expects nothing, so a cold
	// index reports the item as a whole absent and leaves the absences open on
	// its hosts with nothing that will ever close them.
	Resolved bool
	// Known is the "ip|cloud" set the expected roster intersects with.
	Known map[string]struct{}
	// OutOfBusiness is keyed by group, because that is what the evaluation asks
	// about - not by host, which it never sees.
	OutOfBusiness map[string]struct{}
}
