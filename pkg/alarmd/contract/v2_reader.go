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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

type MessageFramingError struct {
	ReasonCode string
	FieldPath  string
	Message    string
}

func (e *MessageFramingError) Error() string {
	if e.FieldPath == "" {
		return fmt.Sprintf("alarmd contract framing: %s: %s", e.ReasonCode, e.Message)
	}
	return fmt.Sprintf("alarmd contract framing: %s: %s: %s", e.ReasonCode, e.FieldPath, e.Message)
}

func framing(reason, field, message string) error {
	return &MessageFramingError{ReasonCode: reason, FieldPath: field, Message: message}
}

// ReadExecutionEnvelopeV2 establishes message framing first, then reports
// bounded Plan/Level/record issues without promoting them to message failure.
func ReadExecutionEnvelopeV2(payload []byte, limits ReaderLimitsV2) (*FramedExecutionEnvelopeV2, []ValidationIssue, error) {
	if err := validateReaderLimitsV2(limits); err != nil {
		return nil, nil, framing(ReasonMessageBudgetExceeded, "reader_limits", err.Error())
	}
	if len(payload) == 0 || len(payload) > limits.MaxEnvelopeBytes {
		return nil, nil, framing(ReasonMessageBudgetExceeded, "execution_envelope", "encoded bytes exceed Reader limit")
	}
	if !utf8.Valid(payload) || bytes.HasPrefix(payload, []byte{0xef, 0xbb, 0xbf}) {
		return nil, nil, framing(ReasonMalformedJSON, "json", "must be valid UTF-8 without BOM")
	}
	if err := validateJSONBudgetsV2(payload, limits.MaxContractDepth, limits.MaxStringBytes); err != nil {
		var budgetError *jsonBudgetErrorV2
		if errors.As(err, &budgetError) {
			return nil, nil, framing(ReasonMessageBudgetExceeded, "json", err.Error())
		}
		return nil, nil, framing(ReasonMalformedJSON, "json", err.Error())
	}
	if err := validateJSONSurrogateEscapes(payload); err != nil {
		return nil, nil, framing(ReasonMalformedJSON, "json", err.Error())
	}
	if err := rejectDuplicateJSONFields(payload); err != nil {
		return nil, nil, framing(ReasonMalformedJSON, "json", err.Error())
	}

	object, _, err := validateExecutionEnvelopeWireShapeV2(payload)
	if err != nil {
		return nil, nil, err
	}

	planSetRaw := object["plan_set"]
	if len(planSetRaw) > limits.MaxPlanSetBytes {
		return nil, nil, framing(ReasonMessageBudgetExceeded, "execution_envelope.plan_set", "encoded bytes exceed Reader limit")
	}
	var planSetObject map[string]json.RawMessage
	if err := decodeJSONObject(planSetRaw, &planSetObject); err != nil {
		return nil, nil, framing(ReasonMalformedJSON, "execution_envelope.plan_set", err.Error())
	}
	expectedPlanDigest, err := digestJSONObjectWithoutV2(
		"execution_envelope.plan_set.plan_set_digest", "plan-set-v2", planSetRaw, "plan_set_digest",
	)
	if err != nil {
		return nil, nil, framing(ReasonPlanSetConflict, "execution_envelope.plan_set", err.Error())
	}
	var suppliedPlanDigest string
	if err := decodeJSONObject(planSetObject["plan_set_digest"], &suppliedPlanDigest); err != nil || suppliedPlanDigest != expectedPlanDigest {
		return nil, nil, framing(ReasonPlanSetConflict, "execution_envelope.plan_set.plan_set_digest", "digest does not match canonical Plan Set")
	}
	expectedPayloadDigest, err := digestJSONObjectWithoutV2(
		"execution_envelope.payload_digest", "execution-envelope-payload-v2", payload, "payload_digest",
	)
	if err != nil {
		return nil, nil, framing(ReasonPayloadDigestMismatch, "execution_envelope.payload_digest", err.Error())
	}
	var suppliedPayloadDigest string
	if err := decodeJSONObject(object["payload_digest"], &suppliedPayloadDigest); err != nil || suppliedPayloadDigest != expectedPayloadDigest {
		return nil, nil, framing(ReasonPayloadDigestMismatch, "execution_envelope.payload_digest", "digest does not match canonical Envelope")
	}

	var rawRecords []json.RawMessage
	if err := decodeJSONObject(object["records"], &rawRecords); err != nil {
		return nil, nil, framing(ReasonMalformedJSON, "execution_envelope.records", err.Error())
	}
	if len(rawRecords) > limits.MaxRecordsPerMessage {
		return nil, nil, framing(ReasonMessageBudgetExceeded, "execution_envelope.records", "record count exceeds Reader limit")
	}
	for index, record := range rawRecords {
		if len(record) > limits.MaxRecordBytes {
			return nil, nil, framing(ReasonMessageBudgetExceeded, fmt.Sprintf("execution_envelope.records[%d]", index), "encoded bytes exceed Reader limit")
		}
	}
	var rawPlans []json.RawMessage
	if err := decodeJSONObject(planSetObject["evaluation_plans"], &rawPlans); err != nil {
		return nil, nil, framing(ReasonMalformedJSON, "execution_envelope.plan_set.evaluation_plans", err.Error())
	}
	if len(rawPlans) == 0 || len(rawPlans) > limits.MaxPlansPerMessage {
		return nil, nil, framing(ReasonMessageBudgetExceeded, "execution_envelope.plan_set.evaluation_plans", "plan count outside Reader limit")
	}
	var rawSelectors []json.RawMessage
	if err := decodeJSONObject(object["selectors"], &rawSelectors); err != nil {
		return nil, nil, framing(ReasonMalformedJSON, "execution_envelope.selectors", err.Error())
	}
	if len(rawSelectors) != len(rawPlans) {
		return nil, nil, framing(ReasonSelectorOrdinalInvalid, "execution_envelope.selectors", "must contain one selector per Plan")
	}
	for index, selector := range rawSelectors {
		if len(selector) > limits.MaxSelectorBytes {
			return nil, nil, framing(ReasonMessageBudgetExceeded, fmt.Sprintf("execution_envelope.selectors[%d]", index), "encoded bytes exceed Reader limit")
		}
		var ordinal struct {
			PlanOrdinal uint32 `json:"plan_ordinal"`
		}
		if err := decodeJSONObject(selector, &ordinal); err != nil || ordinal.PlanOrdinal != uint32(index) {
			return nil, nil, framing(ReasonSelectorOrdinalInvalid, fmt.Sprintf("execution_envelope.selectors[%d].plan_ordinal", index), "ordinals must be continuous from zero")
		}
	}

	envelope, err := decodeExecutionEnvelopePartsV2(object, planSetObject, rawPlans, rawSelectors, rawRecords)
	if err != nil {
		return nil, nil, err
	}
	if envelope.PlanSet.PlanCount != uint32(len(envelope.PlanSet.EvaluationPlans)) || len(envelope.Selectors) != len(envelope.PlanSet.EvaluationPlans) {
		return nil, nil, framing(ReasonPlanSetConflict, "execution_envelope.plan_set.plan_count", "count does not match Plan Set body")
	}
	if err := validateEnvelopeFramingSemanticsV2(envelope); err != nil {
		return nil, nil, err
	}

	issues := collectValidationIssuesV2(envelope, rawPlans, rawSelectors, rawRecords, limits)
	return &FramedExecutionEnvelopeV2{Envelope: *envelope, RawPayload: bytes.Clone(payload)}, issues, nil
}

