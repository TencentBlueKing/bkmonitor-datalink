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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestComparatorKafkaEndToEnd(t *testing.T) {
	broker := os.Getenv("ALARM_ENGINE_KAFKA_E2E_BROKER")
	if broker == "" {
		t.Skip("ALARM_ENGINE_KAFKA_E2E_BROKER is not set")
	}

	version := sarama.V2_1_0_0
	adminConfig := sarama.NewConfig()
	adminConfig.Version = version
	admin, err := sarama.NewClusterAdmin([]string{broker}, adminConfig)
	if err != nil {
		t.Fatalf("NewClusterAdmin() error = %v", err)
	}
	defer admin.Close()
	prefix := fmt.Sprintf("alarm-engine-e2e-%d", time.Now().UnixNano())
	topics := []string{prefix + "-input", prefix + "-go", prefix + "-python"}
	auditTopic := prefix + "-audit"
	for _, topic := range append(append([]string(nil), topics...), auditTopic) {
		if err := admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); err != nil {
			t.Fatalf("CreateTopic(%q) error = %v", topic, err)
		}
		defer admin.DeleteTopic(topic)
	}

	sink, err := OpenComparisonAuditSink(ComparisonAuditSinkConfig{
		Brokers: []string{broker}, InputTopics: topics, OutputTopic: auditTopic,
		AllowedOutputTopics: []string{auditTopic}, ClientID: prefix + "-audit", BrokerVersion: "2.1.0",
	})
	if err != nil {
		t.Fatalf("OpenComparisonAuditSink() error = %v", err)
	}
	service, err := OpenComparatorService(ComparatorServiceConfig{
		Brokers: []string{broker}, TriggerInputTopic: topics[0], GoDecisionTopic: topics[1], PythonDecisionTopic: topics[2],
		GroupID: prefix + "-group", ClientID: prefix + "-consumer", BrokerVersion: "2.1.0",
		MaxEntries: 100, CoverageTimeout: 200 * time.Millisecond, BarrierInterval: 50 * time.Millisecond,
	}, sink, 5*time.Second)
	if err != nil {
		t.Fatalf("OpenComparatorService() error = %v", err)
	}
	serviceContext, cancelService := context.WithCancel(context.Background())
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- service.Run(serviceContext) }()
	deadline := time.Now().Add(10 * time.Second)
	for !service.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !service.Ready() {
		cancelService()
		t.Fatalf("comparator service did not become ready: %v", <-serviceDone)
	}

	producerConfig := sarama.NewConfig()
	producerConfig.Version = version
	producerConfig.Producer.RequiredAcks = sarama.WaitForAll
	producerConfig.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer([]string{broker}, producerConfig)
	if err != nil {
		t.Fatalf("NewSyncProducer() error = %v", err)
	}
	inputPayload, inputKey := comparatorTriggerInputFixture(t, "normal")
	input, err := contract.DecodeTriggerInput(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	decisionPayload, decisionKey := comparatorTriggerDecisionFixture(t, input)
	missingPayload, missingKey := comparatorTriggerInputFixture(t, "anomalous")
	missingInput, err := contract.DecodeTriggerInput(missingPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput(missing) error = %v", err)
	}
	for _, message := range []*sarama.ProducerMessage{
		{Topic: topics[2], Key: sarama.ByteEncoder(decisionKey), Value: sarama.ByteEncoder(decisionPayload)},
		{Topic: topics[0], Key: sarama.ByteEncoder(inputKey), Value: sarama.ByteEncoder(inputPayload)},
		{Topic: topics[1], Key: sarama.ByteEncoder(decisionKey), Value: sarama.ByteEncoder(decisionPayload)},
		{Topic: topics[0], Key: sarama.ByteEncoder(missingKey), Value: sarama.ByteEncoder(missingPayload)},
	} {
		if _, _, err := producer.SendMessage(message); err != nil {
			t.Fatalf("SendMessage(%q) error = %v", message.Topic, err)
		}
	}
	consumer, err := sarama.NewConsumer([]string{broker}, adminConfig)
	if err != nil {
		t.Fatalf("NewConsumer() error = %v", err)
	}
	defer consumer.Close()
	partition, err := consumer.ConsumePartition(auditTopic, 0, sarama.OffsetOldest)
	if err != nil {
		t.Fatalf("ConsumePartition() error = %v", err)
	}
	defer partition.Close()
	matched, missingAtBarrier := false, false
	readDeadline := time.After(10 * time.Second)
	for !matched || !missingAtBarrier {
		select {
		case message := <-partition.Messages():
			if len(message.Value) > contract.MaxComparisonAuditBytesV1 {
				t.Fatalf("audit bytes = %d, want at most %d", len(message.Value), contract.MaxComparisonAuditBytesV1)
			}
			batch, err := contract.DecodeComparisonAuditBatch(message.Value)
			if err != nil {
				t.Fatalf("DecodeComparisonAuditBatch() error = %v", err)
			}
			for _, audit := range batch.Audits {
				if audit.InputID == input.DetectionOutcomes[0].InputID && audit.JoinStatus == contract.ComparisonJoinComplete && audit.Verdict == contract.ComparisonVerdictMatch {
					matched = true
				}
				if audit.InputID == missingInput.DetectionOutcomes[0].InputID && audit.Coverage.Phase == contract.ComparisonCoverageMissingAtBarrier && audit.Verdict == contract.ComparisonVerdictNone {
					missingAtBarrier = true
				}
			}
		case err := <-partition.Errors():
			t.Fatalf("audit consumer error = %v", err)
		case <-readDeadline:
			t.Fatal("timed out waiting for MATCH and MISSING_AT_BARRIER audits")
		}
	}

	const timestampCount = 60000
	largeDecisionPayload, largeDecisionKey := comparatorTriggerDecisionFixture(t, missingInput)
	largeDecisionBatch, err := contract.DecodeTriggerDecisionBatch(largeDecisionPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerDecisionBatch(large) error = %v", err)
	}
	timestamps := make([]int64, timestampCount)
	for index := 0; index < len(timestamps)-1; index++ {
		timestamps[index] = int64(index)
	}
	timestamps[len(timestamps)-1] = missingInput.DetectionOutcomes[0].Record.SourceTime
	largeDecisionBatch.Decisions[0].AnomalyTimestamps = timestamps
	largeDecisionPayload, err = contract.EncodeTriggerDecisionBatch(largeDecisionBatch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch(large) error = %v", err)
	}
	for _, topic := range []string{topics[2], topics[1]} {
		if _, _, err := producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic, Key: sarama.ByteEncoder(largeDecisionKey), Value: sarama.ByteEncoder(largeDecisionPayload),
		}); err != nil {
			t.Fatalf("SendMessage(large %q) error = %v", topic, err)
		}
	}
	if err := producer.Close(); err != nil {
		t.Fatalf("producer.Close() error = %v", err)
	}

	largeAudit := false
	readDeadline = time.After(10 * time.Second)
	for !largeAudit {
		select {
		case message := <-partition.Messages():
			if len(message.Value) > contract.MaxComparisonAuditBytesV1 {
				t.Fatalf("large audit bytes = %d, want at most %d", len(message.Value), contract.MaxComparisonAuditBytesV1)
			}
			batch, err := contract.DecodeComparisonAuditBatch(message.Value)
			if err != nil {
				t.Fatalf("DecodeComparisonAuditBatch(large) error = %v", err)
			}
			for _, audit := range batch.Audits {
				if audit.InputID == missingInput.DetectionOutcomes[0].InputID && audit.GoInvalid && audit.PythonInvalid &&
					audit.GoDecision != nil && audit.PythonDecision != nil &&
					audit.GoDecision.Decision.AnomalyTimestampsCount == timestampCount &&
					audit.PythonDecision.Decision.AnomalyTimestampsCount == timestampCount &&
					audit.Verdict == contract.ComparisonVerdictNone {
					largeAudit = true
				}
			}
		case err := <-partition.Errors():
			t.Fatalf("large audit consumer error = %v", err)
		case <-readDeadline:
			t.Fatal("timed out waiting for compact large-decision audit")
		}
	}

	cancelService()
	if err := <-serviceDone; err != nil {
		t.Fatalf("Service.Run() shutdown error = %v", err)
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := sink.Shutdown(shutdownContext); err != nil {
		t.Fatalf("sink.Shutdown() error = %v", err)
	}
}
