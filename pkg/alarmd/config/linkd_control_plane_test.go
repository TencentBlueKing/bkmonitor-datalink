// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// The link control plane's JWT secret comes from a file and only from one:
// read and cleared, its defaults applied, and a secret written inline is
// refused. A control plane without the file is no control plane, and a
// stated link refuses it (TestAStatedLinkNeedsItsControlPlaneAndNoLinkNeverDoes).
func TestTheLinkControlPlaneSecretComesOnlyFromAFile(t *testing.T) {
	clearCredentialEnvironment(t)
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"phase_two.linkd": {
		"control_plane:", "  url: http://linkd.example.test:8080",
		"  jwt_secret_file: " + writeCredential(t, "jwt", "example-secret\n")}})), nil)
	if err != nil {
		t.Fatal(err)
	}
	plane := cfg.PhaseTwo.Linkd.ControlPlane
	if plane.JWTSecret != "example-secret" || plane.JWTSecretFile != "" || plane.Username() != "alarmd" || plane.Source() != "built_in_bk" || !plane.Configured() {
		t.Fatalf("control plane = %+v", plane)
	}
	if _, err = LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"phase_two.linkd": {
		"control_plane:", "  url: http://linkd.example.test:8080"}})), nil); !errors.Is(err, ErrLinkdControlPlaneRequired) {
		t.Fatalf("a control plane without the secret: %v, want %v", err, ErrLinkdControlPlaneRequired)
	}
	for name, block := range map[string][]string{
		"inline":      {"control_plane:", "  url: http://linkd.example.test:8080", "  jwt_secret: example-secret"},
		"URL user":    {"control_plane:", "  url: http://a:b@linkd.example.test", "  jwt_secret_file: " + writeCredential(t, "jwt", "x\n")},
		"source path": {"control_plane:", "  url: http://linkd.example.test", "  event_source_id: a/b", "  jwt_secret_file: " + writeCredential(t, "jwt", "x\n")},
	} {
		if _, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"phase_two.linkd": block})), nil); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// The resolved secret is never written out: not in YAML, not in JSON (a
// configuration printed for a reader or a fact page).
func TestTheLinkControlPlaneSecretIsNeverMarshalled(t *testing.T) {
	plane := LinkdControlPlaneConfig{URL: "http://192.0.2.10:8090", JWTSecretFile: "/etc/alarmd/secrets/linkd-jwt-secret",
		JWTSecret: "example-secret"}
	encoded, err := json.Marshal(plane)
	if err != nil || strings.Contains(string(encoded), "example-secret") {
		t.Fatalf("json %s (%v) carries the secret", encoded, err)
	}
}

// The startup refusal is keyed on the destination, never on the output
// protocol. A deployment that states the link - a phase_two.linkd block,
// even an empty one - does not load as a Control or Worker without a whole
// control plane: it could read none of the link's alerts back or close
// them. A deployment without the block - the existing bk-monitor
// deployment, any fresh bk-monitor install - always loads, on native, auto,
// unset or legacy alike: there the standard raw event feeds the platform's
// own alert pipeline, and the wire format says nothing about the link.
func TestAStatedLinkNeedsItsControlPlaneAndNoLinkNeverDoes(t *testing.T) {
	clearCredentialEnvironment(t)
	withProtocol := func(text, protocol string) string {
		if protocol == "" {
			return text
		}
		return strings.Replace(text, "phase_two:\n", "phase_two:\n  output:\n    protocol: "+protocol+"\n", 1)
	}
	empty := credentialConfig(map[string][]string{"phase_two.linkd": nil})
	if !strings.Contains(empty, "  linkd:\n") {
		t.Fatal("the fixture states no linkd block; the cases below would not be about one")
	}
	absent := strings.Replace(empty, "  linkd:\n", "", 1)
	incomplete := credentialConfig(map[string][]string{"phase_two.linkd": {"control_plane:", "  url: http://linkd.example.test:8080"}})
	secretOnly := credentialConfig(map[string][]string{"phase_two.linkd": {"control_plane:",
		"  jwt_secret_file: " + writeCredential(t, "jwt", "example-secret\n")}})
	whole := credentialConfig(nil)
	protocols := []string{"", OutputProtocolAuto, OutputProtocolLegacy, OutputProtocolNative}
	for _, selected := range []roles.Set{nil, {roles.Control}, {roles.Worker}} {
		for _, protocol := range protocols {
			for name, text := range map[string]string{"an empty linkd block": empty, "a control plane without its secret": incomplete,
				"a control plane without its url": secretOnly} {
				if _, err := LoadWithRoles(writeConfig(t, withProtocol(text, protocol)), selected); !errors.Is(err, ErrLinkdControlPlaneRequired) {
					t.Fatalf("%s, protocol %q, roles %v: %v, want %v", name, protocol, selected, err, ErrLinkdControlPlaneRequired)
				}
			}
			loaded, err := LoadWithRoles(writeConfig(t, withProtocol(whole, protocol)), selected)
			if err != nil || !loaded.PhaseTwo.Linkd.Present() || !loaded.PhaseTwo.Linkd.ControlPlane.Configured() {
				t.Fatalf("a whole control plane, protocol %q, roles %v: %v", protocol, selected, err)
			}
			loaded, err = LoadWithRoles(writeConfig(t, withProtocol(absent, protocol)), selected)
			if err != nil || loaded.PhaseTwo.Linkd.Present() {
				t.Fatalf("no linkd block, protocol %q, roles %v: %v (present %t), want it to load without the link",
					protocol, selected, err, err == nil && loaded.PhaseTwo.Linkd.Present())
			}
		}
	}
	// A configuration built in code states the link by naming any part of
	// its control plane, or by the decoder's flag.
	for _, protocol := range protocols {
		cfg := validGoAccessConfigObject()
		cfg.PhaseTwo.Output.Protocol = protocol
		cfg.PhaseTwo.Linkd = LinkdConfig{}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("an object without the link, protocol %q: %v, want it valid", protocol, err)
		}
		cfg.PhaseTwo.Linkd = LinkdConfig{Declared: true}
		if err := cfg.Validate(); !errors.Is(err, ErrLinkdControlPlaneRequired) {
			t.Fatalf("an object stating the link without its plane, protocol %q: %v, want %v", protocol, err, ErrLinkdControlPlaneRequired)
		}
		cfg.PhaseTwo.Linkd = LinkdConfig{ControlPlane: LinkdControlPlaneConfig{URL: "http://192.0.2.10:8090"}}
		if err := cfg.Validate(); !errors.Is(err, ErrLinkdControlPlaneRequired) {
			t.Fatalf("an object naming the plane without its secret, protocol %q: %v, want %v", protocol, err, ErrLinkdControlPlaneRequired)
		}
	}
}
