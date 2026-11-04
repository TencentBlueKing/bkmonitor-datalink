// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// withoutTheLink is a deployment whose alerts do not go to the link: no
// phase_two.linkd block, on the given output protocol.
func withoutTheLink(protocol string) func(*config.Config, string) {
	return func(cfg *config.Config, address string) {
		cfg.PhaseTwo.Linkd = config.LinkdConfig{}
		cfg.PhaseTwo.Output.Protocol = protocol
		withCompatibilityOutput(cfg, address)
	}
}

// The recovery gate is keyed on the destination. Where the link is the
// destination (a phase_two.linkd block, which does not load without its
// control plane) it reads the link's copy. Where it is not, it answers as
// the self-maintained gate did before the link copy existed: a RECOVERY of
// an alert this process opened passes, one of an alert another process
// opened is held and counted (held_no_open_alert), and a Plan on the
// Python-compatible format is not gated at all (protocol_not_gated).
//
// Host B is absent in round 1 and its no-data alert is raised; in round 2
// its data is back and the round decides the closing RECOVERY - in the
// process that raised it ("own"), or in another that took the Plan over
// from the same store ("foreign").
func TestTheRecoveryGateAnswersByTheDestination(t *testing.T) {
	for name, test := range map[string]struct {
		configure    func(*config.Config, string)
		unrevisioned bool
		// own and foreign are the gate's outcome for the RECOVERY;
		// written is whether the own RECOVERY reaches the sink.
		own, foreign string
		written      bool
	}{
		"the link, native": {own: "passed", foreign: "held_no_open_alert", written: true},
		"no link, native": {configure: withoutTheLink(config.OutputProtocolNative),
			own: "passed", foreign: "held_no_open_alert", written: true},
		"no link, auto, revisioned": {configure: withoutTheLink(config.OutputProtocolAuto),
			own: "passed", foreign: "held_no_open_alert", written: true},
		"no link, auto, unrevisioned": {configure: withoutTheLink(config.OutputProtocolAuto), unrevisioned: true,
			own: "protocol_not_gated", foreign: "protocol_not_gated"},
		"no link, legacy": {configure: withoutTheLink(config.OutputProtocolLegacy),
			own: "protocol_not_gated", foreign: "protocol_not_gated"},
	} {
		t.Run(name, func(t *testing.T) {
			options := roundsOptions{hosts: true, configure: test.configure, unrevisioned: test.unrevisioned}
			open := func(fixture *roundsFixture) string {
				t.Helper()
				fixture.run(1)
				raised, _ := eventsAbout(fixture.written(), "bk_target_ip", followsHostB)
				if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
					t.Fatalf("round 1 sent host B's no-data events %+v, want the anomaly that opens its alert", raised)
				}
				return raised[0].RecordRef.DimensionIdentityDigest
			}

			fixture := startRoundsFixture(t, options)
			series := open(fixture)
			fixture.report(ipSeries(followsHostA), ipSeries(followsHostB))
			mark, sent := fixture.mark(), len(fixture.written())
			fixture.run(2)
			closed, _ := eventsAbout(fixture.written()[sent:], "bk_target_ip", followsHostB)
			if wrote := len(closed) == 1 && closed[0].EventKind == contract.TriggerEventRecovery; wrote != test.written {
				t.Fatalf("own: round 2 wrote host B's no-data events %+v, want the RECOVERY written %t", closed, test.written)
			}
			if got, want := fixture.openAlertGatesFor(mark, series), []observability.OpenAlertGateFact{{Outcome: observability.OpenAlertGateOutcome(test.own), Records: 1}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("own: the gate answered %+v, want %+v", got, want)
			}

			first := startRoundsFixture(t, options)
			series = open(first)
			first.stopBundle()
			options.address, options.client, options.base = first.address, first.redis, first.base
			taken := startRoundsFixture(t, options)
			taken.report(ipSeries(followsHostA), ipSeries(followsHostB))
			mark = taken.mark()
			taken.run(2)
			if held, _ := eventsAbout(taken.written(), "bk_target_ip", followsHostB); test.foreign == "held_no_open_alert" && len(held) != 0 {
				t.Fatalf("foreign: round 2 wrote host B's no-data events %+v; this process opened no alert for it", held)
			}
			if got, want := taken.openAlertGatesFor(mark, series), []observability.OpenAlertGateFact{{Outcome: observability.OpenAlertGateOutcome(test.foreign), Records: 1}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("foreign: the gate answered %+v, want %+v", got, want)
			}
		})
	}
}

// The existing bk-monitor deployment's shape, end to end: a Control and
// channel process and a Worker process with no phase_two.linkd block, on
// auto (its source publishes no strategy_revision, so every Plan is on the
// Python-compatible format) and on native (a revisioned strategy, on the
// standard raw event, the platform's own alert pipeline). Each role set
// loads from a file; the bundle builds and activates the Plan, owns its
// Query Group, and holds no RECOVERY of an alert it opened.
func TestTheExistingDeploymentShapeStartsActivatesAndHoldsNoOwnRecovery(t *testing.T) {
	application := validGoAccessApplicationYAML()
	if !strings.Contains(application, applicationLinkdControlPlaneYAML) {
		t.Fatal("the application fixture states no linkd block; removing it would prove nothing")
	}
	application = strings.Replace(application, applicationLinkdControlPlaneYAML, "", 1)
	for name, test := range map[string]struct {
		protocol     string
		unrevisioned bool
		own          string
	}{
		"auto, unrevisioned": {protocol: config.OutputProtocolAuto, unrevisioned: true, own: "protocol_not_gated"},
		"native, revisioned": {protocol: config.OutputProtocolNative, own: "passed"},
	} {
		t.Run(name, func(t *testing.T) {
			contents := strings.Replace(application, "phase_two:\n", "phase_two:\n  output:\n    protocol: "+test.protocol+"\n", 1)
			path := filepath.Join(t.TempDir(), "alarmd.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, selected := range []roles.Set{{roles.Control, roles.Channel}, {roles.Worker}} {
				loaded, err := config.LoadWithCredentials(path, selected, config.CredentialsForRoles)
				if err != nil {
					t.Fatalf("roles %v: %v, want the deployment to load", selected, err)
				}
				if loaded.PhaseTwo.Linkd.Present() || loaded.OutputProtocol() != test.protocol {
					t.Fatalf("roles %v: link present %t, protocol %q", selected, loaded.PhaseTwo.Linkd.Present(), loaded.OutputProtocol())
				}
			}
			fixture := startRoundsFixture(t, roundsOptions{hosts: true, unrevisioned: test.unrevisioned,
				configure: withoutTheLink(test.protocol)})
			fixture.run(1)
			raised, _ := eventsAbout(fixture.written(), "bk_target_ip", followsHostB)
			if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("round 1 sent host B's no-data events %+v, want its anomaly", raised)
			}
			fixture.report(ipSeries(followsHostA), ipSeries(followsHostB))
			mark := fixture.mark()
			fixture.run(2)
			want := []observability.OpenAlertGateFact{{Outcome: observability.OpenAlertGateOutcome(test.own), Records: 1}}
			if got := fixture.openAlertGatesFor(mark, raised[0].RecordRef.DimensionIdentityDigest); !reflect.DeepEqual(got, want) {
				t.Fatalf("the gate answered %+v for the RECOVERY of an alert this process opened, want %+v", got, want)
			}
		})
	}
}
