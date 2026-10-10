// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package linkdoutput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"
	"time"
	"unicode/utf8"
)

// checkStandardPayload is the consumer's acceptance of a standard event,
// transcribed rule by rule from pkg/linkd at feat/linkd-dev 949f77b2:
// internal/cleaner/raw_event.go (StandardCleaner.Clean,
// validateAdditionalDimensions, validateJSONObject), internal/domain/value.go
// (Scalar.UnmarshalJSON, DimensionMap.Validate), internal/domain/values.go
// (EventValues.UnmarshalJSON), internal/domain/evaluation.go
// (ValidateEvaluations), internal/domain/event.go (Event.validate lengths),
// and the three labels internal/lifecycle/enrich requires as positive
// integers.
//
// It exists because there is no consumer in the environment the tests run
// in, and the consumer refuses a message whole and quietly: a rule this side
// breaks is an alert that never appears. Every golden message in this package
// passes through it. The rules are copied, not shared, so a change on either
// side shows up here as a failing test rather than as a silent disagreement.
func checkStandardPayload(payload []byte) error {
	if err := checkJSONObject(payload); err != nil {
		return err
	}
	var message struct {
		TenantID    string                     `json:"bk_tenant_id"`
		EventID     string                     `json:"event_id"`
		AlertID     string                     `json:"alert_id"`
		Title       string                     `json:"title"`
		Content     string                     `json:"content"`
		Values      map[string]json.RawMessage `json:"values"`
		Evaluations []struct {
			Severity     string `json:"severity"`
			Action       string `json:"action"`
			ActionReason string `json:"action_reason"`
		} `json:"evaluations"`
		Dimensions map[string]json.RawMessage `json:"dimensions"`
		Subject    struct {
			System, Type, ID, Name string
		} `json:"subject"`
		OccurredAt string                     `json:"occurred_at"`
		ProducedAt string                     `json:"produced_at"`
		Labels     map[string]json.RawMessage `json:"labels"`
		ExtraData  map[string]json.RawMessage `json:"extra_data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return fmt.Errorf("decode standard payload: %w", err)
	}
	// Evaluations: 1..32, valid actions, severity 1..32 bytes, unique after
	// mapping (the identity mapping is assumed), reason at most 256 bytes.
	if len(message.Evaluations) == 0 || len(message.Evaluations) > 32 {
		return errors.New("standard evaluations must contain between 1 and 32 items")
	}
	seen := map[string]bool{}
	for _, evaluation := range message.Evaluations {
		switch evaluation.Action {
		case "triggered", "resolved", "closed":
		default:
			return fmt.Errorf("standard evaluation action %q is invalid", evaluation.Action)
		}
		if size := len(evaluation.Severity); size < 1 || size > 32 {
			return fmt.Errorf("severity %q length out of range", evaluation.Severity)
		}
		if seen[evaluation.Severity] {
			return errors.New("evaluations contain duplicate severity")
		}
		seen[evaluation.Severity] = true
		if len(evaluation.ActionReason) > 256 {
			return errors.New("action_reason exceeds 256 bytes")
		}
	}
	for name, dimensions := range map[string]map[string]json.RawMessage{"dimensions": message.Dimensions, "labels": message.Labels} {
		if err := checkDimensionMap(dimensions); err != nil {
			return fmt.Errorf("standard %s: %w", name, err)
		}
	}
	if raw, present := message.ExtraData["additional_dimensions"]; present {
		if err := checkJSONObject(raw); err != nil {
			return fmt.Errorf("standard extra_data.additional_dimensions: %w", err)
		}
		var additional map[string]json.RawMessage
		if err := json.Unmarshal(raw, &additional); err != nil {
			return fmt.Errorf("standard extra_data.additional_dimensions: %w", err)
		}
		if err := checkDimensionMap(additional); err != nil {
			return fmt.Errorf("standard extra_data.additional_dimensions: %w", err)
		}
		for key := range additional {
			if _, exists := message.Dimensions[key]; exists {
				return fmt.Errorf("standard dimensions and extra_data.additional_dimensions duplicate key %q", key)
			}
		}
	}
	// Values: at most 256 finite numbers under 1..256-byte keys; null, text,
	// booleans and nested values are refused.
	if len(message.Values) > 256 {
		return errors.New("values must not exceed 256 fields")
	}
	for key, raw := range message.Values {
		if size := len(key); size < 1 || size > 256 {
			return fmt.Errorf("values key %q length out of range", key)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("values must contain only finite numbers")
		}
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return errors.New("values must contain only finite numbers")
		}
	}
	// Event field lengths, as the domain validates them after the factory.
	for _, field := range []struct {
		name  string
		value string
		min   int
		max   int
	}{
		{"bk_tenant_id", message.TenantID, 1, 64},
		{"title", message.Title, 0, 256},
		{"content", message.Content, 0, 1 << 20},
		{"subject_system", message.Subject.System, 0, 32},
		{"subject_type", message.Subject.Type, 0, 128},
		{"subject_id", message.Subject.ID, 0, 256},
		{"subject_name", message.Subject.Name, 0, 256},
		{"source_event_id", message.EventID, 0, 256},
		{"source_alert_id", message.AlertID, 0, 256},
	} {
		if size := len(field.value); size < field.min || size > field.max || !utf8.ValidString(field.value) {
			return fmt.Errorf("%s length %d out of [%d, %d]", field.name, size, field.min, field.max)
		}
	}
	for name, value := range map[string]string{"occurred_at": message.OccurredAt, "produced_at": message.ProducedAt} {
		if value == "" {
			// The factory would substitute received_at; this side always
			// states both, so an empty one is a defect here.
			return fmt.Errorf("%s is empty", name)
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	// The three labels the enrichment processors require as positive
	// integers; a string, a fraction, zero or a negative is invalid_field.
	for _, name := range []string{"strategy_id", "strategy_version", "bk_biz_id"} {
		raw, present := message.Labels[name]
		if !present {
			return fmt.Errorf("labels.%s missing_field", name)
		}
		var scalar any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&scalar); err != nil {
			return fmt.Errorf("labels.%s invalid_field: %w", name, err)
		}
		number, isNumber := scalar.(json.Number)
		if !isNumber {
			return fmt.Errorf("labels.%s invalid_field: %s is not a number", name, raw)
		}
		value, err := number.Int64()
		if err != nil || value <= 0 {
			return fmt.Errorf("labels.%s invalid_field: %s", name, raw)
		}
	}
	return nil
}

// checkDimensionMap is DimensionMap decoding: every value a string, a finite
// number or a boolean; a null, an object or an array refuses the map.
func checkDimensionMap(dimensions map[string]json.RawMessage) error {
	for key, raw := range dimensions {
		if key == "" {
			return errors.New("dimension key must not be empty")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode scalar: %w", err)
		}
		switch typed := value.(type) {
		case string, bool:
		case json.Number:
			number, err := typed.Float64()
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return fmt.Errorf("dimension %q: scalar number must be finite", key)
			}
		default:
			return fmt.Errorf("dimension %q: scalar must be string, finite number, or boolean", key)
		}
	}
	return nil
}

// checkJSONObject is validateJSONObject: one JSON object, no duplicate keys
// at any depth, nothing trailing.
func checkJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode source JSON: %w", err)
	}
	if token != json.Delim('{') {
		return errors.New("source payload must be a JSON object")
	}
	if err := checkObjectBody(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("source payload contains trailing JSON data")
		}
		return err
	}
	return nil
}

func checkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return checkObjectBody(decoder)
	case '[':
		for decoder.More() {
			if err := checkJSONValue(decoder); err != nil {
				return err
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array closing delimiter")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func checkObjectBody(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("JSON object key must be a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("source payload contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		if err := checkJSONValue(decoder); err != nil {
			return err
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return errors.New("invalid JSON object closing delimiter")
	}
	return nil
}

// The transcription has to refuse what the consumer refuses, or every golden
// message passing through it proves nothing. Each case breaks one rule the
// converter is relied on to keep.
func TestTheTranscribedAcceptanceRefusesWhatTheConsumerRefuses(t *testing.T) {
	accepted := convertRaw(t, decision(nil)).Payload
	if err := checkStandardPayload(accepted); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	for name, broken := range map[string][]byte{
		"no evaluations":                bytes.Replace(accepted, []byte(`"evaluations":[{"severity":"warning","action":"triggered","action_reason":""}]`), []byte(`"evaluations":[]`), 1),
		"an unknown action":             bytes.Replace(accepted, []byte(`"action":"triggered"`), []byte(`"action":"recovered"`), 1),
		"a null dimension":              bytes.Replace(accepted, []byte(`"device":"sda"`), []byte(`"device":null`), 1),
		"a string label":                bytes.Replace(accepted, []byte(`"strategy_id":123`), []byte(`"strategy_id":"123"`), 1),
		"a zero label":                  bytes.Replace(accepted, []byte(`"strategy_version":7`), []byte(`"strategy_version":0`), 1),
		"a missing label":               bytes.Replace(accepted, []byte(`"bk_biz_id":2`), []byte(`"biz":2`), 1),
		"a textual value":               bytes.Replace(accepted, []byte(`"values":{"value":92.5}`), []byte(`"values":{"value":"92.5"}`), 1),
		"a duplicated additional key":   bytes.Replace(accepted, []byte(`"extra_data":{`), []byte(`"extra_data":{"additional_dimensions":{"device":"sdb"},`), 1),
		"a duplicate severity":          bytes.Replace(accepted, []byte(`"evaluations":[{"severity":"warning","action":"triggered","action_reason":""}]`), []byte(`"evaluations":[{"severity":"warning","action":"triggered","action_reason":""},{"severity":"warning","action":"resolved","action_reason":""}]`), 1),
		"a top-level duplicate key":     bytes.Replace(accepted, []byte(`"title":`), []byte(`"title":"x","title":`), 1),
		"trailing data":                 append(append([]byte{}, accepted...), '{', '}'),
		"an unreadable occurred_at":     bytes.Replace(accepted, []byte(`"occurred_at":"2025-09-01T00:00:00Z"`), []byte(`"occurred_at":"1756684800"`), 1),
		"an empty tenant":               bytes.Replace(accepted, []byte(`"bk_tenant_id":"tenant-a"`), []byte(`"bk_tenant_id":""`), 1),
		"a title over the length bound": bytes.Replace(accepted, []byte(`"title":"Strategy 123 level 2 triggered"`), append(append([]byte(`"title":"`), bytes.Repeat([]byte("t"), 257)...), '"'), 1),
	} {
		if bytes.Equal(broken, accepted) {
			t.Fatalf("%s: the fixture was not changed; the rule is not being exercised", name)
		}
		if err := checkStandardPayload(broken); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