type evaluationPlanWirePartsV2 struct {
	PlanID              string                 `json:"plan_id"`
	StrategyRef         StrategyRefV2          `json:"strategy_ref"`
	InputProjection     InputProjectionV2      `json:"input_projection"`
	SourceCompatibility *SourceCompatibilityV2 `json:"source_compatibility,omitempty"`
	StrategyIR          json.RawMessage        `json:"strategy_ir"`
}

type strategyIRWirePartsV2 struct {
	Schema             Schema               `json:"schema"`
	RequiredFeatures   []string             `json:"required_features"`
	StrategyRef        StrategyRefV2        `json:"strategy_ref"`
	ExecutionSemantics ExecutionSemanticsV2 `json:"execution_semantics"`
	InputProjection    InputProjectionV2    `json:"input_projection"`
	Levels             []json.RawMessage    `json:"levels"`
}

func decodeExecutionEnvelopePartsV2(
	object, planSetObject map[string]json.RawMessage,
	rawPlans, rawSelectors, rawRecords []json.RawMessage,
) (*ExecutionEnvelopeV2, error) {
	envelope := &ExecutionEnvelopeV2{}
	fields := []struct {
		path   string
		raw    json.RawMessage
		target any
	}{
		{"schema", object["schema"], &envelope.Schema},
		{"required_features", object["required_features"], &envelope.RequiredFeatures},
		{"execution_id", object["execution_id"], &envelope.ExecutionID},
		{"message_id", object["message_id"], &envelope.MessageID},
		{"tenant_id", object["tenant_id"], &envelope.TenantID},
		{"query_group", object["query_group"], &envelope.QueryGroup},
		{"source_window", object["source_window"], &envelope.SourceWindow},
		{"query_result", object["query_result"], &envelope.QueryResult},
		{"dataset_contract", object["dataset_contract"], &envelope.DatasetContract},
		{"plan_set.plan_set_digest", planSetObject["plan_set_digest"], &envelope.PlanSet.PlanSetDigest},
		{"plan_set.plan_count", planSetObject["plan_count"], &envelope.PlanSet.PlanCount},
		{"payload_digest", object["payload_digest"], &envelope.PayloadDigest},
	}
	for _, field := range fields {
		if err := decodeJSONObject(field.raw, field.target); err != nil {
			return nil, framing(ReasonMalformedJSON, "execution_envelope."+field.path, err.Error())
		}
	}

	envelope.PlanSet.EvaluationPlans = make([]EvaluationPlanV2, len(rawPlans))
	for index := range rawPlans {
		envelope.PlanSet.EvaluationPlans[index] = decodeEvaluationPlanBestEffortV2(rawPlans[index])
	}
	envelope.Selectors = make([]PlanSelectorV2, len(rawSelectors))
	for index := range rawSelectors {
		if err := decodeJSONObject(rawSelectors[index], &envelope.Selectors[index]); err != nil {
			envelope.Selectors[index].PlanOrdinal = uint32(index)
		}
	}
	envelope.Records = make([]CanonicalRecordV2, len(rawRecords))
	for index := range rawRecords {
		envelope.Records[index] = decodeCanonicalRecordBestEffortV2(rawRecords[index])
	}
	return envelope, nil
}

