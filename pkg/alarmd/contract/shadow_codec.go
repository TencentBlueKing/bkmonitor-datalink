// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"bytes"
	"encoding/json"
)

// Decoding has no offset side effects. NativeEvent means skip-complete only;
// consumers must still honor earlier pending Audit ACKs in that partition.
// Any error requires gap Audit ACK before its offset can advance.
type ShadowResultRecordV1 struct {
	Kind        string
	Evidence    *FinalResultEvidenceV1
	Receipt     *ChainCoverageReceiptV1
	NativeEvent *TriggerEventV1
}

func (c *KnownCountV1) UnmarshalJSON(b []byte) error {
	if _, err := validateJSONObjectFields(b, "shadow.count", []string{"known"}, []string{"value"}, false); err != nil {
		return err
	}
	type plain KnownCountV1
	var value plain
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	*c = KnownCountV1(value)
	return c.Validate()
}

func decodeShadowJSON(b []byte, maxBytes int, target any) error {
	if maxBytes <= 0 || len(b) > maxBytes {
		return invalid("shadow.payload", "invalid or exceeded byte bound")
	}
	// Reuse the contract parser's duplicate-key, UTF-8 and trailing JSON checks.
	var raw json.RawMessage
	if err := decodeJSONObject(b, &raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return invalid("shadow.payload", "invalid fields or types")
	}
	return nil
}

func DecodeShadowResultRecordV1(b []byte, maxBytes int) (ShadowResultRecordV1, error) {
	if maxBytes <= 0 || len(b) > maxBytes {
		return ShadowResultRecordV1{}, invalid("shadow.payload", "invalid or exceeded byte bound")
	}
	var header struct {
		Schema     Schema `json:"schema"`
		RecordType string `json:"record_type"`
	}
	if err := decodeJSONObject(b, &header); err != nil {
		return ShadowResultRecordV1{}, err
	}
	switch {
	case header.Schema == (Schema{Name: TriggerEventSchemaV1, Major: 1}) && header.RecordType == "":
		// A record_type key (even empty) must not disguise a wrapper as native.
		var fields map[string]json.RawMessage
		if err := decodeJSONObject(b, &fields); err != nil {
			return ShadowResultRecordV1{}, err
		}
		if _, ok := fields["record_type"]; ok {
			return ShadowResultRecordV1{}, invalid("shadow.record_type", "ambiguous native envelope")
		}
		e, err := DecodeTriggerEventV1WithLimits(b, TriggerEventReaderLimitsV1{MaxPayloadBytes: maxBytes, MaxEvidenceBytes: maxBytes})
		if err != nil {
			return ShadowResultRecordV1{}, err
		}
		return ShadowResultRecordV1{Kind: ShadowNativeEvent, NativeEvent: e}, nil
	case header.Schema == (Schema{Name: FinalResultEvidenceSchemaV1, Major: 1}) && header.RecordType == ShadowFinalResult:
		var e FinalResultEvidenceV1
		if err := decodeShadowJSON(b, maxBytes, &e); err != nil {
			return ShadowResultRecordV1{}, err
		}
		if err := ValidateFinalResultEvidenceV1(&e); err != nil {
			return ShadowResultRecordV1{}, err
		}
		return ShadowResultRecordV1{Kind: ShadowFinalResult, Evidence: &e}, nil
	case header.Schema == (Schema{Name: ChainCoverageReceiptSchemaV1, Major: 1}) && header.RecordType == ShadowCoverage:
		var r ChainCoverageReceiptV1
		if err := decodeShadowJSON(b, maxBytes, &r); err != nil {
			return ShadowResultRecordV1{}, err
		}
		if err := ValidateChainCoverageReceiptV1(&r); err != nil {
			return ShadowResultRecordV1{}, err
		}
		return ShadowResultRecordV1{Kind: ShadowCoverage, Receipt: &r}, nil
	default:
		return ShadowResultRecordV1{}, invalid("shadow.header", "unknown schema or conflicting record type")
	}
}

func EncodeFinalResultEvidenceV1(e *FinalResultEvidenceV1, maxBytes int) ([]byte, error) {
	if err := ValidateFinalResultEvidenceV1(e); err != nil {
		return nil, err
	}
	return encodeShadowJSON(e, maxBytes)
}
func EncodeChainCoverageReceiptV1(r *ChainCoverageReceiptV1, maxBytes int) ([]byte, error) {
	if err := ValidateChainCoverageReceiptV1(r); err != nil {
		return nil, err
	}
	return encodeShadowJSON(r, maxBytes)
}
func EncodeValidationEpochManifestV1(m *ValidationEpochManifestV1, maxBytes int) ([]byte, error) {
	if err := ValidateValidationEpochManifestV1(m); err != nil {
		return nil, err
	}
	return encodeShadowJSON(m, maxBytes)
}
func DecodeValidationEpochManifestV1(b []byte, maxBytes int) (*ValidationEpochManifestV1, error) {
	var m ValidationEpochManifestV1
	if err := decodeShadowJSON(b, maxBytes, &m); err != nil {
		return nil, err
	}
	if err := ValidateValidationEpochManifestV1(&m); err != nil {
		return nil, err
	}
	return &m, nil
}
func encodeShadowJSON(v any, maxBytes int) ([]byte, error) {
	b, err := CanonicalJSONV2(v)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 || len(b) > maxBytes {
		return nil, invalid("shadow.payload", "invalid or exceeded byte bound")
	}
	return b, nil
}
