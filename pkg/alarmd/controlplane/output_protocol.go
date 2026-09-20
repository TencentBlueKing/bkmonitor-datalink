// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// The deployment's choice of protocol. These three words are the configuration
// surface; they are spelled here rather than imported from the configuration
// package so the control plane does not depend on it, and the configuration
// validates the same three.
const (
	outputProtocolAuto   = "auto"
	outputProtocolLegacy = "legacy"
	outputProtocolNative = "native"
)

// resolveWireFormat turns the deployment's choice into the format this Plan
// publishes, which is a different vocabulary on purpose: the choice says what a
// deployment wants, the format says what bytes are written, and "auto" is not a
// format.
//
// Auto is not a third behaviour either - it is the rule that was already in
// force, that a frozen revision is what makes a strategy publishable natively.
// Resolving it into the Plan means the Plan says which format it uses instead
// of leaving every reader to re-derive it from the revision.
//
// The second result is false when the choice cannot be honoured for this
// strategy, which happens only for a forced native choice without a revision.
func resolveWireFormat(configured string, snapshotRevision int64) (string, bool) {
	switch configured {
	case outputProtocolLegacy:
		return contract.WireFormatPythonCompatible, true
	case outputProtocolNative:
		if snapshotRevision == 0 {
			return "", false
		}
		return contract.WireFormatStandardRawEvent, true
	}
	if snapshotRevision == 0 {
		return contract.WireFormatPythonCompatible, true
	}
	return contract.WireFormatTriggerEvent, true
}