func decodeEvaluationPlanBestEffortV2(raw json.RawMessage) EvaluationPlanV2 {
	var wire evaluationPlanWirePartsV2
	if decodeJSONObject(raw, &wire) != nil {
		var object map[string]json.RawMessage
		var plan EvaluationPlanV2
		if decodeJSONObject(raw, &object) == nil {
			_ = decodeJSONObject(object["plan_id"], &plan.PlanID)
		}
		return plan
	}
	plan := EvaluationPlanV2{
		PlanID: wire.PlanID, StrategyRef: wire.StrategyRef, InputProjection: wire.InputProjection,
		SourceCompatibility: wire.SourceCompatibility,
	}
	var strategyWire strategyIRWirePartsV2
	if decodeJSONObject(wire.StrategyIR, &strategyWire) != nil {
		return plan
	}
	plan.StrategyIR = StrategyIRV2{
		Schema: strategyWire.Schema, RequiredFeatures: strategyWire.RequiredFeatures, StrategyRef: strategyWire.StrategyRef,
		ExecutionSemantics: strategyWire.ExecutionSemantics, InputProjection: strategyWire.InputProjection,
		Levels: make([]LevelIRV2, len(strategyWire.Levels)),
	}
	for index := range strategyWire.Levels {
		plan.StrategyIR.Levels[index] = decodeLevelBestEffortV2(strategyWire.Levels[index])
	}
	return plan
}

func decodeLevelBestEffortV2(raw json.RawMessage) LevelIRV2 {
	var level LevelIRV2
	if decodeJSONObject(raw, &level) == nil {
		return level
	}
	var object map[string]json.RawMessage
	var definition map[string]json.RawMessage
	if decodeJSONObject(raw, &object) == nil && decodeJSONObject(object["definition"], &definition) == nil {
		_ = decodeJSONObject(definition["level_id"], &level.Definition.LevelID)
		_ = decodeJSONObject(definition["level_code"], &level.Definition.LevelCode)
	}
	return level
}

func decodeCanonicalRecordBestEffortV2(raw json.RawMessage) CanonicalRecordV2 {
	var record CanonicalRecordV2
	if decodeJSONObject(raw, &record) == nil {
		return record
	}
	var object map[string]json.RawMessage
	if decodeJSONObject(raw, &object) == nil {
		_ = decodeJSONObject(object["record_id"], &record.RecordID)
	}
	return record
}

func validateReaderLimitsV2(limits ReaderLimitsV2) error {
	values := []int{
		limits.MaxEnvelopeBytes, limits.MaxRecordsPerMessage, limits.MaxPlansPerMessage, limits.MaxLevelsPerPlan,
		limits.MaxSelectorBytes, limits.MaxRecordBytes, limits.MaxPlanSetBytes, limits.MaxContractDepth,
		limits.MaxStringBytes, limits.MaxValidationIssues,
	}
	for _, value := range values {
		if value <= 0 {
			return invalid("reader_limits", "all limits must be positive")
		}
	}
	return nil
}

func validateJSONBudgetsV2(payload []byte, maxDepth, maxStringBytes int) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch typed := token.(type) {
		case json.Delim:
			switch typed {
			case '{', '[':
				depth++
				if depth > maxDepth {
					return &jsonBudgetErrorV2{message: "contract depth exceeds Reader limit"}
				}
			case '}', ']':
				depth--
			}
		case string:
			if len(typed) > maxStringBytes {
				return &jsonBudgetErrorV2{message: "string exceeds Reader limit"}
			}
		}
	}
	if depth != 0 {
		return invalid("json", "unterminated JSON")
	}
	return nil
}

type jsonBudgetErrorV2 struct {
	message string
}

func (e *jsonBudgetErrorV2) Error() string { return e.message }

func validateExecutionEnvelopeWireShapeV2(payload []byte) (map[string]json.RawMessage, int, error) {
	required := []string{
		"schema", "required_features", "execution_id", "message_id", "tenant_id", "query_group", "source_window",
		"query_result", "dataset_contract", "plan_set", "selectors", "records", "payload_digest",
	}
	object, err := validateJSONObjectFields(payload, "execution_envelope", required, nil, true)
	if err != nil {
		return nil, 0, framing(ReasonMalformedJSON, "execution_envelope", err.Error())
	}
	minor, err := validateSchemaRawV2(object["schema"], "execution_envelope.schema", ExecutionEnvelopeSchemaV2, 2)
	if err != nil {
		return nil, 0, err
	}
	allowUnknown := minor > 0
	object, err = validateJSONObjectFields(payload, "execution_envelope", required, nil, allowUnknown)
	if err != nil {
		return nil, 0, framing(ReasonMalformedJSON, "execution_envelope", err.Error())
	}
	if err := validateRequiredFeaturesRawV2(object["required_features"], "execution_envelope.required_features"); err != nil {
		return nil, 0, err
	}
	if err := validateEnvelopeNestedShapeV2(object, allowUnknown); err != nil {
		return nil, 0, err
	}
	return object, minor, nil
}

func validateSchemaRawV2(raw json.RawMessage, field, name string, major int) (int, error) {
	object, err := validateJSONObjectFields(raw, field, []string{"name", "major", "minor"}, nil, false)
	if err != nil {
		return 0, framing(ReasonMalformedJSON, field, err.Error())
	}
	var schema Schema
	if err := decodeJSONObject(raw, &schema); err != nil {
		return 0, framing(ReasonMalformedJSON, field, err.Error())
	}
	if schema.Name != name || schema.Major != major || schema.Minor < 0 || schema.Minor > maxContractInt {
		return 0, framing(ReasonSchemaMajorUnsupported, field, "unsupported schema name or version")
	}
	_ = object
	return schema.Minor, nil
}

