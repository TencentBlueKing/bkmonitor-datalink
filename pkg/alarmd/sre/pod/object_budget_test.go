// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pod

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

func manyConfigMaps(n int) string {
	docs := make([]string, 0, n)
	for index := 0; index < n; index++ {
		docs = append(docs, fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm-%d}\ndata: {k: v}\n", index))
	}
	return strings.Join(docs, "---\n")
}

// A dry run whose time runs out stops between objects: the objects it
// checked keep their verdicts, the rest are not_attempted, the receipt names
// where to resume, and it is not clean.
func TestADryRunOutOfTimeKeepsWhatItCheckedAndSaysWhereToResume(t *testing.T) {
	runner := &fakeDryRunner{applied: func(_ string, object map[string]any) (*unstructured.Unstructured, error) {
		time.Sleep(50 * time.Millisecond) // fixed-wait: the dry run's per-object time, which the case sets
		return &unstructured.Unstructured{Object: object}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond) // fixed-wait: the operation's deadline the case runs out
	defer cancel()
	receipt, err := dryRunProvider(t, runner).DryRun(ctx, DryRunRequest{Namespace: "ns1", Manifests: manyConfigMaps(10)})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ResumeFrom == nil || *receipt.ResumeFrom < 1 || *receipt.ResumeFrom > 3 {
		t.Fatalf("resume from %v, want after the one to three objects that fit 180 ms", receipt.ResumeFrom)
	}
	resume := *receipt.ResumeFrom
	if len(receipt.Objects) != 10 || receipt.Counts[DryRunWouldCreate] != resume || receipt.Counts[DryRunNotAttempted] != 10-resume || receipt.Clean {
		t.Fatalf("receipt %+v", receipt.Counts)
	}
	for index, object := range receipt.Objects {
		if want := DryRunWouldCreate; index >= resume {
			want = DryRunNotAttempted
			if object.Verdict != want || object.Name != fmt.Sprintf("cm-%d", index) {
				t.Fatalf("object %d %+v, want %s", index, object, want)
			}
		} else if object.Verdict != want {
			t.Fatalf("object %d %+v, want %s", index, object, want)
		}
	}
}

// An apply whose time runs out sends nothing for the objects it did not
// reach, and says where to resume.
func TestAnApplyOutOfTimeSendsNothingPastWhereItStopped(t *testing.T) {
	runner := &fakeDryRunner{}
	sent := 0
	writer := &fakeWriter{applied: func(_ string, object map[string]any, _ bool) (*unstructured.Unstructured, error) {
		sent++
		time.Sleep(50 * time.Millisecond) // fixed-wait: the apply's per-object time, which the case sets
		return &unstructured.Unstructured{Object: object}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond) // fixed-wait: the operation's deadline the case runs out
	defer cancel()
	receipt, err := writeProvider(t, runner, writer).Apply(ctx, ApplyRequest{Namespace: "ns1", Manifests: manyConfigMaps(10)})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ResumeFrom == nil || sent != *receipt.ResumeFrom || receipt.Counts[WriteNotAttempted] != 10-sent || len(receipt.Objects) != 10 {
		t.Fatalf("sent %d, resume %v, counts %+v", sent, receipt.ResumeFrom, receipt.Counts)
	}
	ctx2, cancel2 := context.WithCancel(testwait.Context(t))
	defer cancel2()
	sent = 0
	if whole, err := writeProvider(t, runner, writer).Apply(ctx2, ApplyRequest{Namespace: "ns1", Manifests: manyConfigMaps(3)}); err != nil || whole.ResumeFrom != nil || sent != 3 {
		t.Fatalf("with room: resume %v sent %d err %v", whole.ResumeFrom, sent, err)
	}
}
