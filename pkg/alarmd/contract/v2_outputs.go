// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"unicode/utf8"
)

func BuildTriggerEventV1(input TriggerEventBuildInputV1) (*TriggerEventV1, error) {
	if input.MaxEvidenceBytes <= 0 {
		return nil, invalid("trigger_event.max_evidence_bytes", "must be positive")
	}
	results := append([]LevelResultV1(nil), input.LevelResults...)
	sort.Slice(results, func(left, right int) bool { return results[left].LevelID < results[right].LevelID })
	if err := validateSuccessfulLevelResultsV1(results); err != nil {
		return nil, err
	}
	evidence, err := CanonicalJSONV2(results)
	if err != nil {
		return nil, err
	}
	if len(evidence) > input.MaxEvidenceBytes {
		return nil, invalid("trigger_event.level_results", "evidence exceeds admitted byte limit")
	}
	computedKind, primary, err := aggregateTriggerEventV1(results)
	if err != nil {
		return nil, err
	}
	if input.EventKind != computedKind {
		return nil, invalid("trigger_event.event_kind", "does not match successful active Level results")
	}
	semanticDigest, err := DeriveEventSemanticDigestV1(
		input.EventKind, results, input.DetectPlanFingerprint, input.TriggerStateFingerprint,
	)
	if err != nil {
		return nil, err
	}
	eventID, err := DeriveTriggerEventIDV1(
		input.TenantID, input.BusinessID, input.PlanRef.StrategyID, input.PlanRef.StateCompatibilityHash,
		input.RecordRef.RecordID, input.EventKind, semanticDigest,
	)
	if err != nil {
		return nil, err
	}
	event := &TriggerEventV1{
		Schema: Schema{Name: TriggerEventSchemaV1, Major: 1, Minor: 0}, RequiredFeatures: []string{},
		EventID: eventID, EventSemanticDigest: semanticDigest, EventKind: input.EventKind, PrimaryLevelID: primary,
		TenantID: input.TenantID, BusinessID: input.BusinessID, PlanRef: input.PlanRef, RecordRef: input.RecordRef,
		Observed: input.Observed, LevelResults: results, EvaluationTime: input.EvaluationTime,
		DetectPlanFingerprint: input.DetectPlanFingerprint, TriggerStateFingerprint: input.TriggerStateFingerprint,
		Trace: TriggerEventTraceV1{ExecutionID: input.ExecutionID},
	}
	if err := ValidateTriggerEventV1(event); err != nil {
		return nil, err
	}
	return event, nil
}

func DeriveEventSemanticDigestV1(eventKind string, results []LevelResultV1, detectPlanFingerprint, triggerStateFingerprint string) (string, error) {
	if eventKind != TriggerEventAbnormal && eventKind != TriggerEventRecovery {
		return "", invalid("trigger_event.event_kind", "unsupported value")
	}
	if !sha256Pattern.MatchString(detectPlanFingerprint) || !sha256Pattern.MatchString(triggerStateFingerprint) {
		return "", invalid("trigger_event", "Plan fingerprints must be 64 lowercase hexadecimal characters")
	}
	type semanticLevel struct {
		LevelID                 uint32 `json:"level_id"`
		Priority                uint32 `json:"priority"`
		Result                  string `json:"result"`
		LevelTriggerFingerprint string `json:"level_trigger_fingerprint"`
	}
	levels := make([]semanticLevel, 0, len(results))
	for _, result := range results {
		if !sha256Pattern.MatchString(result.LevelTriggerFingerprint) {
			return "", invalid("trigger_event.level_results.level_trigger_fingerprint", "must be 64 lowercase hexadecimal characters")
		}
		levels = append(levels, semanticLevel{
			LevelID: result.LevelID, Priority: result.Priority, Result: result.Result,
			LevelTriggerFingerprint: result.LevelTriggerFingerprint,
		})
	}
	return digestCanonicalV2("trigger_event.event_semantic_digest", "event-semantic-digest-v1", struct {
		EventKind               string          `json:"event_kind"`
		Levels                  []semanticLevel `json:"levels"`
		DetectPlanFingerprint   string          `json:"detect_plan_fingerprint"`
		TriggerStateFingerprint string          `json:"trigger_state_fingerprint"`
	}{eventKind, levels, detectPlanFingerprint, triggerStateFingerprint})
}