func validateRequiredFeaturesRawV2(raw json.RawMessage, field string) error {
	var features []string
	if err := decodeJSONObject(raw, &features); err != nil || features == nil {
		return framing(ReasonMalformedJSON, field, "must be an array")
	}
	previous := ""
	for index, feature := range features {
		if feature == "" || (index > 0 && feature <= previous) {
			return framing(ReasonRequiredFeatureUnsupported, field, "features must be non-empty, sorted and unique")
		}
		return framing(ReasonRequiredFeatureUnsupported, field, "required feature is not supported")
	}
	return nil
}

func validateEnvelopeNestedShapeV2(object map[string]json.RawMessage, allowUnknown bool) error {
	shapes := []struct {
		field    string
		raw      json.RawMessage
		required []string
		optional []string
	}{
		{"query_group", object["query_group"], []string{"key", "query_md5", "query_revision", "evaluation_time"}, nil},
		{"source_window", object["source_window"], []string{"from_time", "until_time"}, nil},
		{"query_result", object["query_result"], []string{"completeness"}, []string{"reason_code"}},
		{"dataset_contract", object["dataset_contract"], []string{"schema_digest", "normalization_digest", "identity_fields", "source_time_field", "received_time_field"}, []string{"collection_time_field"}},
		{"plan_set", object["plan_set"], []string{"plan_set_digest", "plan_count", "evaluation_plans"}, nil},
	}
	for _, shape := range shapes {
		if _, err := validateJSONObjectFields(shape.raw, "execution_envelope."+shape.field, shape.required, shape.optional, allowUnknown); err != nil {
			return framing(ReasonMalformedJSON, "execution_envelope."+shape.field, err.Error())
		}
	}
	var queryResult map[string]json.RawMessage
	if err := decodeJSONObject(object["query_result"], &queryResult); err != nil {
		return framing(ReasonMalformedJSON, "execution_envelope.query_result", err.Error())
	}
	if reason, ok := queryResult["reason_code"]; ok && bytes.Equal(bytes.TrimSpace(reason), []byte("null")) {
		return framing(ReasonMalformedJSON, "execution_envelope.query_result.reason_code", "must be omitted instead of null")
	}
	var datasetContract map[string]json.RawMessage
	if err := decodeJSONObject(object["dataset_contract"], &datasetContract); err != nil {
		return framing(ReasonMalformedJSON, "execution_envelope.dataset_contract", err.Error())
	}
	if collection, ok := datasetContract["collection_time_field"]; ok && bytes.Equal(bytes.TrimSpace(collection), []byte("null")) {
		return framing(ReasonMalformedJSON, "execution_envelope.dataset_contract.collection_time_field", "must be omitted instead of null")
	}
	var selectors []json.RawMessage
	if err := decodeJSONObject(object["selectors"], &selectors); err != nil {
		return framing(ReasonMalformedJSON, "execution_envelope.selectors", err.Error())
	}
	for index, selector := range selectors {
		selectorObject, err := validateJSONObjectFields(selector, fmt.Sprintf("execution_envelope.selectors[%d]", index), []string{"plan_ordinal", "selector"}, nil, allowUnknown)
		if err != nil {
			return framing(ReasonMalformedJSON, fmt.Sprintf("execution_envelope.selectors[%d]", index), err.Error())
		}
		_ = selectorObject
	}
	return nil
}

func validatePlanWireShapeV2(raw json.RawMessage, index int, allowUnknown bool) error {
	path := fmt.Sprintf("execution_envelope.plan_set.evaluation_plans[%d]", index)
	object, err := validateJSONObjectFields(
		raw, path,
		[]string{"plan_id", "strategy_ref", "input_projection", "strategy_ir"},
		[]string{"source_compatibility"}, allowUnknown,
	)
	if err != nil {
		return framing(ReasonMalformedJSON, path, err.Error())
	}
	if _, err := validateJSONObjectFields(object["strategy_ref"], path+".strategy_ref", []string{"tenant_id", "strategy_id", "revision"}, nil, allowUnknown); err != nil {
		return framing(ReasonMalformedJSON, path+".strategy_ref", err.Error())
	}
	if _, err := validateJSONObjectFields(object["input_projection"], path+".input_projection", []string{"value_fields", "dimension_fields", "business_identity_field", "multi_value_alignment", "data_unit", "missing_value_policy"}, nil, allowUnknown); err != nil {
		return framing(ReasonMalformedJSON, path+".input_projection", err.Error())
	}
	if source, ok := object["source_compatibility"]; ok {
		if _, err := validateJSONObjectFields(source, path+".source_compatibility", []string{"item_id"}, nil, allowUnknown); err != nil {
			return framing(ReasonMalformedJSON, path+".source_compatibility", err.Error())
		}
	}
	var typed evaluationPlanWirePartsV2
	if err := decodeJSONObject(raw, &typed); err != nil {
		return framing(ReasonMalformedJSON, path, err.Error())
	}
	return validateStrategyIRWireShapeV2(object["strategy_ir"], path+".strategy_ir")
}

