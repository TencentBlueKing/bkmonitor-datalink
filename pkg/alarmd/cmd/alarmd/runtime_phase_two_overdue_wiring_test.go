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
	"strings"
	"testing"
)

// The seam tests next door prove publisherOverdue works when it is given a
// source. They do not prove the production publisher is given one -- cutting
// either wire leaves every one of them green, and the page then quietly reports
// "未启用" on a deployment whose index is running, or lists parked objects as
// bare hashes nobody can trace to a strategy.
//
// Both fields are optional by type, which is what makes this worth pinning:
// nothing in the compiler notices their absence. The production assembly needs
// Redis and a control plane to stand up, so the assembly itself is read.
func TestTheProductionPublisherIsGivenItsOverdueSourceAndStrategyNames(t *testing.T) {
	raw, err := os.ReadFile("runtime_phase_two_bundle.go")
	if err != nil {
		t.Fatalf("read the assembly: %v", err)
	}
	source := string(raw)
	start := strings.Index(source, "publisher = fleetPublisher{")
	if start < 0 {
		t.Fatal("the production publisher assembly moved; this guard has to move with it")
	}
	end := strings.Index(source[start:], "\n\t}")
	if end < 0 {
		t.Fatal("could not find the end of the publisher literal")
	}
	literal := source[start : start+end]

	for _, wire := range []struct{ field, why string }{
		{"overdue:", "the due index, without which the page reports 未启用 on a running index"},
		{"strategies:", "strategy names, without which parked objects arrive as bare hashes"},
	} {
		if !strings.Contains(literal, wire.field) {
			t.Errorf("the production publisher is not given %s -- %s", wire.field, wire.why)
		}
	}
}