func DeriveTriggerEventIDV1(tenantID, businessID, strategyID, stateCompatibilityHash, recordID, eventKind, semanticDigest string) (string, error) {
	if tenantID == "" || !utf8.ValidString(tenantID) || !canonicalSignedDecimalPattern.MatchString(businessID) ||
		!canonicalDecimalPattern.MatchString(strategyID) || !sha256Pattern.MatchString(stateCompatibilityHash) ||
		!sha256Pattern.MatchString(recordID) || !sha256Pattern.MatchString(semanticDigest) {
		return "", invalid("trigger_event.event_id", "contains invalid identity")
	}
	if eventKind != TriggerEventAbnormal && eventKind != TriggerEventRecovery {
		return "", invalid("trigger_event.event_kind", "unsupported value")
	}
	return deriveLengthPrefixedSHA256(
		"trigger_event.event_id", "event-id-v1", []byte(tenantID), []byte(businessID), []byte(strategyID),
		[]byte(stateCompatibilityHash), []byte(recordID), []byte(eventKind), []byte(semanticDigest),
	)
}

func ValidateTriggerEventV1(event *TriggerEventV1) error {
	if event == nil {
		return invalid("trigger_event", "must be non-null")
	}
	if event.Schema.Name != TriggerEventSchemaV1 || event.Schema.Major != 1 || event.Schema.Minor != 0 || event.RequiredFeatures == nil || len(event.RequiredFeatures) != 0 {
		return invalid("trigger_event.schema", "unsupported header")
	}
	if event.TenantID == "" || !utf8.ValidString(event.TenantID) || !canonicalSignedDecimalPattern.MatchString(event.BusinessID) ||
		!canonicalDecimalPattern.MatchString(event.PlanRef.StrategyID) || event.PlanRef.StrategyRevision == "" || !utf8.ValidString(event.PlanRef.StrategyRevision) ||
		!sha256Pattern.MatchString(event.PlanRef.StateCompatibilityHash) || !sha256Pattern.MatchString(event.EventSemanticDigest) ||
		!sha256Pattern.MatchString(event.RecordRef.RecordID) || !sha256Pattern.MatchString(event.RecordRef.DimensionIdentityDigest) ||
		!sha256Pattern.MatchString(event.DetectPlanFingerprint) || !sha256Pattern.MatchString(event.TriggerStateFingerprint) ||
		event.RecordRef.SourceTime < 0 || event.EvaluationTime < 0 || !isOpaqueASCII(event.Trace.ExecutionID) {
		return invalid("trigger_event", "contains invalid identity, fingerprint or time")
	}
	if event.RecordRef.Dimensions == nil || event.Observed.Values == nil {
		return invalid("trigger_event", "dimensions and observed values must be objects")
	}
	if err := validateSuccessfulLevelResultsV1(event.LevelResults); err != nil {
		return err
	}
	for _, result := range event.LevelResults {
		if result.DecisionWindow.SourceTime != event.RecordRef.SourceTime {
			return invalid("trigger_event.level_results.decision_window.source_time", "must match record_ref.source_time")
		}
	}
	kind, primary, err := aggregateTriggerEventV1(event.LevelResults)
	if err != nil {
		return err
	}
	if kind != event.EventKind || primary != event.PrimaryLevelID {
		return invalid("trigger_event", "event kind or primary Level does not match Level results")
	}
	expectedSemanticDigest, err := DeriveEventSemanticDigestV1(
		event.EventKind, event.LevelResults, event.DetectPlanFingerprint, event.TriggerStateFingerprint,
	)
	if err != nil {
		return err
	}
	if event.EventSemanticDigest != expectedSemanticDigest {
		return invalid("trigger_event.event_semantic_digest", "does not match canonical Level results and fingerprints")
	}
	expectedID, err := DeriveTriggerEventIDV1(
		event.TenantID, event.BusinessID, event.PlanRef.StrategyID, event.PlanRef.StateCompatibilityHash,
		event.RecordRef.RecordID, event.EventKind, event.EventSemanticDigest,
	)
	if err != nil {
		return err
	}
	if event.EventID != expectedID {
		return invalid("trigger_event.event_id", "does not match stable identity")
	}
	return nil
}

