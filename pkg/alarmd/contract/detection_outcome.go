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
	"fmt"
)

const (
	OutcomeNormal      = "NORMAL"
	OutcomeAnomalous   = "ANOMALOUS"
	OutcomeError       = "ERROR"
	OutcomeUnsupported = "UNSUPPORTED"

	EvaluationNormal    = "NORMAL"
	EvaluationAnomalous = "ANOMALOUS"
)

var errorCodes = map[string]map[string]struct{}{
	OutcomeError: {
		"ALGORITHM_ERROR": {},
		"INTERNAL_ERROR":  {},
		"INVALID_INPUT":   {},
	},
	OutcomeUnsupported: {
		"UNSUPPORTED_FEATURE":  {},
		"UNSUPPORTED_STRATEGY": {},
	},
}

type DetectionRecord struct {
	RecordID      string          `json:"record_id"`
	SourceTime    int64           `json:"source_time"`
	DimensionsMD5 string          `json:"dimensions_md5"`
	DataRaw       json.RawMessage `json:"data_raw"`
}

type Evaluation struct {
	Level   int             `json:"level"`
	Result  string          `json:"result"`
	Anomaly json.RawMessage `json:"anomaly,omitempty"`
}

type DetectionOutcome struct {
	Schema           Schema          `json:"schema"`
	RequiredFeatures []string        `json:"required_features"`
	InputID          string          `json:"input_id"`
	BatchID          string          `json:"batch_id"`
	TenantID         string          `json:"tenant_id"`
	Purpose          string          `json:"purpose"`
	StrategyRef      StrategyRef     `json:"strategy_ref"`
	Record           DetectionRecord `json:"record"`
	Evaluations      []Evaluation    `json:"evaluations"`
	Outcome          string          `json:"outcome"`
	ErrorCode        json.RawMessage `json:"error_code,omitempty"`
}

func DecodeDetectionOutcome(payload []byte, strategy *TriggerStrategyIR) (*DetectionOutcome, error) {
	return decodeDetectionOutcome(payload, strategy, true)
}

func decodeDetectionOutcome(payload []byte, strategy *TriggerStrategyIR, validateStrategy bool) (*DetectionOutcome, error) {
	schema, object, err := validateContractEnvelope(
		payload,
		"detection_outcome",
		detectionOutcomeSchema,
		[]string{
			"schema", "required_features", "input_id", "batch_id", "tenant_id", "purpose", "strategy_ref",
			"record", "evaluations", "outcome",
		},
		[]string{"error_code"},
	)
	if err != nil {
		return nil, err
	}
	allowUnknown := schema.Minor > 0
	if _, err := validateJSONObjectFields(
		object["strategy_ref"],
		"detection_outcome.strategy_ref",
		[]string{"strategy_id", "item_id", "generation", "content_sha256"},
		nil,
		allowUnknown,
	); err != nil {
		return nil, err
	}
	record, err := validateJSONObjectFields(
		object["record"],
		"detection_outcome.record",
		[]string{"record_id", "source_time", "dimensions_md5", "data_raw"},
		nil,
		allowUnknown,
	)
	if err != nil {
		return nil, err
	}
	if _, err := validateJSONObjectFields(
		record["data_raw"],
		"detection_outcome.record.data_raw",
		[]string{"record_id", "time"},
		[]string{"values"},
		true,
	); err != nil {
		return nil, err
	}
	var evaluations []json.RawMessage
	if err := decodeJSONObject(object["evaluations"], &evaluations); err != nil {
		return nil, invalid("detection_outcome.evaluations", err.Error())
	}
	for _, evaluation := range evaluations {
		evaluationObject, err := validateJSONObjectFields(
			evaluation,
			"detection_outcome.evaluations",
			[]string{"level", "result"},
			[]string{"anomaly"},
			allowUnknown,
		)
		if err != nil {
			return nil, err
		}
		if anomaly, ok := evaluationObject["anomaly"]; ok && !bytes.Equal(bytes.TrimSpace(anomaly), []byte("null")) {
			if _, err := validateJSONObjectFields(
				anomaly,
				"detection_outcome.evaluations.anomaly",
				[]string{"anomaly_id"},
				[]string{"anomaly_message", "context"},
				true,
			); err != nil {
				return nil, err
			}
		}
	}
	var outcome DetectionOutcome
	if err := decodeJSONObject(payload, &outcome); err != nil {
		return nil, err
	}
	if validateStrategy {
		if err := outcome.Validate(strategy); err != nil {
			return nil, err
		}
	} else if err := outcome.validateWithStrategy(strategy); err != nil {
		return nil, err
	}
	return &outcome, nil
}

