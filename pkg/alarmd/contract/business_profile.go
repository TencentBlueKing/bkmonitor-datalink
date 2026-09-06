package contract

import (
	"bytes"
	"encoding/json"
	"errors"
)

const BusinessAbnormalConfigV1 = "python-business-abnormal-config-v1"
const BusinessAbnormalReferenceV1 = "python-business-abnormal-v1"

// BusinessPrimaryV1 contains only shared, actually observable ABNORMAL facts.
// Input quality and chain-native identifiers are diagnostic provenance.
type BusinessPrimaryV1 struct {
	LevelID  uint32                  `json:"level_id"`
	Status   string                  `json:"status"`
	Priority uint32                  `json:"priority"`
	Values   map[string]string       `json:"normalized_values"`
	Unit     string                  `json:"unit"`
	Trigger  TriggerWindowEvidenceV1 `json:"trigger"`
}

type BusinessNativeV1 struct {
	EventID     string  `json:"event_id"`
	SnapshotKey string  `json:"snapshot_key,omitempty"`
	RecordID    string  `json:"record_id,omitempty"`
	SourceTimes []int64 `json:"anomaly_source_times,omitempty"`
}

type BusinessAbnormalV1 struct {
	Schema         string            `json:"schema"`
	Subject        ShadowSubjectV1   `json:"subject"`
	Primary        BusinessPrimaryV1 `json:"primary"`
	Config         json.RawMessage   `json:"comparison_config"`
	ConfigDigest   string            `json:"comparison_config_digest"`
	InputQuality   string            `json:"input_quality"`
	SemanticDigest string            `json:"semantic_digest"`
	Native         BusinessNativeV1  `json:"native"`
}

// CanonicalBusinessConfigV1 validates the actual full adapter output first.
// Only Recovery, which does not select scalar Threshold ABNORMAL results, is
// omitted. Existing v1/v2 canonical objects and bytes remain unchanged.
func CanonicalBusinessConfigV1(c ComparisonConfigV2) (json.RawMessage, string, error) {
	wire, _, err := CanonicalComparisonConfigV2(c)
	if err != nil {
		return nil, "", err
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(wire, &object); err != nil {
		return nil, "", err
	}
	var levels []map[string]json.RawMessage
	if err = json.Unmarshal(object["levels"], &levels); err != nil {
		return nil, "", err
	}
	for _, l := range levels {
		delete(l, "recovery")
	}
	object["levels"], _ = json.Marshal(levels)
	object["schema_version"], _ = json.Marshal(BusinessAbnormalConfigV1)
	wire, err = CanonicalJSONV2(object)
	if err != nil {
		return nil, "", err
	}
	digest, err := DeriveCanonicalDigestV2(BusinessAbnormalConfigV1, json.RawMessage(wire))
	return wire, digest, err
}

func BusinessSemanticDigestV1(r BusinessAbnormalV1) (string, error) {
	// Both chains hash exactly these shared facts, not delivery/quality/native IDs.
	return DeriveCanonicalDigestV2(BusinessAbnormalReferenceV1, struct {
		Subject      ShadowSubjectV1   `json:"subject"`
		Primary      BusinessPrimaryV1 `json:"primary"`
		ConfigDigest string            `json:"comparison_config_digest"`
	}{r.Subject, r.Primary, r.ConfigDigest})
}

// validateBusinessConfig checks the reduced domain using the existing full
// validator. The temporary Recovery value is a validator witness only: it is
// discarded before byte comparison and is never an execution/config fact.
func validateBusinessConfig(raw json.RawMessage) (ComparisonConfigV2, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ComparisonConfigV2{}, err
	}
	var version string
	if err := json.Unmarshal(object["schema_version"], &version); err != nil || version != BusinessAbnormalConfigV1 {
		return ComparisonConfigV2{}, errors.New("business config version")
	}
	var levels []map[string]json.RawMessage
	if err := json.Unmarshal(object["levels"], &levels); err != nil {
		return ComparisonConfigV2{}, err
	}
	for _, l := range levels {
		if _, exists := l["recovery"]; exists {
			return ComparisonConfigV2{}, errors.New("business config recovery field")
		}
		l["recovery"] = json.RawMessage(`{"enabled":false,"consecutive_windows":0,"mode":"CONTINUOUS_TRIGGER_MISS","input_requirement":"DATA_DRIVEN"}`)
	}
	object["levels"], _ = json.Marshal(levels)
	object["schema_version"] = json.RawMessage(`"comparison-config-v2"`)
	wire, _ := json.Marshal(object)
	var c ComparisonConfigV2
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	canonical, _, err := CanonicalBusinessConfigV1(c)
	if err != nil {
		return c, err
	}
	want, err := CanonicalJSONV2(raw)
	if err != nil || !bytes.Equal(canonical, want) {
		return c, errors.New("business config is incomplete or noncanonical")
	}
	return c, nil
}

