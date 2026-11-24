// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package textregexp

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestUnicodeLookaroundAndCaptureOrder(t *testing.T) {
	re, err := Compile(`(?<=GUID\()(?<word>\w+)-(\d+)(?=\))`)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := re.FindAll("GUID(主机-１２)", 2)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%v err=%v", matches, err)
	}
	if matches[0].Text != "主机-１２" || matches[0].Groups[0] != "主机" || matches[0].Groups[1] != "１２" {
		t.Fatalf("unexpected extraction: %#v", matches[0])
	}
}

func TestOptionalAndEmptyCaptureAreDifferent(t *testing.T) {
	re, err := Compile(`(a)?(b*)c`)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := re.FindAll("c", 1)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%v err=%v", matches, err)
	}
	if matches[0].Groups[0] != nil || matches[0].Groups[1] != "" {
		t.Fatalf("missing/empty captures conflated: %#v", matches[0])
	}
}

func TestBackreferenceAndEmptyMatchProgress(t *testing.T) {
	re, err := Compile(`(\w+)-\1`)
	if err != nil {
		t.Fatal(err)
	}
	if matched, err := re.MatchString("主机-主机"); err != nil || !matched {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	empty, err := Compile(`a*`)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := empty.FindAll("bb", 10)
	if err != nil || len(matches) != 3 {
		t.Fatalf("empty matching did not advance: matches=%v err=%v", matches, err)
	}
}

func TestTimeoutDoesNotExposeInput(t *testing.T) {
	re, err := Compile(`(a+)+$`)
	if err != nil {
		t.Fatal(err)
	}
	input := "private-input-" + strings.Repeat("a", 2000) + "!"
	_, err = re.MatchString(input)
	if !errors.Is(err, ErrTimeout) || strings.Contains(err.Error(), "private-input") {
		t.Fatalf("expected redacted timeout, got %v", err)
	}
}

func TestCompiledExpressionCanBeSharedAcrossEvents(t *testing.T) {
	re, err := Compile(`(?<word>\w+)-\1`)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 20 {
				matches, err := re.FindAll("主机-主机 other-other", 3)
				if err != nil || len(matches) != 2 || matches[0].Groups[0] != "主机" {
					t.Errorf("unexpected concurrent extraction: %v %v", matches, err)
					return
				}
			}
		})
	}
	workers.Wait()
}