func validateStrategyIRWireShapeV2(raw json.RawMessage, path string) error {
	base, err := validateJSONObjectFields(raw, path, []string{"schema", "required_features", "strategy_ref", "execution_semantics", "input_projection", "levels"}, nil, true)
	if err != nil {
		return framing(ReasonMalformedJSON, path, err.Error())
	}
	minor, err := validateSchemaRawV2(base["schema"], path+".schema", StrategyIRSchemaV2, 2)
	if err != nil {
		return err
	}
	allowUnknown := minor > 0
	base, err = validateJSONObjectFields(raw, path, []string{"schema", "required_features", "strategy_ref", "execution_semantics", "input_projection", "levels"}, nil, allowUnknown)
	if err != nil {
		return framing(ReasonMalformedJSON, path, err.Error())
	}
	if err := validateRequiredFeaturesRawV2(base["required_features"], path+".required_features"); err != nil {
		return err
	}
	if _, err := validateJSONObjectFields(base["strategy_ref"], path+".strategy_ref", []string{"tenant_id", "strategy_id", "revision"}, nil, allowUnknown); err != nil {
		return framing(ReasonMalformedJSON, path+".strategy_ref", err.Error())
	}
	_, err = validateJSONObjectFields(base["execution_semantics"], path+".execution_semantics", []string{"evaluation_scope", "query_window", "aggregation_interval", "evaluation_interval", "lateness_tolerance"}, nil, allowUnknown)
	if err != nil {
		return framing(ReasonMalformedJSON, path+".execution_semantics", err.Error())
	}
	if _, err := validateJSONObjectFields(base["input_projection"], path+".input_projection", []string{"value_fields", "dimension_fields", "business_identity_field", "multi_value_alignment", "data_unit", "missing_value_policy"}, nil, allowUnknown); err != nil {
		return framing(ReasonMalformedJSON, path+".input_projection", err.Error())
	}
	var levels []json.RawMessage
	if err := decodeJSONObject(base["levels"], &levels); err != nil {
		return framing(ReasonMalformedJSON, path+".levels", err.Error())
	}
	if levels == nil {
		return framing(ReasonMalformedJSON, path+".levels", "must be an array")
	}
	var typed strategyIRWirePartsV2
	if err := decodeJSONObject(raw, &typed); err != nil {
		return framing(ReasonMalformedJSON, path, err.Error())
	}
	return nil
}

func validateLevelWireShapeV2(raw json.RawMessage, path string, allowUnknown bool) error {
	levelObject, err := validateJSONObjectFields(raw, path, []string{"definition", "connector", "detect_plan", "trigger_plan", "recovery_plan"}, nil, allowUnknown)
	if err != nil {
		return err
	}
	definition, err := validateJSONObjectFields(levelObject["definition"], path+".definition", []string{"level_id", "priority"}, []string{"level_code"}, allowUnknown)
	if err != nil {
		return err
	}
	if code, ok := definition["level_code"]; ok && bytes.Equal(bytes.TrimSpace(code), []byte("null")) {
		return invalid(path+".definition.level_code", "must be omitted instead of null")
	}
	detectPlan, err := validateJSONObjectFields(levelObject["detect_plan"], path+".detect_plan", []string{"algorithms"}, nil, allowUnknown)
	if err != nil {
		return err
	}
	var algorithms []json.RawMessage
	if err := decodeJSONObject(detectPlan["algorithms"], &algorithms); err != nil || algorithms == nil {
		return invalid(path+".detect_plan.algorithms", "must be an array")
	}
	for algorithmIndex, algorithm := range algorithms {
		algorithmPath := fmt.Sprintf("%s.detect_plan.algorithms[%d]", path, algorithmIndex)
		algorithmObject, err := validateJSONObjectFields(algorithm, algorithmPath, []string{"type", "version", "config"}, nil, allowUnknown)
		if err != nil {
			return err
		}
		if !rawIsJSONObjectV2(algorithmObject["config"]) {
			return invalid(algorithmPath+".config", "must be a JSON object")
		}
	}
	if err := validateTypedPlanWireShapeV2(levelObject["trigger_plan"], path+".trigger_plan", allowUnknown); err != nil {
		return err
	}
	if err := validateTypedPlanWireShapeV2(levelObject["recovery_plan"], path+".recovery_plan", allowUnknown); err != nil {
		return err
	}
	var typed LevelIRV2
	return decodeJSONObject(raw, &typed)
}

func validateTypedPlanWireShapeV2(raw json.RawMessage, path string, allowUnknown bool) error {
	object, err := validateJSONObjectFields(raw, path, []string{"type", "version", "config"}, nil, allowUnknown)
	if err != nil {
		return framing(ReasonMalformedJSON, path, err.Error())
	}
	if !rawIsJSONObjectV2(object["config"]) {
		return framing(ReasonMalformedJSON, path+".config", "must be a JSON object")
	}
	return nil
}

