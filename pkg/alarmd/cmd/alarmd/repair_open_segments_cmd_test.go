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
	"bytes"
	"context"
	"strings"
	"testing"
)

// Applying without somewhere to write what it replaced is refused before
// anything is opened.
//
// This command rewrites live execution state by hand. The refusal is at the
// flags rather than inside the repair so that it costs no connection and no
// read: an operator who forgot the directory finds out immediately.
func TestTheRepairCommandRefusesToApplyWithoutEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRepairOpenSegments(context.Background(),
		[]string{"--config", "/nonexistent.yaml", "--apply"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 for a usage error", code)
	}
	if !strings.Contains(stderr.String(), "--evidence-dir") {
		t.Fatalf("stderr = %q, want it to name the missing flag", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing: the run never started", stdout.String())
	}
}

// The command needs a configuration, and takes no connection flags.
//
// It is run by exec into a pod with that pod's own configuration; credentials
// on a command line end up in a shell history and a process list.
func TestTheRepairCommandRequiresAConfigurationAndTakesNoCredentials(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runRepairOpenSegments(context.Background(), nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2 without --config", code)
	}
	if !strings.Contains(stderr.String(), "--config") {
		t.Fatalf("stderr = %q, want it to name the missing flag", stderr.String())
	}

	// And no flag offers to take a password or an address.
	var usage bytes.Buffer
	runRepairOpenSegments(context.Background(), []string{"--help"}, &stdout, &usage)
	for _, forbidden := range []string{"password", "address", "-addr", "user"} {
		if strings.Contains(strings.ToLower(usage.String()), forbidden) {
			t.Fatalf("the repair command offers a %q flag; it must take its connection from the "+
				"configuration the pod already reads", forbidden)
		}
	}
}
