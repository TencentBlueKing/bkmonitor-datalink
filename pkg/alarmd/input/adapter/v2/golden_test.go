// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAdapterReadsM0ExecutionEnvelopeGolden(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract", "testdata", "go-v2", "execution_envelope_v2.json"))
	if err != nil {
		t.Fatalf("ReadFile(M0 golden) error = %v", err)
	}
	result, err := New(readerLimits()).Decode(context.Background(), payload)
	if err != nil {
		t.Fatalf("Decode(M0 golden) error = %v", err)
	}
	if result.Rejected || result.Input == nil || result.Terminals.Len() != 0 {
		t.Fatalf("Decode(M0 golden) = %#v, want accepted input", result)
	}
	if result.Input.Execution().ExecutionID != "execution-1" || result.Input.RecordBatch().ValidLen() != 1 {
		t.Fatalf("golden input = (%q, %d), want (execution-1, 1)", result.Input.Execution().ExecutionID, result.Input.RecordBatch().ValidLen())
	}
}