func validateEnvelopeFramingSemanticsV2(envelope *ExecutionEnvelopeV2) error {
	if envelope.ExecutionID == "" || envelope.MessageID == "" || !isOpaqueASCII(envelope.ExecutionID) || !isOpaqueASCII(envelope.MessageID) {
		return framing(ReasonMalformedJSON, "execution_envelope.execution_id", "execution_id and message_id must be non-empty opaque ASCII")
	}
	if envelope.TenantID == "" || !utf8.ValidString(envelope.TenantID) {
		return framing(ReasonMalformedJSON, "execution_envelope.tenant_id", "must be non-empty valid UTF-8")
	}
	if envelope.QueryGroup.Key == "" || envelope.QueryGroup.QueryMD5 == "" || envelope.QueryGroup.QueryRevision == "" || envelope.QueryGroup.EvaluationTime < 0 {
		return framing(ReasonMalformedJSON, "execution_envelope.query_group", "contains invalid identity or time")
	}
	if envelope.SourceWindow.FromTime < 0 || envelope.SourceWindow.UntilTime < envelope.SourceWindow.FromTime {
		return framing(ReasonMalformedJSON, "execution_envelope.source_window", "contains invalid range")
	}
	switch envelope.QueryResult.Completeness {
	case QueryCompletenessFull:
		if envelope.QueryResult.ReasonCode != "" {
			return framing(ReasonMalformedJSON, "execution_envelope.query_result.reason_code", "must be omitted for FULL")
		}
	case QueryCompletenessPartial:
		if !ReasonAllowedForV2(envelope.QueryResult.ReasonCode, ReasonDomainQueryResult) {
			return framing(ReasonMalformedJSON, "execution_envelope.query_result.reason_code", "is required for PARTIAL")
		}
	case QueryCompletenessUnavailable:
		if !ReasonAllowedForV2(envelope.QueryResult.ReasonCode, ReasonDomainQueryResult) || len(envelope.Records) != 0 {
			return framing(ReasonMalformedJSON, "execution_envelope.query_result", "UNAVAILABLE requires a reason and empty records")
		}
	default:
		return framing(ReasonMalformedJSON, "execution_envelope.query_result.completeness", "unsupported value")
	}
	if !sha256Pattern.MatchString(envelope.DatasetContract.SchemaDigest) || !sha256Pattern.MatchString(envelope.DatasetContract.NormalizationDigest) {
		return framing(ReasonMalformedJSON, "execution_envelope.dataset_contract", "digests must be 64 lowercase hexadecimal characters")
	}
	if !sortedUniqueStringsV2(envelope.DatasetContract.IdentityFields, true) || envelope.DatasetContract.SourceTimeField == "" || envelope.DatasetContract.ReceivedTimeField == "" {
		return framing(ReasonMalformedJSON, "execution_envelope.dataset_contract", "identity fields must be sorted and source/received time fields must be non-empty")
	}
	return nil
}

func collectValidationIssuesV2(envelope *ExecutionEnvelopeV2, rawPlans, rawSelectors, rawRecords []json.RawMessage, limits ReaderLimitsV2) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	truncated := false
	appendIssue := func(issue ValidationIssue) bool {
		if truncated {
			return false
		}
		// Reserve the final slot for an explicit tail sentinel. Once the
		// ordinary issue budget is exhausted, the current object and every
		// later unverified object are terminal, never implicitly valid.
		if len(issues) < limits.MaxValidationIssues-1 {
			issues = append(issues, issue)
			return true
		}
		budgetIssue := issue
		budgetIssue.ReasonCode = ReasonValidationBudgetExceeded
		budgetIssue.UnverifiedTail = &ValidationUnverifiedTailV2{}
		if issue.Scope == ValidationScopeRecord {
			budgetIssue.UnverifiedTail.RecordFromOrdinal = issue.RecordOrdinal
		} else {
			budgetIssue.Scope = ValidationScopePlan
			budgetIssue.LevelID = nil
			budgetIssue.UnverifiedTail.PlanFromOrdinal = issue.PlanOrdinal
			firstRecord := uint32(0)
			budgetIssue.UnverifiedTail.RecordFromOrdinal = &firstRecord
		}
		issues = append(issues, budgetIssue)
		truncated = true
		return false
	}

	previousPlan := ""
planLoop:
	for index := range envelope.PlanSet.EvaluationPlans {
		planOrdinal := uint32(index)
		plan := &envelope.PlanSet.EvaluationPlans[index]
		if err := validatePlanWireShapeV2(rawPlans[index], index, envelope.Schema.Minor > 0); err != nil {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonPlanInvalid, FieldPath: "plan_set.evaluation_plans", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
			continue
		}
		rawLevels := rawPlanLevelsV2(rawPlans[index])
		planIdentityValid := canonicalDecimalPattern.MatchString(plan.PlanID) && plan.PlanID == plan.StrategyRef.StrategyID &&
			plan.StrategyRef.TenantID == envelope.TenantID && plan.StrategyRef.Revision != "" &&
			(previousPlan == "" || compareCanonicalDecimal(plan.PlanID, previousPlan) > 0)
		if !planIdentityValid {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonPlanInvalid, FieldPath: "plan_set.evaluation_plans", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
		} else {
			previousPlan = plan.PlanID
		}
		if len(plan.StrategyIR.Levels) == 0 {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonPlanInvalid, FieldPath: "strategy_ir.levels", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
			continue
		}
		if len(plan.StrategyIR.Levels) > limits.MaxLevelsPerPlan {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonPlanBudgetExceeded, FieldPath: "strategy_ir.levels", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
			continue
		}
		if plan.StrategyIR.StrategyRef != plan.StrategyRef || !equalInputProjectionV2(plan.StrategyIR.InputProjection, plan.InputProjection) ||
			plan.InputProjection.MissingValuePolicy != MissingValuePolicyRequired ||
			!sortedUniqueStringsV2(plan.InputProjection.ValueFields, false) || !sortedUniqueStringsV2(plan.InputProjection.DimensionFields, true) ||
			plan.InputProjection.BusinessIdentityField != "bk_biz_id" || plan.InputProjection.MultiValueAlignment == "" {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonProjectionInvalid, FieldPath: "input_projection", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
		}
		levelCounts := make(map[uint32]int, len(plan.StrategyIR.Levels))
		for _, level := range plan.StrategyIR.Levels {
			levelCounts[level.Definition.LevelID]++
		}
		duplicate := false
		for _, count := range levelCounts {
			if count > 1 {
				duplicate = true
				break
			}
		}
		if duplicate {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonPlanDuplicateLevelID, FieldPath: "strategy_ir.levels", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
		} else {
			previousLevel := uint32(0)
			for levelIndex := range plan.StrategyIR.Levels {
				level := &plan.StrategyIR.Levels[levelIndex]
				levelID := level.Definition.LevelID
				levelPath := fmt.Sprintf("plan_set.evaluation_plans[%d].strategy_ir.levels[%d]", index, levelIndex)
				if levelIndex >= len(rawLevels) || validateLevelWireShapeV2(rawLevels[levelIndex], levelPath, plan.StrategyIR.Schema.Minor > 0) != nil {
					if !appendIssue(ValidationIssue{Scope: ValidationScopeLevel, ReasonCode: ReasonLevelInvalid, FieldPath: levelPath, PlanOrdinal: &planOrdinal, PlanID: plan.PlanID, LevelID: &levelID}) {
						break planLoop
					}
					continue
				}
				levelValid := levelID != 0 && level.Definition.Priority != 0 && (previousLevel == 0 || levelID > previousLevel) &&
					(level.Connector == LevelConnectorAND || level.Connector == LevelConnectorOR) && len(level.DetectPlan.Algorithms) > 0
				if !levelValid {
					if !appendIssue(ValidationIssue{Scope: ValidationScopeLevel, ReasonCode: ReasonLevelInvalid, FieldPath: "strategy_ir.levels", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID, LevelID: &levelID}) {
						break planLoop
					}
				} else {
					previousLevel = levelID
				}
			}
		}
		if index < len(envelope.Selectors) && (!validSelectorV2(envelope.Selectors[index].Selector, len(envelope.Records)) || !validSelectorWireV2(rawSelectors[index])) {
			if !appendIssue(ValidationIssue{Scope: ValidationScopePlan, ReasonCode: ReasonSelectorInvalid, FieldPath: "selectors", PlanOrdinal: &planOrdinal, PlanID: plan.PlanID}) {
				break planLoop
			}
		}
		_ = rawPlans[index]
	}

	recordBodies := make(map[string]string, len(envelope.Records))
	businessID := ""