func EncodeTriggerEventV1(event *TriggerEventV1) ([]byte, error) {
	if err := ValidateTriggerEventV1(event); err != nil {
		return nil, err
	}
	return CanonicalJSONV2(event)
}

func DecodeTriggerEventV1(payload []byte) (*TriggerEventV1, error) {
	limit := len(payload)
	if limit == 0 {
		limit = 1
	}
	return DecodeTriggerEventV1WithLimits(payload, TriggerEventReaderLimitsV1{MaxPayloadBytes: limit, MaxEvidenceBytes: limit})
}

func DecodeTriggerEventV1WithLimits(payload []byte, limits TriggerEventReaderLimitsV1) (*TriggerEventV1, error) {
	if limits.MaxPayloadBytes <= 0 || limits.MaxEvidenceBytes <= 0 {
		return nil, invalid("trigger_event.reader_limits", "payload and evidence limits must be positive")
	}
	if len(payload) == 0 || len(payload) > limits.MaxPayloadBytes {
		return nil, invalid("trigger_event", "encoded bytes exceed Reader limit")
	}
	required := []string{
		"schema", "required_features", "event_id", "event_semantic_digest", "event_kind", "primary_level_id", "tenant_id",
		"business_id", "plan_ref", "record_ref", "observed", "level_results", "evaluation_time", "detect_plan_fingerprint",
		"trigger_state_fingerprint", "trace",
	}
	object, err := validateOutputHeaderV1(payload, "trigger_event", TriggerEventSchemaV1, required)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(object["level_results"])) > limits.MaxEvidenceBytes {
		return nil, invalid("trigger_event.level_results", "encoded evidence exceeds Reader limit")
	}
	nested := []struct {
		field    string
		raw      json.RawMessage
		required []string
		optional []string
	}{
		{"plan_ref", object["plan_ref"], []string{"strategy_id", "strategy_revision", "state_compatibility_hash"}, nil},
		{"record_ref", object["record_ref"], []string{"record_id", "source_time", "dimension_identity_digest", "dimensions"}, nil},
		{"observed", object["observed"], []string{"values", "unit"}, nil},
		{"trace", object["trace"], []string{"execution_id"}, nil},
	}
	for _, item := range nested {
		if _, err := validateJSONObjectFields(item.raw, "trigger_event."+item.field, item.required, item.optional, false); err != nil {
			return nil, err
		}
	}
	var rawResults []json.RawMessage
	if err := decodeJSONObject(object["level_results"], &rawResults); err != nil {
		return nil, err
	}
	for index, rawResult := range rawResults {
		path := "trigger_event.level_results[" + strconv.Itoa(index) + "]"
		resultObject, err := validateJSONObjectFields(rawResult, path, []string{"level_id", "priority", "result", "decision_window", "detect_evidence", "level_trigger_fingerprint"}, []string{"level_code"}, false)
		if err != nil {
			return nil, err
		}
		if code, ok := resultObject["level_code"]; ok && bytes.Equal(bytes.TrimSpace(code), []byte("null")) {
			return nil, invalid(path+".level_code", "must be omitted instead of null")
		}
		window, err := validateJSONObjectFields(resultObject["decision_window"], path+".decision_window", []string{"type", "version", "source_time", "trigger", "recovery", "history_completeness", "window_evidence"}, nil, false)
		if err != nil {
			return nil, err
		}
		if _, err := validateJSONObjectFields(window["trigger"], path+".decision_window.trigger", []string{"window_start", "window_end", "window_size", "required_anomalies", "observed_anomalies"}, nil, false); err != nil {
			return nil, err
		}
		if _, err := validateJSONObjectFields(window["recovery"], path+".decision_window.recovery", []string{"enabled", "required_consecutive_windows", "observed_consecutive_misses", "oldest_window_start"}, nil, false); err != nil {
			return nil, err
		}
		if _, err := validateJSONObjectFields(window["window_evidence"], path+".decision_window.window_evidence", []string{"anomaly_timestamps_digest", "late_accepted"}, nil, false); err != nil {
			return nil, err
		}
		detect, err := validateJSONObjectFields(resultObject["detect_evidence"], path+".detect_evidence", []string{"detection_result", "predicate_digest", "normalized_value", "effective_time_status"}, []string{"matched_algorithm_ordinal", "matched_group_ordinal", "result_reason"}, false)
		if err != nil {
			return nil, err
		}
		for _, optional := range []string{"matched_algorithm_ordinal", "matched_group_ordinal", "result_reason"} {
			if value, ok := detect[optional]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, invalid(path+".detect_evidence."+optional, "must be omitted instead of null")
			}
		}
	}
	var event TriggerEventV1
	if err := decodeJSONObject(payload, &event); err != nil {
		return nil, err
	}
	if err := ValidateTriggerEventV1(&event); err != nil {
		return nil, err
	}
	return &event, nil
}

