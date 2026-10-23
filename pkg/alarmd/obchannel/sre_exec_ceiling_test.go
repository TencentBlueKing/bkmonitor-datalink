// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// The longest pod.exec fits its operation: the checks before and after, the
// start, the command and the termination grace add up to the operation's
// ceiling, so a command run to its limit still gets its termination record
// and final check.
func TestTheLongestExecFitsItsOperation(t *testing.T) {
	if pod.ExecOperationCeiling != MaxExecutionTimeout {
		t.Fatalf("pod's ceiling %v, the channel's %v", pod.ExecOperationCeiling, MaxExecutionTimeout)
	}
	total := 2*pod.VerificationTimeout + pod.StartupAllowance + pod.MaxExecTimeout + pod.TerminationGrace
	if total > MaxExecutionTimeout {
		t.Fatalf("the parts add up to %v, past the %v operation", total, MaxExecutionTimeout)
	}
	for _, op := range SREOperations(SREOptions{}) {
		if op.ID == "pod.exec" && op.ExecutionTimeout != MaxExecutionTimeout {
			t.Fatalf("pod.exec runs under %v, want the ceiling %v", op.ExecutionTimeout, MaxExecutionTimeout)
		}
	}
}

// k8s.apply and k8s.dry_run take a whole chart's manifests: their budget is
// the channel's ceiling, and a run that cannot finish stops between objects
// and says where to resume.
func TestManifestOperationsTakeTheCeiling(t *testing.T) {
	for _, op := range SREOperations(SREOptions{}) {
		if (op.ID == pod.OpApply || op.ID == pod.OpDryRun) && op.ExecutionTimeout != MaxExecutionTimeout {
			t.Errorf("%s runs under %v, want the ceiling %v", op.ID, op.ExecutionTimeout, MaxExecutionTimeout)
		}
	}
}
