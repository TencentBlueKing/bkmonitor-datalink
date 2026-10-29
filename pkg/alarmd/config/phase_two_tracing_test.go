// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// The span export reads its section at load: an endpoint that is a host and
// nothing else, a ratio within 0..1 and a positive rate, each refused by its
// key otherwise; absent, the export is off at the defaults. Its token comes
// from token_file for a Worker and is never part of what the configuration
// shows.
func TestTheSpanExportIsReadFromItsSectionAndItsTokenStaysUnshown(t *testing.T) {
	load := func(leaf string, selected roles.Set) (Config, error) {
		return LoadWithRoles(writeConfig(t, platformSettingsConfigContents("", platformSettingsControlBase+leaf)), selected)
	}
	silent, err := load("", nil)
	if err != nil || silent.PhaseTwo.Tracing.OTLPEndpoint != "" || silent.TraceSampleRatio() != 0.1 || silent.TraceMaxTracesPerSecond() != 5 {
		t.Fatalf("no section: %+v, %v; want off at ratio 0.1 and 5 a second", silent.PhaseTwo.Tracing, err)
	}
	token := writeCredential(t, "trace-token", "token-for-test\n")
	stated, err := load("  tracing:\n    otlp_endpoint: http://collector:4318\n    token_file: "+token+
		"\n    sample_ratio: 0.5\n    max_traces_per_second: 2\n", roles.Set{roles.Worker})
	if err != nil || stated.PhaseTwo.Tracing.OTLPEndpoint != "http://collector:4318" || stated.PhaseTwo.Tracing.Token != "token-for-test" ||
		stated.TraceSampleRatio() != 0.5 || stated.TraceMaxTracesPerSecond() != 2 {
		t.Fatalf("stated: %+v, %v", stated.PhaseTwo.Tracing.OTLPEndpoint, err)
	}
	shown, err := json.Marshal(stated)
	if err != nil || strings.Contains(string(shown), "token-for-test") {
		t.Fatalf("the configuration as shown carries the token (%v)", err)
	}
	for leaf, key := range map[string]string{
		"    otlp_endpoint: collector:4318\n":                  "otlp_endpoint",
		"    otlp_endpoint: http://collector:4318/v1/traces\n": "otlp_endpoint",
		"    sample_ratio: 1.5\n":                              "sample_ratio",
		"    sample_ratio: -0.1\n":                             "sample_ratio",
		"    max_traces_per_second: 0\n":                       "max_traces_per_second",
	} {
		if _, err := load("  tracing:\n"+leaf, nil); err == nil || !strings.Contains(err.Error(), "phase_two.tracing."+key) {
			t.Fatalf("%q: error = %v, want a refusal naming phase_two.tracing.%s", leaf, err, key)
		}
	}
}
