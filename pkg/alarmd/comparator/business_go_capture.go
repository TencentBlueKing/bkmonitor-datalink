package comparator

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// This matches the finite reader's lossless archive row, not a second broker connector.
type goCaptureRecord struct {
	Topic           string    `json:"topic"`
	Partition       int32     `json:"partition"`
	Offset          int64     `json:"offset"`
	Key             []byte    `json:"key_base64"`
	Value           []byte    `json:"value_base64"`
	ValueSHA256     string    `json:"value_sha256"`
	BrokerTimestamp time.Time `json:"broker_timestamp"`
	ObservedAt      time.Time `json:"observed_at"`
	Kind            string    `json:"kind"`
	Reason          string    `json:"reason,omitempty"`
}

type GoCaptureCounts struct {
	Seen           uint64 `json:"seen"`
	TargetResult   uint64 `json:"target_result"`
	TargetCoverage uint64 `json:"target_coverage"`
	OutOfScope     uint64 `json:"out_of_scope"`
	Native         uint64 `json:"native"`
	Recovery       uint64 `json:"recovery"`
	Invalid        uint64 `json:"invalid"`
}

func (r *BusinessRun) captureCountsCopy() *GoCaptureCounts {
	if r.goCaptureCounts == nil {
		return nil
	}
	copy := *r.goCaptureCounts
	return &copy
}
func (r *BusinessRun) outsideGoTarget(tenant, business, strategy string) bool {
	return r.target != nil && (tenant != r.target.TenantID || business != r.target.BusinessID || !r.pythonStrategies[strategy])
}
func (r *BusinessRun) observeGoCapture(at time.Time, wire []byte) (err error) {
	if r.goCaptureCounts == nil {
		r.goCaptureCounts = &GoCaptureCounts{}
	}
	counts := r.goCaptureCounts
	counts.Seen++
	classified := false
	defer func() {
		if !classified {
			counts.Invalid++
		}
	}()
	var row goCaptureRecord
	if err = decodeBusinessLine(wire, &row); err != nil {
		return err
	}
	hash := sha256.Sum256(row.Value)
	if hex.EncodeToString(hash[:]) != row.ValueSHA256 || len(row.Value) > r.limits.MessageBytes {
		return errors.New("Go original bytes/hash bound")
	}
	offset := BusinessOffset{BusinessPartition{contract.ShadowGo, row.Topic, row.Partition}, row.Offset, row.ValueSHA256}
	if e, decodeErr := contract.DecodeGoCoverageRecord(row.Value, r.limits.MessageBytes); decodeErr == nil {
		if e.EpochID != r.epoch {
			return errors.New("Go coverage Epoch mismatch")
		}
		if r.outsideGoTarget(e.Receipt.TenantID, e.Receipt.BusinessID, e.Receipt.StrategyID) {
			err = r.SkipNative(offset)
			if err == nil {
				counts.OutOfScope++
				classified = true
			}
			return err
		}
		err = r.ObserveGoCoverage(at, offset, row.Value)
		if err == nil {
			r.setScopeAuditFirst(e.Identity, row.ObservedAt)
			counts.TargetCoverage++
			classified = true
		}
		return err
	}
	if e, decodeErr := contract.DecodeGoBusinessAbnormalV1(row.Value, r.limits.MessageBytes); decodeErr == nil {
		if e.EpochID != r.epoch {
			return errors.New("Go business Epoch mismatch")
		}
		if r.outsideGoTarget(e.Reference.Subject.TenantID, e.Reference.Subject.BusinessID, e.Reference.Subject.StrategyID) {
			err = r.SkipNative(offset)
			if err == nil {
				counts.OutOfScope++
				classified = true
			}
			return err
		}
		err = r.Observe(at, offset, row.Value)
		if err == nil {
			r.setEntryAuditFirst(e.Reference.Subject, row.ObservedAt)
			counts.TargetResult++
			classified = true
		}
		return err
	}
	native, decodeErr := contract.DecodeShadowResultRecordV1(row.Value, r.limits.MessageBytes)
	knownRecovery := native.Kind == contract.ShadowFinalResult && native.Evidence != nil && native.Evidence.Chain == contract.ShadowGo && native.Evidence.EpochID == r.epoch && native.Evidence.ResultKind == contract.TriggerEventRecovery
	if decodeErr != nil || (native.Kind != contract.ShadowNativeEvent && !knownRecovery) {
		return errors.New("Go capture unsupported frame")
	}
	err = r.SkipNative(offset)
	if err == nil {
		classified = true
		if knownRecovery {
			counts.Recovery++
		} else {
			counts.Native++
		}
	}
	return err
}

type goCaptureRange struct {
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
}

