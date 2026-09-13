// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"strings"
	"testing"
)

// The default must put diagnostics on its own loopback address: deployments
// that never touch the new key still stop exposing pprof on the routable
// surface, and port-forward keeps reaching it.
func TestDefaultSeparatesTheDiagnosticsSurface(t *testing.T) {
	cfg := Default()
	if cfg.HTTP.DiagnosticsListen == "" {
		t.Fatal("default diagnostics_listen is empty, want a loopback address")
	}
	if cfg.HTTP.DiagnosticsListen == cfg.HTTP.Listen {
		t.Fatalf("default diagnostics_listen %q equals http listen", cfg.HTTP.DiagnosticsListen)
	}
	if !strings.HasPrefix(cfg.HTTP.DiagnosticsListen, "127.0.0.1:") {
		t.Fatalf("default diagnostics_listen = %q, want loopback", cfg.HTTP.DiagnosticsListen)
	}
}

// alarmd and the comparator can run side by side with default configuration.
// Any two of their four default addresses colliding turns that into a hard
// startup failure, which is the regression the 6060/6061 choice fixed.
func TestDefaultAddressesOfBothBinariesDoNotCollide(t *testing.T) {
	addresses := map[string]string{
		"alarmd listen":         Default().HTTP.Listen,
		"alarmd diagnostics":    Default().HTTP.DiagnosticsListen,
		"comparator listen":     DefaultComparator().HTTP.Listen,
		"comparator diagnostic": DefaultComparator().HTTP.DiagnosticsListen,
	}
	for leftName, left := range addresses {
		for rightName, right := range addresses {
			if leftName >= rightName {
				continue
			}
			if listenAddressesCollide(left, right) {
				t.Fatalf("%s (%s) collides with %s (%s)", leftName, left, rightName, right)
			}
		}
	}
}

func TestDiagnosticsListenValidation(t *testing.T) {
	cases := []struct {
		name    string
		address string
		wantErr string
	}{
		{name: "empty serves no diagnostics", address: "", wantErr: ""},
		{name: "loopback accepted", address: "127.0.0.1:9001", wantErr: ""},
		{name: "missing port rejected", address: "127.0.0.1", wantErr: "diagnostics_listen"},
		{name: "empty host rejected", address: ":9001", wantErr: "empty host"},
		{name: "invalid port rejected", address: "127.0.0.1:0", wantErr: "invalid port"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := validConfigObject()
			cfg.HTTP.DiagnosticsListen = testCase.address
			err := cfg.Validate()
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("validate = %v, want error containing %q", err, testCase.wantErr)
			}
		})
	}
}

// Pointing both surfaces at one socket would undo the split, and the bind
// failure it causes at startup is a worse way to learn about it. Comparing the
// strings is not enough: the deployed query surface binds a wildcard, so the
// natural "same port, only loopback" edit collides without looking equal.
func TestDiagnosticsListenMustNotCollideWithQuerySurface(t *testing.T) {
	cases := []struct {
		name      string
		listen    string
		diagnosis string
		collides  bool
	}{
		{name: "identical", listen: "127.0.0.1:8080", diagnosis: "127.0.0.1:8080", collides: true},
		{name: "wildcard query surface, loopback diagnostics", listen: "0.0.0.0:8080", diagnosis: "127.0.0.1:8080", collides: true},
		{name: "wildcard diagnostics, loopback query surface", listen: "127.0.0.1:8080", diagnosis: "0.0.0.0:8080", collides: true},
		{name: "localhost spelled out", listen: "127.0.0.1:8080", diagnosis: "localhost:8080", collides: true},
		{name: "ipv6 wildcard on the same port", listen: "0.0.0.0:8080", diagnosis: "[::]:8080", collides: true},
		{name: "different port on the same host", listen: "0.0.0.0:8080", diagnosis: "0.0.0.0:6060", collides: false},
		{name: "different host and port", listen: "0.0.0.0:8080", diagnosis: "127.0.0.1:6060", collides: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := validConfigObject()
			cfg.HTTP.Listen = testCase.listen
			cfg.HTTP.DiagnosticsListen = testCase.diagnosis
			err := cfg.Validate()
			if testCase.collides {
				if err == nil || !strings.Contains(err.Error(), "must differ") {
					t.Fatalf("validate = %v, want an error about the two surfaces colliding", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate = %v, want nil", err)
			}
		})
	}
}
