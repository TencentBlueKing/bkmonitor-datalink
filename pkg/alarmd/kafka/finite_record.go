package kafka

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	FiniteBusiness = "BUSINESS_ABNORMAL"
	FiniteReceipt  = "COVERAGE_RECEIPT"
	FiniteNative   = "NATIVE"
	FiniteRecovery = "GO_RECOVERY_SINGLE_CHAIN"
	FiniteGap      = "GAP"
)

// Raw bytes remain available even when the official decoder rejects the record.
// Every kind occupies its native offset, including Receipt and Recovery.
type FiniteRecord struct {
	Topic           string                         `json:"topic"`
	Partition       int32                          `json:"partition"`
	Offset          int64                          `json:"offset"`
	Key             []byte                         `json:"key_base64"`
	Value           []byte                         `json:"value_base64"`
	ValueSHA256     string                         `json:"value_sha256"`
	BrokerTimestamp time.Time                      `json:"broker_timestamp"`
	ObservedAt      time.Time                      `json:"observed_at"`
	Kind            string                         `json:"kind"`
	Reason          string                         `json:"reason,omitempty"`
	Business        *contract.GoBusinessAbnormalV1 `json:"-"`
	Shadow          *contract.ShadowResultRecordV1 `json:"-"`
}

func finiteRecord(message *sarama.ConsumerMessage, observed time.Time, limit int) FiniteRecord {
	sum := sha256.Sum256(message.Value)
	r := FiniteRecord{Topic: message.Topic, Partition: message.Partition, Offset: message.Offset,
		Key: append([]byte(nil), message.Key...), Value: append([]byte(nil), message.Value...), ValueSHA256: hex.EncodeToString(sum[:]),
		BrokerTimestamp: message.Timestamp, ObservedAt: observed, Kind: FiniteGap, Reason: "INVALID_OR_UNSUPPORTED_RECORD"}
	if business, err := contract.DecodeGoBusinessAbnormalV1(message.Value, limit); err == nil {
		r.Kind, r.Reason, r.Business = FiniteBusiness, "", business
		return r
	}
	record, err := contract.DecodeShadowResultRecordV1(message.Value, limit)
	if err != nil {
		return r
	}
	switch record.Kind {
	case contract.ShadowCoverage:
		if record.Receipt != nil && record.Receipt.Chain == contract.ShadowGo {
			r.Kind = FiniteReceipt
		}
	case contract.ShadowNativeEvent:
		r.Kind = FiniteNative
	case contract.ShadowFinalResult:
		if record.Evidence != nil && record.Evidence.Chain == contract.ShadowGo && record.Evidence.ResultKind == contract.TriggerEventRecovery {
			r.Kind = FiniteRecovery
		}
	}
	if r.Kind != FiniteGap {
		r.Reason = ""
		r.Shadow = &record
	}
	return r
}
