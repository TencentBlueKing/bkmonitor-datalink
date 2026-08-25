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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// CanonicalJSONV2 produces the shared digest representation: sorted object
// keys, preserved array order and number tokens, no insignificant whitespace,
// no HTML escaping and no trailing newline.
func CanonicalJSONV2(value any) ([]byte, error) {
	var raw []byte
	switch typed := value.(type) {
	case json.RawMessage:
		raw = bytes.Clone(typed)
	case []byte:
		raw = bytes.Clone(typed)
	default:
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return nil, invalid("canonical_json", err.Error())
		}
		raw = bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	}
	if len(raw) == 0 || !utf8.Valid(raw) || bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		return nil, invalid("canonical_json", "must contain non-empty UTF-8 JSON without BOM")
	}
	if err := validateJSONSurrogateEscapes(raw); err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSONFields(raw); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, invalid("canonical_json", err.Error())
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return nil, invalid("canonical_json", err.Error())
	}
	return restoreJSONLineSeparatorsV2(bytes.TrimSuffix(output.Bytes(), []byte{'\n'})), nil
}

// encoding/json always escapes U+2028 and U+2029. The v2 canonical contract
// keeps non-ASCII UTF-8 literal; an even preceding backslash count represents
// a literal "\\u2028" string and must remain escaped.
func restoreJSONLineSeparatorsV2(encoded []byte) []byte {
	result := make([]byte, 0, len(encoded))
	for index := 0; index < len(encoded); {
		if encoded[index] != '\\' {
			result = append(result, encoded[index])
			index++
			continue
		}
		start := index
		for index < len(encoded) && encoded[index] == '\\' {
			index++
		}
		backslashes := index - start
		if backslashes%2 == 1 && index+5 <= len(encoded) &&
			(string(encoded[index:index+5]) == "u2028" || string(encoded[index:index+5]) == "u2029") {
			result = append(result, encoded[start:index-1]...)
			if encoded[index+4] == '8' {
				result = append(result, '\xe2', '\x80', '\xa8')
			} else {
				result = append(result, '\xe2', '\x80', '\xa9')
			}
			index += 5
			continue
		}
		result = append(result, encoded[start:index]...)
	}
	return result
}