recordLoop:
	for index := range envelope.Records {
		if truncated {
			break
		}
		recordOrdinal := uint32(index)
		record := &envelope.Records[index]
		reason := ReasonRecordInvalid
		if validRecordWireShapeV2(rawRecords[index]) {
			reason = validateCanonicalRecordV2(envelope.TenantID, &envelope.DatasetContract, record)
		}
		if reason == "" {
			if businessID == "" {
				businessID = record.BusinessID
			} else if record.BusinessID != businessID {
				reason = ReasonRecordIdentityConflict
			}
		}
		if reason != "" {
			if !appendIssue(ValidationIssue{Scope: ValidationScopeRecord, ReasonCode: reason, FieldPath: "records", RecordOrdinal: &recordOrdinal, RecordID: record.RecordID}) {
				break recordLoop
			}
		}
		canonical, err := CanonicalJSONV2(rawRecords[index])
		if err == nil {
			body := string(canonical)
			if previous, exists := recordBodies[record.RecordID]; exists {
				duplicateReason := ReasonRecordInvalid
				if previous != body {
					duplicateReason = ReasonRecordIdentityConflict
				}
				if !appendIssue(ValidationIssue{Scope: ValidationScopeRecord, ReasonCode: duplicateReason, FieldPath: "records", RecordOrdinal: &recordOrdinal, RecordID: record.RecordID}) {
					break recordLoop
				}
			} else {
				recordBodies[record.RecordID] = body
			}
		}
	}

	sortValidationIssuesV2(issues)
	return issues
}

func rawPlanLevelsV2(rawPlan json.RawMessage) []json.RawMessage {
	var planObject map[string]json.RawMessage
	var strategyObject map[string]json.RawMessage
	var levels []json.RawMessage
	if decodeJSONObject(rawPlan, &planObject) != nil || decodeJSONObject(planObject["strategy_ir"], &strategyObject) != nil ||
		decodeJSONObject(strategyObject["levels"], &levels) != nil {
		return nil
	}
	return levels
}

