// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The choice is resolved once, per strategy, when its Plan is built. The table
// is the whole contract: what a deployment asked for, what the strategy is, and
// what bytes come out.
func TestTheDeploymentChoiceResolvesToOneFormatPerStrategy(t *testing.T) {
	for name, test := range map[string]struct {
		configured string
		revision   int64
		want       string
		honoured   bool
	}{
		"unset keeps what the revision already decided": {
			configured: "", revision: 7, want: contract.WireFormatTriggerEvent, honoured: true,
		},
		"unset and no revision is the compatibility protocol": {
			configured: "", revision: 0, want: contract.WireFormatPythonCompatible, honoured: true,
		},
		"auto is the same rule, named": {
			configured: outputProtocolAuto, revision: 7, want: contract.WireFormatTriggerEvent, honoured: true,
		},
		"legacy forces compatibility even with a revision": {
			configured: outputProtocolLegacy, revision: 7, want: contract.WireFormatPythonCompatible, honoured: true,
		},
		"native publishes the standard raw event": {
			configured: outputProtocolNative, revision: 7, want: contract.WireFormatStandardRawEvent, honoured: true,
		},
		"native cannot be honoured without a revision": {
			configured: outputProtocolNative, revision: 0, honoured: false,
		},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			format, honoured := resolveWireFormat(test.configured, test.revision)
			if honoured != test.honoured {
				t.Fatalf("honoured = %t, want %t", honoured, test.honoured)
			}
			if honoured && format != test.want {
				t.Fatalf("format = %q, want %q", format, test.want)
			}
		})
	}
}

// The one case that must never be a silent fallback: a forced native choice
// meeting a strategy with no revision has to refuse the strategy, because
// publishing it the other way is exactly what the choice was made to stop.
func TestAForcedNativeChoiceRefusesRatherThanFallsBack(t *testing.T) {
	format, honoured := resolveWireFormat(outputProtocolNative, 0)
	if honoured {
		t.Fatalf("format = %q, want the strategy refused", format)
	}
	if format == contract.WireFormatPythonCompatible {
		t.Fatal("a forced native choice must not fall back to the compatibility protocol")
	}
}
