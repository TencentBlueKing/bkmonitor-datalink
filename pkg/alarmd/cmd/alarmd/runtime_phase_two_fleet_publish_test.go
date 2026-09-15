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
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// Every field the replica snapshot carries is assigned where the snapshot is
// assembled.
//
// The assembly is a run of hand-written assignments, and this page's most
// frequent defect by a wide margin is a value that exists, is used where it is
// produced, and is dropped on one of the handoffs out. A field left unassigned
// here reaches the deployment as its zero value, and for most of them the zero
// value is the reassuring reading: no spans lost, no demotions, nothing
// overdue.
//
// Read off the source, which is the weaker kind of check and is used because
// the stronger kind is not available: the assembly needs a whole publisher with
// a tracker, a capacity source and a dispatch source behind it, and a test that
// builds all of those proves mostly that the test can build them. This catches
// the realistic failure -- a field added to the snapshot and never assigned, or
// an assignment deleted -- and it does not catch an assignment that reads the
// wrong source. Where that mattered the translation was given a name and a
// check of its own instead; see historyCoverageFacts and rotationView.
func TestEverySnapshotFieldIsAssignedWhereTheSnapshotIsBuilt(t *testing.T) {
	source, err := os.ReadFile("runtime_phase_two_fleet.go")
	if err != nil {
		t.Fatal(err)
	}
	assembled := string(source)
	// Fields the replica does not fill in: they are set by the aggregate or by
	// the reader, not by the replica publishing itself.
	notPublishedHere := map[string]string{
		"Replica":    "the publisher keys the snapshot by it rather than storing it in the body",
		"ObservedAt": "stamped by the store on write, so a replica cannot backdate itself",
	}
	snapshot := reflect.TypeOf(fleet.Snapshot{})
	for index := 0; index < snapshot.NumField(); index++ {
		name := snapshot.Field(index).Name
		if _, skipped := notPublishedHere[name]; skipped {
			continue
		}
		if strings.Contains(assembled, "snapshot."+name+" =") ||
			strings.Contains(assembled, "snapshot."+name+",") ||
			strings.Contains(assembled, name+":") {
			continue
		}
		t.Errorf("fleet.Snapshot.%s is never assigned in the assembly, so it reaches the deployment "+
			"as its zero value -- which for most of these fields is the reading that says nothing "+
			"is wrong", name)
	}
}