func validateGoCaptureHeader(wire []byte, header BusinessCaptureHeader) error {
	var request struct {
		Schema        string           `json:"schema"`
		Ranges        []goCaptureRange `json:"ranges"`
		MaxRecords    int64            `json:"max_records"`
		MaxBytes      int64            `json:"max_bytes"`
		MaxPartitions int              `json:"max_partitions"`
		TimeoutMillis int64            `json:"timeout_millis"`
	}
	if err := decodeBusinessLine(wire, &request); err != nil {
		return err
	}
	if request.MaxRecords <= 0 || request.MaxBytes <= 0 || request.MaxPartitions < len(request.Ranges) || request.TimeoutMillis <= 0 {
		return errors.New("Go capture invalid bounds")
	}
	seen := map[BusinessPartition]bool{}
	for _, p := range request.Ranges {
		key := BusinessPartition{contract.ShadowGo, p.Topic, p.Partition}
		found := false
		for _, want := range header.Ranges {
			if want.BusinessPartition == key && want.Start == p.Start && want.End == p.End {
				found = true
			}
		}
		if !found || seen[key] {
			return errors.New("Go capture frozen range mismatch")
		}
		seen[key] = true
	}
	for _, p := range header.Ranges {
		if p.Chain == contract.ShadowGo && !seen[p.BusinessPartition] {
			return errors.New("Go partition absent")
		}
	}
	return nil
}
func (r *BusinessRun) verifyGoCaptureSummary(wire []byte) error {
	var summary struct {
		Schema  string `json:"schema"`
		Summary struct {
			StartedAt         time.Time `json:"started_at"`
			FinishedAt        time.Time `json:"finished_at"`
			Complete          bool      `json:"complete"`
			ReferenceComplete bool      `json:"reference_complete"`
			Reason            string    `json:"reason,omitempty"`
			Records           int64     `json:"records"`
			Bytes             int64     `json:"bytes"`
			Gaps              int64     `json:"gaps"`
			Partitions        []struct {
				goCaptureRange
				Low       int64 `json:"low"`
				High      int64 `json:"high"`
				FinalLow  int64 `json:"final_low"`
				FinalHigh int64 `json:"final_high"`
				ReadEnd   int64 `json:"read_end"`
			} `json:"partitions"`
		} `json:"summary"`
	}
	if err := decodeBusinessLine(wire, &summary); err != nil {
		return err
	}
	s := summary.Summary
	if !s.Complete || !s.ReferenceComplete || s.Gaps != 0 || s.Reason != "" || s.StartedAt.IsZero() || s.FinishedAt.Before(s.StartedAt) {
		return errors.New("Go capture incomplete")
	}
	seen := map[BusinessPartition]bool{}
	var count int64
	for _, fact := range s.Partitions {
		key := BusinessPartition{contract.ShadowGo, fact.Topic, fact.Partition}
		p := r.partitions[key]
		if p == nil || seen[key] || fact.Start != p.start || fact.End != p.end || fact.ReadEnd != p.end || p.read != p.end || fact.Low < 0 || fact.Low > p.start || fact.FinalLow < 0 || fact.FinalLow > p.start || fact.High < p.end || fact.FinalHigh < p.end {
			return errors.New("Go range or retention unclosed")
		}
		seen[key] = true
		count += p.end - p.start
	}
	for key := range r.partitions {
		if key.Chain == contract.ShadowGo && !seen[key] {
			return errors.New("Go summary partition absent")
		}
	}
	if r.goCaptureCounts != nil && (r.goCaptureCounts.Seen != uint64(count) || r.goCaptureCounts.Seen != r.goCaptureCounts.TargetResult+r.goCaptureCounts.TargetCoverage+r.goCaptureCounts.OutOfScope+r.goCaptureCounts.Native+r.goCaptureCounts.Recovery+r.goCaptureCounts.Invalid || r.goCaptureCounts.Invalid != 0) {
		return errors.New("Go classification conservation")
	}
	if count != s.Records {
		return errors.New("Go capture count mismatch")
	}
	return nil
}

func (r *BusinessRun) setScopeAuditFirst(id string, observed time.Time) {
	scope := r.scopes[id]
	if scope == nil {
		return
	}
	value := int64(0)
	if !observed.IsZero() && observed.UnixMilli() > 0 {
		value = observed.UnixMilli()
	}
	if !scope.auditFirstSet {
		scope.auditFirst = value
		scope.auditFirstSet = true
		return
	}
	if value == 0 || (scope.auditFirst != 0 && value < scope.auditFirst) {
		scope.auditFirst = value
	}
}

func (r *BusinessRun) setEntryAuditFirst(subject contract.ShadowSubjectV1, observed time.Time) {
	entry := r.entries[businessKey(subject)]
	if entry == nil {
		return
	}
	value := int64(0)
	if !observed.IsZero() && entry.python == nil {
		value = observed.UnixMilli()
	}
	if entry.auditFirst == nil {
		entry.auditFirst = &value
		return
	}
	if value == 0 || (*entry.auditFirst != 0 && value < *entry.auditFirst) {
		entry.auditFirst = &value
	}
}