func validSelectorWireV2(raw json.RawMessage) bool {
	object, err := validateJSONObjectFields(raw, "selector", []string{"plan_ordinal", "selector"}, nil, true)
	if err != nil {
		return false
	}
	selector, err := validateJSONObjectFields(object["selector"], "selector", []string{"kind"}, []string{"ranges", "bitmap_b64"}, false)
	if err != nil {
		return false
	}
	for _, optional := range []string{"ranges", "bitmap_b64"} {
		if value, ok := selector[optional]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	var typed PlanSelectorV2
	return decodeJSONObject(raw, &typed) == nil
}

func validRecordWireShapeV2(raw json.RawMessage) bool {
	object, err := validateJSONObjectFields(
		raw, "record",
		[]string{"record_id", "source_time", "business_id", "dimension_identity", "values", "dimensions", "received_time"},
		[]string{"collection_time"}, false,
	)
	if err != nil {
		return false
	}
	if collectionTime, ok := object["collection_time"]; ok && bytes.Equal(bytes.TrimSpace(collectionTime), []byte("null")) {
		return false
	}
	dimensionObject, err := validateJSONObjectFields(object["dimension_identity"], "dimension_identity", []string{"fields", "digest"}, nil, false)
	if err != nil {
		return false
	}
	var fields []json.RawMessage
	if err := decodeJSONObject(dimensionObject["fields"], &fields); err != nil {
		return false
	}
	for _, field := range fields {
		if _, err := validateJSONObjectFields(field, "dimension_identity.fields", []string{"name", "value"}, nil, false); err != nil {
			return false
		}
	}
	var values map[string]json.RawMessage
	var dimensions map[string]json.RawMessage
	var typed CanonicalRecordV2
	return decodeJSONObject(object["values"], &values) == nil && values != nil &&
		decodeJSONObject(object["dimensions"], &dimensions) == nil && dimensions != nil &&
		decodeJSONObject(raw, &typed) == nil
}

func rawIsJSONObjectV2(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 1 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func validateCanonicalRecordV2(tenantID string, dataset *DatasetContractV2, record *CanonicalRecordV2) string {
	if record.SourceTime < 0 || record.ReceivedTime < 0 || (record.CollectionTime != nil && *record.CollectionTime < 0) {
		return ReasonTimeInvalid
	}
	if !canonicalSignedDecimalPattern.MatchString(record.BusinessID) || record.Values == nil || record.Dimensions == nil {
		return ReasonRecordInvalid
	}
	for _, values := range []map[string]json.RawMessage{record.Values, record.Dimensions} {
		for _, value := range values {
			if !rawIsScalarOrNullV2(value) {
				return ReasonRecordInvalid
			}
		}
	}
	if dataset == nil || len(record.DimensionIdentity.Fields) != len(dataset.IdentityFields) {
		return ReasonRecordIdentityConflict
	}
	for index, identityField := range record.DimensionIdentity.Fields {
		if identityField.Name != dataset.IdentityFields[index] {
			return ReasonRecordIdentityConflict
		}
		dimensionValue, exists := record.Dimensions[identityField.Name]
		if !exists || bytes.Equal(bytes.TrimSpace(dimensionValue), []byte("null")) {
			return ReasonRecordIdentityConflict
		}
		identityCanonical, identityErr := CanonicalJSONV2(identityField.Value)
		dimensionCanonical, dimensionErr := CanonicalJSONV2(dimensionValue)
		if identityErr != nil || dimensionErr != nil || !bytes.Equal(identityCanonical, dimensionCanonical) {
			return ReasonRecordIdentityConflict
		}
	}
	dimensionDigest, err := DeriveDimensionIdentityDigestV2(tenantID, record.BusinessID, record.DimensionIdentity.Fields)
	if err != nil || dimensionDigest != record.DimensionIdentity.Digest {
		return ReasonRecordIdentityConflict
	}
	recordID, err := DeriveRecordIDV2(dimensionDigest, record.SourceTime)
	if err != nil || recordID != record.RecordID {
		return ReasonRecordIdentityConflict
	}
	return ""
}

func rawIsScalarOrNullV2(raw json.RawMessage) bool {
	canonical, err := CanonicalJSONV2(raw)
	return err == nil && len(canonical) > 0 && canonical[0] != '{' && canonical[0] != '['
}

func validSelectorV2(selector SelectorV2, recordCount int) bool {
	switch selector.Kind {
	case SelectorKindRanges:
		if selector.BitmapB64 != "" || selector.Ranges == nil {
			return false
		}
		previousEnd := uint32(0)
		for index, item := range *selector.Ranges {
			if item.Start >= item.End || item.End > uint32(recordCount) || (index > 0 && item.Start < previousEnd) {
				return false
			}
			previousEnd = item.End
		}
		return true
	case SelectorKindBitmap:
		if selector.Ranges != nil || selector.BitmapB64 == "" {
			return false
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(selector.BitmapB64)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != selector.BitmapB64 || len(decoded) != (recordCount+7)/8 {
			return false
		}
		if recordCount%8 != 0 && len(decoded) > 0 {
			// BITMAP is LSB-first: record i is bit (i%8), so unused
			// high bits in the final byte must remain zero.
			unusedMask := byte(0xff << uint(recordCount%8))
			if decoded[len(decoded)-1]&unusedMask != 0 {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func sortValidationIssuesV2(issues []ValidationIssue) {
	scopeOrder := map[ValidationScope]int{ValidationScopePlan: 0, ValidationScopeLevel: 1, ValidationScopeRecord: 2}
	sort.SliceStable(issues, func(left, right int) bool {
		l, r := issues[left], issues[right]
		if scopeOrder[l.Scope] != scopeOrder[r.Scope] {
			return scopeOrder[l.Scope] < scopeOrder[r.Scope]
		}
		if pointerValue(l.PlanOrdinal) != pointerValue(r.PlanOrdinal) {
			return pointerValue(l.PlanOrdinal) < pointerValue(r.PlanOrdinal)
		}
		if pointerValue(l.LevelID) != pointerValue(r.LevelID) {
			return pointerValue(l.LevelID) < pointerValue(r.LevelID)
		}
		if pointerValue(l.RecordOrdinal) != pointerValue(r.RecordOrdinal) {
			return pointerValue(l.RecordOrdinal) < pointerValue(r.RecordOrdinal)
		}
		if l.ReasonCode != r.ReasonCode {
			return l.ReasonCode < r.ReasonCode
		}
		return l.FieldPath < r.FieldPath
	})
}

func pointerValue(value *uint32) uint64 {
	if value == nil {
		return ^uint64(0)
	}
	return uint64(*value)
}

func compareCanonicalDecimal(left, right string) int {
	if len(left) != len(right) {
		if len(left) < len(right) {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

func equalInputProjectionV2(left, right InputProjectionV2) bool {
	leftJSON, leftErr := CanonicalJSONV2(left)
	rightJSON, rightErr := CanonicalJSONV2(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func sortedUniqueStringsV2(values []string, allowEmpty bool) bool {
	if values == nil || (!allowEmpty && len(values) == 0) {
		return false
	}
	previous := ""
	for index, value := range values {
		if value == "" || (index > 0 && value <= previous) {
			return false
		}
		previous = value
	}
	return true
}

func isOpaqueASCII(value string) bool {
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return value != ""
}