func validateSuccessfulLevelResultsV1(results []LevelResultV1) error {
	if len(results) == 0 {
		return invalid("trigger_event.level_results", "must contain at least one successful active Level result")
	}
	previous := uint32(0)
	for index := range results {
		result := results[index]
		if result.LevelID == 0 || result.Priority == 0 || (index > 0 && result.LevelID <= previous) {
			return invalid("trigger_event.level_results", "Level ids must be positive, sorted and unique")
		}
		if !sha256Pattern.MatchString(result.LevelTriggerFingerprint) {
			return invalid("trigger_event.level_results.level_trigger_fingerprint", "must be 64 lowercase hexadecimal characters")
		}
		switch result.Result {
		case LevelResultAbnormal, LevelResultNormal, LevelResultRecovery:
		default:
			return invalid("trigger_event.level_results.result", "only successful active three-state results are allowed")
		}
		window := result.DecisionWindow
		if window.Type != "N_OF_M_WITH_CONTINUOUS_MISS" || window.Version != 1 || window.SourceTime < 0 ||
			window.Trigger.WindowStart < 0 || window.Trigger.WindowEnd < window.Trigger.WindowStart || window.Trigger.WindowSize == 0 ||
			window.Trigger.WindowEnd != window.SourceTime ||
			window.Trigger.RequiredAnomalies == 0 || window.Trigger.RequiredAnomalies > window.Trigger.WindowSize ||
			window.Trigger.ObservedAnomalies > window.Trigger.WindowSize ||
			window.Recovery.OldestWindowStart < 0 || window.Recovery.OldestWindowStart > window.SourceTime ||
			!sha256Pattern.MatchString(window.WindowEvidence.AnomalyTimestampsDigest) {
			return invalid("trigger_event.level_results.decision_window", "contains invalid bounded window evidence")
		}
		if window.HistoryCompleteness != "FULL" && window.HistoryCompleteness != "WARMING" && window.HistoryCompleteness != "GAPPED" {
			return invalid("trigger_event.level_results.decision_window.history_completeness", "must be FULL, WARMING or GAPPED")
		}
		if window.Recovery.Enabled && window.Recovery.RequiredConsecutiveWindows == 0 {
			return invalid("trigger_event.level_results.decision_window.recovery", "enabled recovery requires a positive window count")
		}
		detect := result.DetectEvidence
		if (detect.DetectionResult != "ANOMALOUS" && detect.DetectionResult != "NORMAL") ||
			!sha256Pattern.MatchString(detect.PredicateDigest) || len(detect.NormalizedValue) == 0 ||
			detect.EffectiveTimeStatus != "ACTIVE" {
			return invalid("trigger_event.level_results.detect_evidence", "contains invalid successful active Detect evidence")
		}
		if _, err := CanonicalJSONV2(detect.NormalizedValue); err != nil {
			return invalid("trigger_event.level_results.detect_evidence.normalized_value", err.Error())
		}
		triggerSatisfied := window.Trigger.ObservedAnomalies >= window.Trigger.RequiredAnomalies
		recoverySatisfied := window.Recovery.Enabled &&
			window.Recovery.ObservedConsecutiveMisses >= window.Recovery.RequiredConsecutiveWindows
		if triggerSatisfied && recoverySatisfied {
			return invalid("trigger_event.level_results.decision_window", "trigger and recovery cannot both be satisfied")
		}
		expectedResult := LevelResultNormal
		if detect.DetectionResult == "ANOMALOUS" && triggerSatisfied {
			expectedResult = LevelResultAbnormal
		} else if !triggerSatisfied && recoverySatisfied {
			expectedResult = LevelResultRecovery
		}
		if result.Result != expectedResult {
			return invalid("trigger_event.level_results.result", "does not match current Detect result and trigger/recovery window evidence")
		}
		if window.HistoryCompleteness != "FULL" && result.Result != LevelResultAbnormal {
			return invalid("trigger_event.level_results.result", "WARMING and GAPPED history permit only monotonic ABNORMAL")
		}
		previous = result.LevelID
	}
	return nil
}