func ValidateBusinessAbnormalV1(r *BusinessAbnormalV1) error {
	if r == nil || r.Schema != BusinessAbnormalReferenceV1 || r.Native.EventID == "" {
		return errors.New("business reference header")
	}
	if err := validateShadowSubject(r.Subject); err != nil {
		return err
	}
	c, err := validateBusinessConfig(r.Config)
	if err != nil {
		return err
	}
	digest, err := DeriveCanonicalDigestV2(BusinessAbnormalConfigV1, r.Config)
	if err != nil || digest != r.ConfigDigest {
		return errors.New("business config digest")
	}
	p := r.Primary
	if p.Status != TriggerEventAbnormal || len(p.Values) != 1 || p.Unit != c.Numeric.TargetUnit {
		return errors.New("business primary")
	}
	value, ok := p.Values["value"]
	canonical, err := NormalizeShadowDecimalV1(value)
	if !ok || err != nil || canonical != value {
		return errors.New("business numeric value")
	}
	found := false
	for _, l := range c.Levels {
		if l.LevelID != p.LevelID {
			continue
		}
		found = true
		if p.Priority != l.Priority || p.Trigger.WindowSize != l.Trigger.WindowPoints || p.Trigger.RequiredAnomalies != l.Trigger.RequiredAnomalies || p.Trigger.ObservedAnomalies < p.Trigger.RequiredAnomalies || p.Trigger.WindowEnd != r.Subject.SourceTime || p.Trigger.WindowStart < 0 || p.Trigger.WindowEnd-p.Trigger.WindowStart != int64(l.Trigger.StepSeconds)*int64(l.Trigger.WindowPoints)-1 {
			return errors.New("business trigger window")
		}
	}
	if !found {
		return errors.New("business primary level")
	}
	if r.InputQuality != "UNKNOWN" && r.InputQuality != "FULL" && r.InputQuality != "PARTIAL_ACCEPTED" {
		return errors.New("business input quality")
	}
	digest, err = BusinessSemanticDigestV1(*r)
	if err != nil || digest != r.SemanticDigest {
		return errors.New("business semantic digest")
	}
	return nil
}

type EncodedBusinessAbnormalV1 struct{ payload []byte }

// GoBusinessAbnormalV1 supplies delivery scope/provenance around the common
// reference. These coordinates never enter its comparison subject or digest.
type GoBusinessAbnormalV1 struct {
	Schema     string             `json:"schema"`
	RecordType string             `json:"record_type"`
	EpochID    string             `json:"validation_epoch_id"`
	Context    ShadowContextV1    `json:"frozen_context"`
	Reference  BusinessAbnormalV1 `json:"reference"`
}

func EncodeGoBusinessAbnormalV1(e GoBusinessAbnormalV1, limit int) (EncodedBusinessAbnormalV1, error) {
	if e.Schema != "go-business-abnormal-v1" || e.RecordType != "BUSINESS_ABNORMAL" || e.EpochID == "" {
		return EncodedBusinessAbnormalV1{}, errors.New("Go business envelope")
	}
	if err := validateShadowContext(e.Context, ShadowGo); err != nil {
		return EncodedBusinessAbnormalV1{}, err
	}
	if err := ValidateBusinessAbnormalV1(&e.Reference); err != nil {
		return EncodedBusinessAbnormalV1{}, err
	}
	wire, err := encodeShadowJSON(e, limit)
	return EncodedBusinessAbnormalV1{payload: wire}, err
}
func DecodeGoBusinessAbnormalV1(wire []byte, limit int) (*GoBusinessAbnormalV1, error) {
	if limit <= 0 || len(wire) == 0 || len(wire) > limit {
		return nil, errors.New("Go business envelope size")
	}
	if _, err := CanonicalJSONV2(json.RawMessage(wire)); err != nil {
		return nil, err
	}
	var e GoBusinessAbnormalV1
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return nil, err
	}
	var raw struct {
		Reference json.RawMessage `json:"reference"`
	}
	if err := json.Unmarshal(wire, &raw); err != nil {
		return nil, err
	}
	if _, err := DecodeBusinessAbnormalV1(raw.Reference, limit); err != nil {
		return nil, err
	}
	if _, err := EncodeGoBusinessAbnormalV1(e, limit); err != nil {
		return nil, err
	}
	return &e, nil
}

func (e EncodedBusinessAbnormalV1) CopyBytes() []byte { return append([]byte(nil), e.payload...) }
func EncodeBusinessAbnormalV1(r *BusinessAbnormalV1, limit int) (EncodedBusinessAbnormalV1, error) {
	if err := ValidateBusinessAbnormalV1(r); err != nil {
		return EncodedBusinessAbnormalV1{}, err
	}
	wire, err := encodeShadowJSON(r, limit)
	return EncodedBusinessAbnormalV1{payload: wire}, err
}
func DecodeBusinessAbnormalV1(wire []byte, limit int) (*BusinessAbnormalV1, error) {
	if limit <= 0 || len(wire) == 0 || len(wire) > limit {
		return nil, errors.New("business wire size")
	}
	if _, err := CanonicalJSONV2(json.RawMessage(wire)); err != nil {
		return nil, err
	}
	// Empty is known, absent/null is unknown; ordinary string decoding alone
	// cannot distinguish them.
	var fields struct {
		Primary map[string]json.RawMessage `json:"primary"`
	}
	if err := json.Unmarshal(wire, &fields); err != nil {
		return nil, err
	}
	for _, name := range []string{"level_id", "status", "priority", "normalized_values", "unit", "trigger"} {
		value, exists := fields.Primary[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("business primary field missing")
		}
	}
	var trigger map[string]json.RawMessage
	if err := json.Unmarshal(fields.Primary["trigger"], &trigger); err != nil {
		return nil, err
	}
	for _, name := range []string{"window_start", "window_end", "window_size", "required_anomalies", "observed_anomalies"} {
		value, exists := trigger[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("business trigger field missing")
		}
	}
	unit, ok := fields.Primary["unit"]
	if !ok || bytes.Equal(bytes.TrimSpace(unit), []byte("null")) {
		return nil, errors.New("business unit missing")
	}
	var r BusinessAbnormalV1
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return nil, err
	}
	if err := ValidateBusinessAbnormalV1(&r); err != nil {
		return nil, err
	}
	return &r, nil
}
