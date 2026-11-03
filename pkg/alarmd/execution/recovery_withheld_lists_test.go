// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The withheld-recovery reasons live in three lists: the contract's words,
// the execution codes the evaluator marks an outcome with, and the
// observability reasons the metrics pre-create and the outcome counter keeps
// on NORMAL. A code in one and not another loses its reason on the way:
// counted as a plain NORMAL, or never pre-created. The three are held to one
// another in both directions.
func TestTheWithheldRecoveryListsAreTheSameList(t *testing.T) {
	if len(execution.RecoveryWithheldCodes) != len(contract.RecoveryWithheldReasons) ||
		len(observability.RecoveryWithheldReasons) != len(contract.RecoveryWithheldReasons) {
		t.Fatalf("lists: %d codes, %d contract reasons, %d observability reasons, want one length",
			len(execution.RecoveryWithheldCodes), len(contract.RecoveryWithheldReasons), len(observability.RecoveryWithheldReasons))
	}
	for _, reason := range contract.RecoveryWithheldReasons {
		code := execution.RecoveryWithheldCodeOf(reason)
		if code == execution.RecoveryWithheldNone || code.Reason() != reason || !slices.Contains(execution.RecoveryWithheldCodes, code) {
			t.Errorf("contract reason %q: code %v reads back %q, listed %v", reason, code, code.Reason(),
				slices.Contains(execution.RecoveryWithheldCodes, code))
		}
		if !slices.Contains(observability.RecoveryWithheldReasons, observability.RecoveryWithheldReason(reason)) {
			t.Errorf("contract reason %q is not an observability reason", reason)
		}
	}
	for _, code := range execution.RecoveryWithheldCodes {
		if !slices.Contains(contract.RecoveryWithheldReasons, code.Reason()) {
			t.Errorf("code %v reads %q, not a contract reason", code, code.Reason())
		}
	}
	for _, reason := range observability.RecoveryWithheldReasons {
		if !slices.Contains(contract.RecoveryWithheldReasons, string(reason)) {
			t.Errorf("observability reason %q is not a contract reason", reason)
		}
	}
}