func aggregateTriggerEventV1(results []LevelResultV1) (string, uint32, error) {
	kind := ""
	for _, result := range results {
		if result.Result == LevelResultAbnormal {
			kind = TriggerEventAbnormal
			break
		}
		if result.Result == LevelResultRecovery {
			kind = TriggerEventRecovery
		}
	}
	if kind == "" {
		return "", 0, invalid("trigger_event.level_results", "all NORMAL results do not produce an event")
	}
	primary := uint32(0)
	primaryPriority := ^uint32(0)
	for _, result := range results {
		if result.Result != kind {
			continue
		}
		if result.Priority < primaryPriority || (result.Priority == primaryPriority && (primary == 0 || result.LevelID < primary)) {
			primary = result.LevelID
			primaryPriority = result.Priority
		}
	}
	return kind, primary, nil
}

func BuildMessageReceiptV1(receipt MessageReceiptV1) (*MessageReceiptV1, error) {
	receipt.Schema = Schema{Name: MessageReceiptSchemaV1, Major: 1, Minor: 0}
	receipt.RequiredFeatures = []string{}
	if receipt.PerPlan == nil {
		receipt.PerPlan = []PlanReceiptV1{}
	}
	if receipt.ReasonCounts == nil {
		receipt.ReasonCounts = []ReasonCountV1{}
	}
	sort.Slice(receipt.PerPlan, func(left, right int) bool {
		return compareCanonicalDecimal(receipt.PerPlan[left].PlanID, receipt.PerPlan[right].PlanID) < 0
	})
	sort.Slice(receipt.ReasonCounts, func(left, right int) bool {
		return receipt.ReasonCounts[left].ReasonCode < receipt.ReasonCounts[right].ReasonCode
	})
	receiptID, err := deriveLengthPrefixedSHA256("message_receipt.receipt_id", "message-receipt-id-v1", []byte(receipt.MessageID), []byte(receipt.PayloadDigest))
	if err != nil {
		return nil, err
	}
	receipt.ReceiptID = receiptID
	if err := ValidateMessageReceiptV1(&receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func ValidateMessageReceiptV1(receipt *MessageReceiptV1) error {
	if receipt == nil || receipt.Schema.Name != MessageReceiptSchemaV1 || receipt.Schema.Major != 1 || receipt.Schema.Minor != 0 ||
		receipt.RequiredFeatures == nil || len(receipt.RequiredFeatures) != 0 || !isOpaqueASCII(receipt.ExecutionID) || !isOpaqueASCII(receipt.MessageID) ||
		!sha256Pattern.MatchString(receipt.PayloadDigest) || !sha256Pattern.MatchString(receipt.PlanSetDigest) ||
		receipt.SourceWindow.FromTime < 0 || receipt.SourceWindow.UntilTime < receipt.SourceWindow.FromTime ||
		receipt.PerPlan == nil || receipt.ReasonCounts == nil {
		return invalid("message_receipt", "contains invalid header or identity")
	}
	if receipt.Status != ReceiptStatusCompleted && receipt.Status != ReceiptStatusCompletedWithTerminal && receipt.Status != ReceiptStatusRejected {
		return invalid("message_receipt.status", "unsupported value")
	}
	expected, err := deriveLengthPrefixedSHA256("message_receipt.receipt_id", "message-receipt-id-v1", []byte(receipt.MessageID), []byte(receipt.PayloadDigest))
	if err != nil || expected != receipt.ReceiptID {
		return invalid("message_receipt.receipt_id", "does not match stable identity")
	}
	if !sortedPlanReceiptsV1(receipt.PerPlan) || !sortedReasonCountsV2(receipt.ReasonCounts, ReasonDomainReceipt) {
		return invalid("message_receipt", "per_plan and reason_counts must be sorted and unique")
	}
	if err := validateReceiptCountsV1(receipt); err != nil {
		return err
	}
	return nil
}

func EncodeMessageReceiptV1(receipt *MessageReceiptV1) ([]byte, error) {
	if err := ValidateMessageReceiptV1(receipt); err != nil {
		return nil, err
	}
	return CanonicalJSONV2(receipt)
}

func BuildExecutionSummaryV1(summary ExecutionSummaryV1) (*ExecutionSummaryV1, error) {
	summary.Schema = Schema{Name: ExecutionSummarySchemaV1, Major: 1, Minor: 0}
	summary.RequiredFeatures = []string{}
	if summary.ReasonCounts == nil {
		summary.ReasonCounts = []ReasonCountV1{}
	}
	sort.Slice(summary.ReasonCounts, func(left, right int) bool {
		return summary.ReasonCounts[left].ReasonCode < summary.ReasonCounts[right].ReasonCode
	})
	window, err := CanonicalJSONV2(summary.SourceWindow)
	if err != nil {
		return nil, err
	}
	summaryID, err := deriveLengthPrefixedSHA256(
		"execution_summary.summary_id", "execution-summary-id-v1", []byte(summary.ExecutionID), []byte(summary.PlanSetDigest), window,
	)
	if err != nil {
		return nil, err
	}
	summary.SummaryID = summaryID
	if err := ValidateExecutionSummaryV1(&summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func ValidateExecutionSummaryV1(summary *ExecutionSummaryV1) error {
	if summary == nil || summary.Schema.Name != ExecutionSummarySchemaV1 || summary.Schema.Major != 1 || summary.Schema.Minor != 0 ||
		summary.RequiredFeatures == nil || len(summary.RequiredFeatures) != 0 || !isOpaqueASCII(summary.ExecutionID) ||
		summary.TenantID == "" || !utf8.ValidString(summary.TenantID) || summary.QueryGroupKey == "" || !utf8.ValidString(summary.QueryGroupKey) ||
		!sha256Pattern.MatchString(summary.PlanSetDigest) ||
		summary.SourceWindow.FromTime < 0 || summary.SourceWindow.UntilTime < summary.SourceWindow.FromTime ||
		summary.ReasonCounts == nil || !sortedReasonCountsV2(summary.ReasonCounts, ReasonDomainSummary) {
		return invalid("execution_summary", "contains invalid header, identity, window or reasons")
	}
	window, err := CanonicalJSONV2(summary.SourceWindow)
	if err != nil {
		return err
	}
	expected, err := deriveLengthPrefixedSHA256("execution_summary.summary_id", "execution-summary-id-v1", []byte(summary.ExecutionID), []byte(summary.PlanSetDigest), window)
	if err != nil || expected != summary.SummaryID {
		return invalid("execution_summary.summary_id", "does not match stable identity")
	}
	if !countSetBalancedV1(summary.Source, summary.Published, summary.Dropped) {
		return invalid("execution_summary", "source must equal published plus dropped for messages, records and bytes")
	}
	hasDropped := summary.Dropped.Messages != 0 || summary.Dropped.Records != 0 || summary.Dropped.Bytes != 0
	if hasDropped != (len(summary.ReasonCounts) != 0) {
		return invalid("execution_summary.reason_counts", "must be non-empty exactly when data was dropped")
	}
	return nil
}

func EncodeExecutionSummaryV1(summary *ExecutionSummaryV1) ([]byte, error) {
	if err := ValidateExecutionSummaryV1(summary); err != nil {
		return nil, err
	}
	return CanonicalJSONV2(summary)
}

func sortedPlanReceiptsV1(values []PlanReceiptV1) bool {
	previous := ""
	for index, value := range values {
		if !canonicalDecimalPattern.MatchString(value.PlanID) || !sha256Pattern.MatchString(value.ResultIdentityDigest) ||
			(index > 0 && compareCanonicalDecimal(value.PlanID, previous) <= 0) {
			return false
		}
		previous = value.PlanID
	}
	return true
}

func sortedReasonCountsV2(values []ReasonCountV1, domain ReasonDomainsV2) bool {
	previous := ""
	for index, value := range values {
		if !ReasonAllowedForV2(value.ReasonCode, domain) || value.Count == 0 || (index > 0 && value.ReasonCode <= previous) {
			return false
		}
		previous = value.ReasonCode
	}
	return true
}

func validateReceiptCountsV1(receipt *MessageReceiptV1) error {
	if receipt.Status == ReceiptStatusRejected {
		if receipt.Counts != (ReceiptCountsV1{}) || len(receipt.PerPlan) != 0 || len(receipt.ReasonCounts) == 0 {
			return invalid("message_receipt", "REJECTED must have zero business counts, empty per_plan and a reason")
		}
		return nil
	}

	var selected, processed, unavailable, terminal, events uint64
	for _, plan := range receipt.PerPlan {
		if plan.Selected > receipt.Counts.Received {
			return invalid("message_receipt.per_plan.selected", "cannot exceed received Dataset records")
		}
		planProcessed, ok := sumCountsV1(plan.Abnormal, plan.Normal, plan.Recovery)
		if !ok {
			return invalid("message_receipt.per_plan", "count overflow")
		}
		planSelected, ok := sumCountsV1(planProcessed, plan.Unavailable, plan.Terminal)
		if !ok || plan.Selected != planSelected {
			return invalid("message_receipt.per_plan.selected", "must equal abnormal + normal + recovery + unavailable + terminal")
		}
		planEvents, ok := sumCountsV1(plan.Abnormal, plan.Recovery)
		if !ok || !addCountV1(&selected, plan.Selected) || !addCountV1(&processed, planProcessed) ||
			!addCountV1(&unavailable, plan.Unavailable) || !addCountV1(&terminal, plan.Terminal) || !addCountV1(&events, planEvents) {
			return invalid("message_receipt.per_plan", "count overflow")
		}
	}
	if selected != receipt.Counts.Selected || processed != receipt.Counts.Processed || unavailable != receipt.Counts.Unavailable ||
		terminal != receipt.Counts.Terminal || events != receipt.Counts.Events {
		return invalid("message_receipt.counts", "must equal per_plan result sums")
	}
	topSelected, ok := sumCountsV1(receipt.Counts.Processed, receipt.Counts.Unavailable, receipt.Counts.Terminal)
	if !ok || topSelected != receipt.Counts.Selected {
		return invalid("message_receipt.counts.selected", "must equal processed + unavailable + terminal")
	}
	wantStatus := ReceiptStatusCompleted
	if receipt.Counts.Terminal != 0 {
		wantStatus = ReceiptStatusCompletedWithTerminal
	}
	if receipt.Status != wantStatus {
		return invalid("message_receipt.status", "does not match terminal count")
	}
	return nil
}

func sumCountsV1(values ...uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		if !addCountV1(&total, value) {
			return 0, false
		}
	}
	return total, true
}

func addCountV1(total *uint64, value uint64) bool {
	if ^uint64(0)-*total < value {
		return false
	}
	*total += value
	return true
}

func countSetBalancedV1(source, published, dropped CountSetV1) bool {
	return countBalancedV1(source.Messages, published.Messages, dropped.Messages) &&
		countBalancedV1(source.Records, published.Records, dropped.Records) &&
		countBalancedV1(source.Bytes, published.Bytes, dropped.Bytes)
}

func countBalancedV1(source, published, dropped uint64) bool {
	return published <= source && source-published == dropped
}

// Decode helpers intentionally use strict JSON object decoding and then the
// same validators used by Writers; they do not accept null optionals.
func DecodeMessageReceiptV1(payload []byte) (*MessageReceiptV1, error) {
	required := []string{
		"schema", "required_features", "receipt_id", "execution_id", "message_id", "payload_digest", "plan_set_digest",
		"source_window", "status", "counts", "per_plan", "reason_counts",
	}
	object, err := validateOutputHeaderV1(payload, "message_receipt", MessageReceiptSchemaV1, required)
	if err != nil {
		return nil, err
	}
	if _, err := validateJSONObjectFields(object["source_window"], "message_receipt.source_window", []string{"from_time", "until_time"}, nil, false); err != nil {
		return nil, err
	}
	if _, err := validateJSONObjectFields(object["counts"], "message_receipt.counts", []string{"received", "selected", "processed", "unavailable", "terminal", "events"}, nil, false); err != nil {
		return nil, err
	}
	if err := validateRawObjectArrayV1(object["per_plan"], "message_receipt.per_plan", []string{"plan_id", "selected", "abnormal", "normal", "recovery", "unavailable", "terminal", "result_identity_digest"}); err != nil {
		return nil, err
	}
	if err := validateRawObjectArrayV1(object["reason_counts"], "message_receipt.reason_counts", []string{"reason_code", "count"}); err != nil {
		return nil, err
	}
	var receipt MessageReceiptV1
	if err := decodeJSONObject(payload, &receipt); err != nil {
		return nil, err
	}
	if err := ValidateMessageReceiptV1(&receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func DecodeExecutionSummaryV1(payload []byte) (*ExecutionSummaryV1, error) {
	required := []string{
		"schema", "required_features", "summary_id", "execution_id", "tenant_id", "query_group_key", "source_window",
		"plan_set_digest", "source", "published", "dropped", "reason_counts",
	}
	object, err := validateOutputHeaderV1(payload, "execution_summary", ExecutionSummarySchemaV1, required)
	if err != nil {
		return nil, err
	}
	if _, err := validateJSONObjectFields(object["source_window"], "execution_summary.source_window", []string{"from_time", "until_time"}, nil, false); err != nil {
		return nil, err
	}
	for _, field := range []string{"source", "published", "dropped"} {
		if _, err := validateJSONObjectFields(object[field], "execution_summary."+field, []string{"messages", "records", "bytes"}, nil, false); err != nil {
			return nil, err
		}
	}
	if err := validateRawObjectArrayV1(object["reason_counts"], "execution_summary.reason_counts", []string{"reason_code", "count"}); err != nil {
		return nil, err
	}
	var summary ExecutionSummaryV1
	if err := decodeJSONObject(payload, &summary); err != nil {
		return nil, err
	}
	if err := ValidateExecutionSummaryV1(&summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func validateOutputHeaderV1(payload []byte, field, schemaName string, required []string) (map[string]json.RawMessage, error) {
	object, err := validateJSONObjectFields(payload, field, required, nil, false)
	if err != nil {
		return nil, err
	}
	if _, err := validateSchemaRawV2(object["schema"], field+".schema", schemaName, 1); err != nil {
		return nil, err
	}
	if err := validateRequiredFeaturesRawV2(object["required_features"], field+".required_features"); err != nil {
		return nil, err
	}
	return object, nil
}

func validateRawObjectArrayV1(raw json.RawMessage, field string, required []string) error {
	var values []json.RawMessage
	if err := decodeJSONObject(raw, &values); err != nil || values == nil {
		return invalid(field, "must be an array")
	}
	for index, value := range values {
		if _, err := validateJSONObjectFields(value, field+"["+strconv.Itoa(index)+"]", required, nil, false); err != nil {
			return err
		}
	}
	return nil
}