func (o *DetectionOutcome) Validate(strategy *TriggerStrategyIR) error {
	if o == nil {
		return invalid("detection_outcome", "must be non-null")
	}
	if err := strategy.Validate(); err != nil {
		return err
	}
	return o.validateWithStrategy(strategy)
}

func (o *DetectionOutcome) validateWithStrategy(strategy *TriggerStrategyIR) error {
	if err := validateHeader(o.Schema, o.RequiredFeatures, detectionOutcomeSchema, map[string]struct{}{
		featureFullLevelEvaluations: {},
		featureRawJSON:              {},
	}); err != nil {
		return err
	}
	if o.TenantID == "" || o.BatchID == "" {
		return invalid("detection_outcome", "tenant_id and batch_id must be non-empty")
	}
	if err := validatePurpose(o.Purpose); err != nil {
		return err
	}
	if err := validateStrategyRef(o.StrategyRef); err != nil {
		return err
	}
	if o.TenantID != strategy.TenantID || o.Purpose != strategy.Purpose || o.StrategyRef != strategy.StrategyRef {
		return invalid("strategy_ref", "outcome does not match StrategyIR")
	}

	dimensionsMD5, sourceTime, err := parseRecordID(o.Record.RecordID)
	if err != nil {
		return err
	}
	if o.Record.DimensionsMD5 != dimensionsMD5 || o.Record.SourceTime != sourceTime {
		return invalid("record", "source coordinate does not match record_id")
	}
	if err := validateDataRaw(o.Record.DataRaw, o.Record.RecordID, sourceTime); err != nil {
		return err
	}
	expectedInputID, err := DeriveInputID(InputIdentity{
		TenantID:              o.TenantID,
		Purpose:               o.Purpose,
		StrategyID:            o.StrategyRef.StrategyID,
		ItemID:                o.StrategyRef.ItemID,
		StrategyContentSHA256: o.StrategyRef.ContentSHA256,
		RecordID:              o.Record.RecordID,
	})
	if err != nil {
		return err
	}
	if o.InputID != expectedInputID {
		return invalid("input_id", "does not match canonical tuple")
	}

	if err := validateOutcomeAndErrorCode(o.Outcome, o.ErrorCode); err != nil {
		return err
	}
	requiredLevels := make(map[int]struct{}, len(strategy.RequiredLevels))
	for _, level := range strategy.RequiredLevels {
		requiredLevels[level] = struct{}{}
	}
	seenLevels := make(map[int]struct{}, len(o.Evaluations))
	anomalousCount := 0
	for _, evaluation := range o.Evaluations {
		if evaluation.Level <= 0 || evaluation.Level > maxContractInt {
			return invalid("evaluations.level", "must be a positive 32-bit signed integer")
		}
		if _, ok := requiredLevels[evaluation.Level]; !ok {
			return invalid("evaluations.level", "is not required by StrategyIR")
		}
		if _, ok := seenLevels[evaluation.Level]; ok {
			return invalid("evaluations", "contains duplicate level")
		}
		seenLevels[evaluation.Level] = struct{}{}
		switch evaluation.Result {
		case EvaluationNormal:
			if len(evaluation.Anomaly) != 0 {
				return invalid("evaluations.anomaly", "NORMAL evaluation must not carry anomaly")
			}
		case EvaluationAnomalous:
			if err := validateAnomaly(evaluation.Anomaly, o.Record.RecordID, o.StrategyRef, evaluation.Level); err != nil {
				return err
			}
			anomalousCount++
		default:
			return invalid("evaluations.result", "unsupported evaluation result")
		}
	}
	if o.Outcome == OutcomeNormal || o.Outcome == OutcomeAnomalous {
		if len(seenLevels) != len(requiredLevels) {
			return invalid("evaluations", "business outcome evaluations must be complete")
		}
	}
	if o.Outcome == OutcomeNormal && anomalousCount != 0 {
		return invalid("outcome", "NORMAL outcome must contain only NORMAL evaluations")
	}
	if o.Outcome == OutcomeAnomalous && anomalousCount == 0 {
		return invalid("outcome", "ANOMALOUS outcome must contain an anomalous evaluation")
	}
	return nil
}

