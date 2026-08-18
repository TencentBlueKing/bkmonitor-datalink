// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestComparatorHandlerJoinsThreeClaimsIntoAudit(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment, err := newComparatorAssignmentCoordinator(
		metadata,
		map[comparator.StreamRole]string{
			comparator.StreamInput: "trigger-input", comparator.StreamGo: "go-decision", comparator.StreamPython: "py-decision",
		},
		100,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("newComparatorAssignmentCoordinator() error = %v", err)
	}
	audits := &collectingComparisonAuditSink{}
	events := []string{}
	handler, err := newComparatorHandler(assignment, fakeSyncOffsetCommitter{events: &events}, audits, time.Hour, nil)
	if err != nil {
		t.Fatalf("newComparatorHandler() error = %v", err)
	}
	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	session := newFakeSession(sessionContext, &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	if err := handler.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	inputPayload, inputKey := comparatorTriggerInputFixture(t, "normal")
	input, err := contract.DecodeTriggerInput(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	decisionPayload, decisionKey := comparatorTriggerDecisionFixture(t, input)
	claims := []*fakeClaim{
		newFakeClaim("py-decision", 0, []*sarama.ConsumerMessage{{Topic: "py-decision", Partition: 0, Offset: 0, Key: decisionKey, Value: decisionPayload}}),
		newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{Topic: "trigger-input", Partition: 0, Offset: 0, Key: inputKey, Value: inputPayload}}),
		newFakeClaim("go-decision", 0, []*sarama.ConsumerMessage{{Topic: "go-decision", Partition: 0, Offset: 0, Key: decisionKey, Value: decisionPayload}}),
	}
	var wait sync.WaitGroup
	errorsByClaim := make(chan error, len(claims))
	for _, claim := range claims {
		claim := claim
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByClaim <- handler.ConsumeClaim(session, claim)
		}()
	}
	wait.Wait()
	close(errorsByClaim)
	for err := range errorsByClaim {
		if err != nil {
			t.Fatalf("ConsumeClaim() error = %v", err)
		}
	}
	if !handler.Ready() {
		t.Fatal("handler never became ready after all claims registered")
	}
	foundMatch := false
	for _, batch := range audits.Batches() {
		for _, audit := range batch.Audits {
			if audit.InputID == input.DetectionOutcomes[0].InputID && audit.JoinStatus == contract.ComparisonJoinComplete &&
				audit.Verdict == contract.ComparisonVerdictMatch {
				foundMatch = true
			}
		}
	}
	if !foundMatch {
		t.Fatalf("audit batches = %#v, want a complete match", audits.Batches())
	}
	cancelSession()
	if err := handler.Cleanup(session); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

type collectingComparisonAuditSink struct {
	mu      sync.Mutex
	batches []*contract.ComparisonAuditBatch
}

func (s *collectingComparisonAuditSink) WriteBatch(_ context.Context, batch *contract.ComparisonAuditBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, batch)
	return nil
}

func (s *collectingComparisonAuditSink) Batches() []*contract.ComparisonAuditBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*contract.ComparisonAuditBatch(nil), s.batches...)
}
