// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The result contract expects one TriggerEvent per record with a business
// outcome. A record whose RECOVERY envelope the trigger held has RECOVERY
// outcomes and no envelope, and says so on each of them; that is the one
// envelope-less shape the contract accepts, and it accepts it only when the
// outcomes say so. On the reference deployment the first build with the
// gate refused every held record here, once per retry for two minutes per
// Slot, and the record's state, every Level of it, went unwritten.
func TestEvaluationAcceptsAHeldRecoveryRecordWithoutItsEnvelope(t *testing.T) {
	t.Run("held RECOVERY without an envelope is accepted", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeRecovery)
		result.Plans[0].LevelOutcomes[0].EnvelopeHeld = true
		result.Plans[0].StateResults[0].Events = nil
		if err := result.Validate(request); err != nil {
			t.Fatalf("a held RECOVERY record without an envelope was refused: %v", err)
		}
	})

	t.Run("RECOVERY without an envelope and without the hold is still refused", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeRecovery)
		result.Plans[0].StateResults[0].Events = nil
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "require exactly one TriggerEvent envelope") {
			t.Fatalf("a RECOVERY record with neither envelope nor hold must be refused at the envelope count, got %v", err)
		}
	})

	t.Run("a held record that still carries an envelope contradicts itself", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeRecovery)
		result.Plans[0].LevelOutcomes[0].EnvelopeHeld = true
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "TriggerEvent kind does not match Level outcomes") {
			t.Fatalf("an envelope on a held record must be refused as a kind mismatch, got %v", err)
		}
	})

	// A record whose envelope the protocol has no message for keeps its
	// identity in the envelope's place. It stands for the envelope exactly:
	// accepted alone, refused beside the envelope it replaced, and refused
	// when the protocol does have a message for it - that would be an event
	// the consumer never receives.
	withoutMessage := func(t *testing.T, format, kind string) (execution.EvaluationResult, execution.EvaluationRequest) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeRecovery)
		state := &result.Plans[0].StateResults[0]
		event := state.Events[0]
		state.WithoutMessage = []execution.EventWithoutMessage{{
			Record:    execution.RecordAnchor{RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime},
			EventKind: kind, Format: format,
		}}
		return result, request
	}
	t.Run("a RECOVERY its protocol has no message for is accepted as its identity", func(t *testing.T) {
		result, request := withoutMessage(t, contract.WireFormatPythonCompatible, contract.TriggerEventRecovery)
		result.Plans[0].StateResults[0].Events = nil
		if err := result.Validate(request); err != nil {
			t.Fatalf("a RECOVERY kept as its identity under the protocol that has no message for it was refused: %v", err)
		}
	})
	t.Run("the identity beside the envelope it replaced is a duplicate", func(t *testing.T) {
		result, request := withoutMessage(t, contract.WireFormatPythonCompatible, contract.TriggerEventRecovery)
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "duplicate TriggerEvent") {
			t.Fatalf("an identity kept beside its envelope must be refused as a duplicate, got %v", err)
		}
	})
	t.Run("an event the protocol has a message for cannot be kept as an identity", func(t *testing.T) {
		result, request := withoutMessage(t, contract.WireFormatStandardRawEvent, contract.TriggerEventRecovery)
		result.Plans[0].StateResults[0].Events = nil
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "its protocol has a message for was not kept") {
			t.Fatalf("a standard RECOVERY kept as an identity must be refused, got %v", err)
		}
	})

	t.Run("the hold is a property of RECOVERY outcomes only", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeAbnormal)
		result.Plans[0].LevelOutcomes[0].EnvelopeHeld = true
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "a held envelope is a property of RECOVERY outcomes only") {
			t.Fatalf("a hold on an ABNORMAL outcome must be refused, got %v", err)
		}
	})
}
