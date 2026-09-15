package kafka

import (
	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFiniteRecordOfficialBusinessAndNativeClassification(t *testing.T) {
	raw, err := os.ReadFile("../contract/testdata/business-kafka-v1/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := contract.DecodeBusinessAbnormalV1(raw, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	d := strings.Repeat("a", 64)
	ctx := contract.ShadowContextV1{ComparisonConfigDigest: d, PlanScheduleRevision: "plan", EvaluationTime: 180, SlotIdentity: "slot", SnapshotRevision: "snapshot", QueryRevision: "query", QueryGroupScheduleRevision: "schedule", DuePlanSetDigest: d, EffectiveTimeRequirementDigest: d, EffectiveTimeFactDigest: d}
	encoded, err := contract.EncodeGoBusinessAbnormalV1(contract.GoBusinessAbnormalV1{Schema: "go-business-abnormal-v1", RecordType: "BUSINESS_ABNORMAL", EpochID: "epoch", Context: ctx, Reference: *ref}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile("../contract/testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := os.ReadFile("../contract/testdata/go-coverage-v1/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		kind string
	}{{"business", encoded.CopyBytes(), FiniteBusiness}, {"coverage", coverage, FiniteReceipt}, {"native", native, FiniteNative}, {"unwrapped Python", raw, FiniteGap}, {"malformed", []byte("{"), FiniteGap}} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &sarama.ConsumerMessage{Topic: "fixture", Partition: 0, Offset: 7, Key: []byte("key"), Value: append([]byte(nil), tc.raw...), Timestamp: time.Unix(10, 0)}
			got := finiteRecord(msg, time.Unix(20, 0), MaxConsumerRecordBytes())
			if got.Kind != tc.kind || got.Offset != 7 || !got.BrokerTimestamp.Equal(msg.Timestamp) {
				t.Fatalf("kind=%s want=%s", got.Kind, tc.kind)
			}
			msg.Value[0] = '!'
			msg.Key[0] = '!'
			if got.Value[0] == '!' || got.Key[0] == '!' {
				t.Fatal("raw aliases Sarama buffer")
			}
		})
	}
}