func deriveLengthPrefixedSHA256(field string, version string, values ...[]byte) (string, error) {
	all := make([][]byte, 0, len(values)+1)
	all = append(all, []byte(version))
	all = append(all, values...)
	digest := sha256.New()
	var prefix [4]byte
	for _, value := range all {
		if !utf8.Valid(value) {
			return "", invalid(field, "canonical field must contain valid UTF-8")
		}
		if uint64(len(value)) > math.MaxUint32 {
			return "", invalid(field, "canonical field exceeds uint32 length")
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(value)))
		_, _ = digest.Write(prefix[:])
		_, _ = digest.Write(value)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func digestCanonicalV2(field, domain string, value any) (string, error) {
	canonical, err := CanonicalJSONV2(value)
	if err != nil {
		return "", invalid(field, err.Error())
	}
	return deriveLengthPrefixedSHA256(field, domain, canonical)
}

// DeriveCanonicalDigestV2 is the shared building block for module-owned,
// versioned semantic objects. Callers own the typed input; M0 owns canonical
// encoding, domain separation and SHA-256 framing.
func DeriveCanonicalDigestV2(domain string, value any) (string, error) {
	if !isOpaqueASCII(domain) {
		return "", invalid("canonical_digest.domain", "must be non-empty opaque ASCII")
	}
	return digestCanonicalV2("canonical_digest", domain, value)
}

func digestJSONObjectWithoutV2(field, domain string, payload []byte, omitted string) (string, error) {
	var object map[string]json.RawMessage
	if err := decodeJSONObject(payload, &object); err != nil {
		return "", invalid(field, err.Error())
	}
	if object == nil {
		return "", invalid(field, "must be a JSON object")
	}
	delete(object, omitted)
	return digestCanonicalV2(field, domain, object)
}

func DeriveDimensionIdentityDigestV2(tenantID, businessID string, fields []DimensionFieldV2) (string, error) {
	if tenantID == "" || !utf8.ValidString(tenantID) {
		return "", invalid("dimension_identity.tenant_id", "must be non-empty valid UTF-8")
	}
	if !canonicalDecimalPattern.MatchString(businessID) {
		return "", invalid("dimension_identity.business_id", "must use canonical decimal form")
	}
	if fields == nil {
		return "", invalid("dimension_identity.fields", "must be an array")
	}
	previous := ""
	for index, dimension := range fields {
		if dimension.Name == "" || !utf8.ValidString(dimension.Name) || (index > 0 && dimension.Name <= previous) {
			return "", invalid("dimension_identity.fields", "names must be non-empty, sorted and unique")
		}
		canonical, err := CanonicalJSONV2(dimension.Value)
		if err != nil || bytes.Equal(canonical, []byte("null")) || len(canonical) == 0 || canonical[0] == '{' || canonical[0] == '[' {
			return "", invalid("dimension_identity.fields.value", "must be a non-null scalar JSON value")
		}
		previous = dimension.Name
	}
	canonicalFields, err := CanonicalJSONV2(fields)
	if err != nil {
		return "", invalid("dimension_identity.fields", err.Error())
	}
	return deriveLengthPrefixedSHA256(
		"dimension_identity.digest", "dimension-identity-v1",
		[]byte(tenantID), []byte(businessID), canonicalFields,
	)
}

func DeriveRecordIDV2(dimensionIdentityDigest string, sourceTime int64) (string, error) {
	if !sha256Pattern.MatchString(dimensionIdentityDigest) {
		return "", invalid("record_id.dimension_identity_digest", "must be 64 lowercase hexadecimal characters")
	}
	if sourceTime < 0 {
		return "", invalid("record_id.source_time", "must be non-negative")
	}
	return deriveLengthPrefixedSHA256(
		"record_id", "record-id-v2", []byte(dimensionIdentityDigest), []byte(strconv.FormatInt(sourceTime, 10)),
	)
}

func DeriveQueryGroupKafkaKeyV2(tenantID, queryGroupKey string) ([]byte, error) {
	if tenantID == "" || queryGroupKey == "" {
		return nil, invalid("query_group.kafka_key", "tenant and Query Group key must be non-empty")
	}
	digest, err := deriveLengthPrefixedSHA256(
		"query_group.kafka_key", "query-group-kafka-key-v1", []byte(tenantID), []byte(queryGroupKey),
	)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(digest)
}

func DerivePlanSetDigestV2(planSet PlanSetV2) (string, error) {
	planSet.PlanSetDigest = ""
	payload, err := CanonicalJSONV2(planSet)
	if err != nil {
		return "", err
	}
	return digestJSONObjectWithoutV2("plan_set.plan_set_digest", "plan-set-v2", payload, "plan_set_digest")
}

func DeriveExecutionEnvelopePayloadDigestV2(envelope ExecutionEnvelopeV2) (string, error) {
	envelope.PayloadDigest = ""
	payload, err := CanonicalJSONV2(envelope)
	if err != nil {
		return "", err
	}
	return digestJSONObjectWithoutV2("execution_envelope.payload_digest", "execution-envelope-payload-v2", payload, "payload_digest")
}

func DeriveStateCompatibilityHashV1(input StateCompatibilityInputV1) (string, error) {
	if input.StateSchemaVersion == "" || input.CodecSemanticsVersion == "" ||
		input.SourceTimeSemanticsVersion == "" || input.HistoryCellSemanticsVersion == "" {
		return "", invalid("state_compatibility", "semantic versions must be non-empty")
	}
	if !sha256Pattern.MatchString(input.IdentitySchemaDigest) {
		return "", invalid("state_compatibility.identity_schema_digest", "must be 64 lowercase hexadecimal characters")
	}
	if input.EvaluationScope != EvaluationScopeSeries && input.EvaluationScope != EvaluationScopeCrossSeries {
		return "", invalid("state_compatibility.evaluation_scope", "unsupported scope")
	}
	if input.AggregationInterval == 0 || input.EvaluationInterval == 0 {
		return "", invalid("state_compatibility", "intervals must be positive")
	}
	return digestCanonicalV2("state_compatibility_hash", "state-compatibility-v1", input)
}

func DeriveLevelDetectFingerprintV1(input LevelDetectSemanticV1) (string, error) {
	if input.LevelID == 0 {
		return "", invalid("level_detect_fingerprint.level_id", "must be positive")
	}
	if !sha256Pattern.MatchString(input.ProjectionDigest) || !sha256Pattern.MatchString(input.DetectorSemanticDigest) {
		return "", invalid("level_detect_fingerprint", "semantic digests must be 64 lowercase hexadecimal characters")
	}
	return digestCanonicalV2("level_detect_fingerprint", "level-detect-fingerprint-v1", input)
}

func DeriveLevelTriggerFingerprintV1(levelDetectFingerprint string, triggerPlan, recoveryPlan TypedPlanV1) (string, error) {
	if !sha256Pattern.MatchString(levelDetectFingerprint) {
		return "", invalid("level_trigger_fingerprint", "detect fingerprint must be 64 lowercase hexadecimal characters")
	}
	return digestCanonicalV2("level_trigger_fingerprint", "level-trigger-fingerprint-v1", struct {
		LevelDetectFingerprint string      `json:"level_detect_fingerprint"`
		TriggerPlan            TypedPlanV1 `json:"trigger_plan"`
		RecoveryPlan           TypedPlanV1 `json:"recovery_plan"`
	}{levelDetectFingerprint, triggerPlan, recoveryPlan})
}

func DerivePlanFingerprintV1(domain string, fingerprints map[uint32]string) (string, error) {
	if domain != DetectPlanFingerprintDomainV1 && domain != TriggerStateFingerprintDomainV1 {
		return "", invalid("plan_fingerprint", "unsupported domain")
	}
	type entry struct {
		LevelID     uint32 `json:"level_id"`
		Fingerprint string `json:"fingerprint"`
	}
	levels := make([]uint32, 0, len(fingerprints))
	for levelID := range fingerprints {
		levels = append(levels, levelID)
	}
	sort.Slice(levels, func(left, right int) bool { return levels[left] < levels[right] })
	entries := make([]entry, 0, len(levels))
	for _, levelID := range levels {
		fingerprint := fingerprints[levelID]
		if levelID == 0 || !sha256Pattern.MatchString(fingerprint) {
			return "", invalid("plan_fingerprint", fmt.Sprintf("invalid Level %d fingerprint", levelID))
		}
		entries = append(entries, entry{LevelID: levelID, Fingerprint: fingerprint})
	}
	if len(entries) == 0 {
		return "", invalid("plan_fingerprint", "must contain at least one Level")
	}
	return digestCanonicalV2("plan_fingerprint", domain, entries)
}
