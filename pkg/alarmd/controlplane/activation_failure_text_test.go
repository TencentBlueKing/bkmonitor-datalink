// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"errors"
	"strings"
	"testing"
)

// Three unrelated paths land in this class, and only the wrapped error tells
// them apart. Unwrap served errors.Is and errors.As and nothing else: every log
// line renders the error as text, so on the wire all three were one sentence.
//
// Measured on bkop before this changed: 75 consecutive failures on one build,
// one distinct message. That reads as "the same deterministic thing happening
// over and over", which is a conclusion the data could not support -- the
// messages were identical because the type made them identical.
func TestTheDependencyThatFailedSurvivesBeingRenderedAsText(t *testing.T) {
	cause := errors.New("persist schedule cutover: redis: connection refused")

	text := activationDependencyIO(cause).Error()

	if !strings.Contains(text, "persist schedule cutover") {
		t.Fatalf("error text = %q, want it to name the dependency that failed", text)
	}
}

// Two different causes have to read differently. Without this the fix could be
// "append a constant" and still pass the test above.
func TestTwoDifferentDependencyFailuresDoNotReadTheSame(t *testing.T) {
	cutover := activationDependencyIO(errors.New("persist schedule cutover: i/o timeout")).Error()
	progress := activationDependencyIO(errors.New("load progress for drain check: i/o timeout")).Error()

	if cutover == progress {
		t.Fatalf("both failures render as %q, so counting distinct messages still cannot separate them", cutover)
	}
}

// Wrapping still has to work for errors.Is and errors.As, which is what the
// classifier uses. A fix that gained a readable message and lost the chain
// would move the blindness rather than remove it.
func TestNamingTheDependencyKeepsTheErrorChainIntact(t *testing.T) {
	cause := errors.New("redis: connection refused")

	wrapped := activationDependencyIO(cause)

	if !errors.Is(wrapped, cause) {
		t.Fatal("the wrapped cause is no longer reachable through errors.Is")
	}
	var typed *ActivationDependencyIOError
	if !errors.As(wrapped, &typed) {
		t.Fatal("the failure no longer classifies as a dependency I/O error")
	}
	if classifyActivationFailure(wrapped, ActivationFailureClassOther) != ActivationFailureClassDependencyIO {
		t.Fatal("the class changed along with the message")
	}
}

// A failure carrying no cause still has to render, and has to keep saying what
// it is rather than trailing an empty colon.
func TestAFailureWithNoCauseStillReadsAsADependencyFailure(t *testing.T) {
	text := (&ActivationDependencyIOError{}).Error()

	if text != "alarmd controlplane: activation dependency I/O failed" {
		t.Fatalf("error text = %q, want the bare sentence with nothing appended", text)
	}
}
