package contract

import (
	"bytes"
	"encoding/json"
	"errors"
)

const GoCoverageEnvelopeSchemaV1 = "go-chain-coverage-envelope-v1"
const GoCoverageRecordType = "GO_COVERAGE"

type GoCoverageEnvelopeV1 struct {
	Schema      string                 `json:"schema"`
	RecordType  string                 `json:"record_type"`
	EpochID     string                 `json:"validation_epoch_id"`
	Receipt     ChainCoverageReceiptV1 `json:"receipt"`
	Config      ComparisonConfigV2     `json:"comparison_config"`
	CompletedAt *int64                 `json:"progress_completed_at_unix_milli,omitempty"`
	Identity    string                 `json:"receipt_identity"`
	Digest      string                 `json:"envelope_digest"`
}
type EncodedGoCoverageV1 struct{ payload []byte }

func (e EncodedGoCoverageV1) CopyBytes() []byte { return append([]byte(nil), e.payload...) }
func goCoverageIdentity(e GoCoverageEnvelopeV1) (string, error) {
	return goCoverageScopeIdentity(e.EpochID, e.Receipt)
}
func goCoverageScopeIdentity(epoch string, receipt ChainCoverageReceiptV1) (string, error) {
	scope := "attempt"
	attempt := receipt.Input.QueryAttempts.ExecutionRef
	if receipt.TerminalFact {
		scope = "terminal"
		attempt = ""
	}
	return DeriveCanonicalDigestV2("go-coverage-identity-v1", struct {
		Epoch, Tenant, Business, Strategy, Scope, Attempt string
		Context                                           ShadowContextV1
	}{epoch, receipt.TenantID, receipt.BusinessID, receipt.StrategyID, scope, attempt, receipt.Context})
}
func goCoverageDigest(e GoCoverageEnvelopeV1) (string, error) {
	e.Digest = ""
	return DeriveCanonicalDigestV2(GoCoverageEnvelopeSchemaV1, e)
}
func ValidateGoCoverageEnvelopeV1(e *GoCoverageEnvelopeV1) error {
	if e == nil || e.Schema != GoCoverageEnvelopeSchemaV1 || e.RecordType != GoCoverageRecordType || e.EpochID == "" || e.EpochID != e.Receipt.EpochID || e.Receipt.Chain != ShadowGo {
		return errors.New("Go coverage envelope header")
	}
	if err := ValidateChainCoverageReceiptV1(&e.Receipt); err != nil {
		return err
	}
	_, digest, err := CanonicalComparisonConfigV2(e.Config)
	if err != nil || digest != e.Receipt.Context.ComparisonConfigDigest {
		return errors.New("Go coverage config mismatch")
	}
	if err := validateGoCoverageFacts(e.Receipt, e.Config.Levels, e.Config.Schedule.WindowSeconds, e.CompletedAt); err != nil {
		return err
	}
	identity, err := goCoverageIdentity(*e)
	if err != nil || identity != e.Identity {
		return errors.New("Go coverage identity")
	}
	digest, err = goCoverageDigest(*e)
	if err != nil || digest != e.Digest {
		return errors.New("Go coverage digest")
	}
	return nil
}

// EncodeGoCoverageEnvelopeV1 freezes both config and observed time into one
// immutable byte value. Queue/network retries use this value without reclocking.
func EncodeGoCoverageEnvelopeV1(e GoCoverageEnvelopeV1, maxBytes int) (EncodedGoCoverageV1, error) {
	e.Schema = GoCoverageEnvelopeSchemaV1
	e.RecordType = GoCoverageRecordType
	configWire, _, err := CanonicalComparisonConfigV2(e.Config)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	var normalized ComparisonConfigV2
	if err = json.Unmarshal(configWire, &normalized); err != nil {
		return EncodedGoCoverageV1{}, err
	}
	e.Config = normalized
	e.Identity, err = goCoverageIdentity(e)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	e.Digest, err = goCoverageDigest(e)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	if err = ValidateGoCoverageEnvelopeV1(&e); err != nil {
		return EncodedGoCoverageV1{}, err
	}
	wire, err := CanonicalJSONV2(e)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	if maxBytes <= 0 || len(wire) > maxBytes {
		return EncodedGoCoverageV1{}, errors.New("Go coverage bound")
	}
	return EncodedGoCoverageV1{payload: wire}, nil
}
func DecodeGoCoverageEnvelopeV1(wire []byte, maxBytes int) (*GoCoverageEnvelopeV1, error) {
	if maxBytes <= 0 || len(wire) > maxBytes {
		return nil, errors.New("Go coverage bound")
	}
	if _, err := CanonicalJSONV2(json.RawMessage(wire)); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		return nil, err
	}
	if raw, ok := fields["progress_completed_at_unix_milli"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("Go coverage unknown time must be omitted")
	}
	var e GoCoverageEnvelopeV1
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return nil, err
	}
	config, err := DecodeComparisonConfigV2(fields["comparison_config"], maxBytes)
	if err != nil {
		return nil, err
	}
	e.Config = *config
	if err := ValidateGoCoverageEnvelopeV1(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

func validateGoCoverageFacts(receipt ChainCoverageReceiptV1, levels []ShadowLevelConfigV2, windowSeconds uint32, completedAt *int64) error {
	if len(receipt.Levels) != len(levels) {
		return errors.New("Go coverage Level closure")
	}
	ids := map[uint32]bool{}
	for _, l := range levels {
		ids[l.LevelID] = true
	}
	for _, l := range receipt.Levels {
		if !ids[l.LevelID] {
			return errors.New("Go coverage Level closure")
		}
	}
	window := receipt.Input.SourceWindow
	if window.FromTime < 0 || window.UntilTime != receipt.Context.EvaluationTime || window.FromTime != window.UntilTime-int64(windowSeconds) {
		return errors.New("Go coverage source window mismatch")
	}
	if completedAt != nil && (!receipt.TerminalFact || *completedAt <= 0) {
		return errors.New("Go coverage terminal time")
	}
	if receipt.CoverageComplete && completedAt == nil {
		return errors.New("Go coverage complete requires actual terminal time")
	}
	return nil
}
