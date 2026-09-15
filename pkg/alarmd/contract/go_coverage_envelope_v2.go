package contract

import (
	"bytes"
	"encoding/json"
	"errors"
)

const GoCoverageEnvelopeSchemaV2 = "go-chain-coverage-envelope-v2"

type GoCoverageEnvelopeV2 struct {
	Schema      string                 `json:"schema"`
	RecordType  string                 `json:"record_type"`
	EpochID     string                 `json:"validation_epoch_id"`
	Receipt     ChainCoverageReceiptV1 `json:"receipt"`
	Config      ComparisonConfigV3     `json:"comparison_config"`
	CompletedAt *int64                 `json:"progress_completed_at_unix_milli,omitempty"`
	Identity    string                 `json:"receipt_identity"`
	Digest      string                 `json:"envelope_digest"`
}

func goCoverageDigestV2(e GoCoverageEnvelopeV2) (string, error) {
	e.Digest = ""
	return DeriveCanonicalDigestV2(GoCoverageEnvelopeSchemaV2, e)
}
func ValidateGoCoverageEnvelopeV2(e *GoCoverageEnvelopeV2) error {
	if e == nil || e.Schema != GoCoverageEnvelopeSchemaV2 || e.RecordType != GoCoverageRecordType || e.EpochID == "" || e.EpochID != e.Receipt.EpochID || e.Receipt.Chain != ShadowGo {
		return errors.New("Go coverage envelope header")
	}
	if err := ValidateChainCoverageReceiptV1(&e.Receipt); err != nil {
		return err
	}
	_, digest, err := CanonicalComparisonConfigV3(e.Config)
	if err != nil || digest != e.Receipt.Context.ComparisonConfigDigest {
		return errors.New("Go coverage config mismatch")
	}
	if err = validateGoCoverageFacts(e.Receipt, e.Config.Levels, e.Config.Schedule.WindowSeconds, e.CompletedAt); err != nil {
		return err
	}
	identity, err := goCoverageScopeIdentity(e.EpochID, e.Receipt)
	if err != nil || identity != e.Identity {
		return errors.New("Go coverage identity")
	}
	digest, err = goCoverageDigestV2(*e)
	if err != nil || digest != e.Digest {
		return errors.New("Go coverage digest")
	}
	return nil
}

// EncodedGoCoverageV1 is the existing immutable queue capability, not a wire
// schema discriminator. Both official codecs retain its bounded resource owner.
func EncodeGoCoverageEnvelopeV2(e GoCoverageEnvelopeV2, maxBytes int) (EncodedGoCoverageV1, error) {
	e.Schema = GoCoverageEnvelopeSchemaV2
	e.RecordType = GoCoverageRecordType
	wire, _, err := CanonicalComparisonConfigV3(e.Config)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	var config ComparisonConfigV3
	if err = json.Unmarshal(wire, &config); err != nil {
		return EncodedGoCoverageV1{}, err
	}
	e.Config = config
	e.Identity, err = goCoverageScopeIdentity(e.EpochID, e.Receipt)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	e.Digest, err = goCoverageDigestV2(e)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	if err = ValidateGoCoverageEnvelopeV2(&e); err != nil {
		return EncodedGoCoverageV1{}, err
	}
	wire, err = CanonicalJSONV2(e)
	if err != nil {
		return EncodedGoCoverageV1{}, err
	}
	if maxBytes <= 0 || len(wire) > maxBytes {
		return EncodedGoCoverageV1{}, errors.New("Go coverage bound")
	}
	return EncodedGoCoverageV1{payload: wire}, nil
}
func DecodeGoCoverageEnvelopeV2(wire []byte, maxBytes int) (*GoCoverageEnvelopeV2, error) {
	if maxBytes <= 0 || len(wire) == 0 || len(wire) > maxBytes {
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
	var e GoCoverageEnvelopeV2
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return nil, err
	}
	config, err := DecodeComparisonConfigV3(fields["comparison_config"], maxBytes)
	if err != nil {
		return nil, err
	}
	e.Config = *config
	if err = ValidateGoCoverageEnvelopeV2(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

// GoCoverageRecord is a decoded consumer view, never an alternative wire.
// It keeps validated config bindings without manufacturing a V2 selector.
type GoCoverageRecord struct {
	EpochID                                                  string
	Receipt                                                  ChainCoverageReceiptV1
	CompletedAt                                              *int64
	Identity, Digest, FullConfigDigest, BusinessConfigDigest string
}

func DecodeGoCoverageRecord(wire []byte, maxBytes int) (*GoCoverageRecord, error) {
	if maxBytes <= 0 || len(wire) > maxBytes {
		return nil, errors.New("Go coverage bound")
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(wire, &header); err != nil {
		return nil, err
	}
	if header.Schema == GoCoverageEnvelopeSchemaV2 {
		e, err := DecodeGoCoverageEnvelopeV2(wire, maxBytes)
		if err != nil {
			return nil, err
		}
		_, business, err := CanonicalBusinessConfigV2(e.Config)
		if err != nil {
			return nil, err
		}
		return &GoCoverageRecord{e.EpochID, e.Receipt, e.CompletedAt, e.Identity, e.Digest, e.Receipt.Context.ComparisonConfigDigest, business}, nil
	}
	e, err := DecodeGoCoverageEnvelopeV1(wire, maxBytes)
	if err != nil {
		return nil, err
	}
	_, business, err := CanonicalBusinessConfigV1(e.Config)
	if err != nil {
		return nil, err
	}
	return &GoCoverageRecord{e.EpochID, e.Receipt, e.CompletedAt, e.Identity, e.Digest, e.Receipt.Context.ComparisonConfigDigest, business}, nil
}