func (o *DetectionOutcome) CanDriveTrigger(strategy *TriggerStrategyIR) (bool, error) {
	if err := o.Validate(strategy); err != nil {
		return false, err
	}
	return o.Outcome == OutcomeNormal || o.Outcome == OutcomeAnomalous, nil
}

func validateOutcomeAndErrorCode(outcome string, errorCode json.RawMessage) error {
	switch outcome {
	case OutcomeNormal, OutcomeAnomalous:
		if len(errorCode) != 0 {
			return invalid("error_code", "business outcome must not carry error_code")
		}
		return nil
	case OutcomeError, OutcomeUnsupported:
		if len(errorCode) == 0 || bytes.Equal(errorCode, []byte("null")) {
			return invalid("error_code", "error outcome requires error_code")
		}
		var code string
		if err := decodeJSONObject(errorCode, &code); err != nil {
			return invalid("error_code", "must be a string")
		}
		if _, ok := errorCodes[outcome][code]; !ok {
			return invalid("error_code", "unsupported code for outcome")
		}
		return nil
	default:
		return invalid("outcome", "unsupported detection outcome")
	}
}

func validateDataRaw(dataRaw json.RawMessage, recordID string, sourceTime int64) error {
	var data struct {
		RecordID string          `json:"record_id"`
		Time     int64           `json:"time"`
		Values   json.RawMessage `json:"values"`
	}
	if err := decodeJSONObject(dataRaw, &data); err != nil {
		return invalid("record.data_raw", err.Error())
	}
	if data.RecordID != recordID || data.Time != sourceTime {
		return invalid("record.data_raw", "source coordinate mismatch")
	}
	if len(data.Values) == 0 || bytes.Equal(data.Values, []byte("null")) {
		return nil
	}
	if bytes.TrimSpace(data.Values)[0] != '{' {
		return nil
	}
	var values map[string]json.RawMessage
	if err := decodeJSONObject(data.Values, &values); err != nil {
		return invalid("record.data_raw.values", err.Error())
	}
	if rawTimestamp, ok := values["timestamp"]; ok {
		if bytes.Equal(bytes.TrimSpace(rawTimestamp), []byte("null")) {
			return invalid("record.data_raw.values.timestamp", "must be an integer")
		}
		var timestamp int64
		if err := decodeJSONObject(rawTimestamp, &timestamp); err != nil {
			return invalid("record.data_raw.values.timestamp", err.Error())
		}
		if timestamp != sourceTime {
			return invalid("record.data_raw.values.timestamp", "does not match source_time")
		}
	}
	return nil
}

func validateAnomaly(anomaly json.RawMessage, recordID string, ref StrategyRef, level int) error {
	if len(anomaly) == 0 || bytes.Equal(anomaly, []byte("null")) {
		return invalid("evaluations.anomaly", "ANOMALOUS evaluation requires anomaly")
	}
	var value struct {
		AnomalyID string `json:"anomaly_id"`
	}
	if err := decodeJSONObject(anomaly, &value); err != nil {
		return invalid("evaluations.anomaly", err.Error())
	}
	want := fmt.Sprintf("%s.%s.%s.%d", recordID, ref.StrategyID, ref.ItemID, level)
	if value.AnomalyID != want {
		return invalid("evaluations.anomaly.anomaly_id", "does not match record, strategy, item and level")
	}
	return nil
}
