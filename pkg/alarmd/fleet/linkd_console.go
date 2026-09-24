// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// LinkdConsoleStanding is the Console read against whether the deployment
// needs it.
//
// The Console is needed where events go to the alert link: those alerts
// live in the link, and the two closes that act on them -- calibration and
// the close of strategies that no longer exist -- act only through it. A
// deployment whose events all go the Python-compatible way hands its alerts
// to the Python alert backend, which closes them itself; there an
// unconfigured Console is the design, and saying it on the first screen
// would be a sentence permanently true and never actionable. So the need is
// decided here, from the leader's own count, and the page shows the line
// only when this says so.
type LinkdConsoleStanding struct {
	// Needed is StandardPlans > 0.
	Needed        bool `json:"needed"`
	StandardPlans int  `json:"standard_plans"`
	// State and Reason are the Console entry's, from Replica: the source
	// replica's own entry when it published one -- only the leader walks the
	// roster, so only its entry can say link_unhealthy -- and the dependency
	// list's otherwise.
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Replica string `json:"replica,omitempty"`
	// Attention is Needed with a Console that is not configured, not
	// answering, or answering for a link that says it is behind: the closes
	// the deployment relies on are not running. The first screen says so.
	Attention bool `json:"attention"`
}

// linkdConsoleStandingOf decides the standing once the replicas are folded.
// Nil without a leader round that counted Plans, or without a Console entry
// -- an older build's -- because neither half of the question has an
// answer then.
func linkdConsoleStandingOf(view *View) *LinkdConsoleStanding {
	if view.Source == nil || !view.Source.PlansKnown {
		return nil
	}
	entry, replica := endpointByRole(view.Dependencies, EndpointLinkdConsole), view.DependenciesReplica
	for index := range view.PerReplica {
		if view.PerReplica[index].Replica != view.SourceReplica {
			continue
		}
		if own := endpointByRole(view.PerReplica[index].Dependencies, EndpointLinkdConsole); own != nil && own.Console != nil {
			entry, replica = own, view.SourceReplica
		}
	}
	if entry == nil || entry.Console == nil {
		return nil
	}
	standing := &LinkdConsoleStanding{StandardPlans: view.Source.StandardPlans, Needed: view.Source.StandardPlans > 0,
		State: entry.Console.State, Reason: entry.Console.Reason, Replica: replica}
	switch standing.State {
	case LinkdConsoleNotConfigured, LinkdConsoleUnreadable, LinkdConsoleLinkUnhealthy:
		standing.Attention = standing.Needed
	}
	return standing
}
