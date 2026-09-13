// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package shadow

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type OfflineInput struct {
	Manifest contract.ValidationEpochManifestV1 `json:"manifest"`
	GoEvents []GoEvidenceInput                  `json:"go_events"`
	Receipts []contract.ChainCoverageReceiptV1  `json:"receipts"`
}

// OfflineOutput deliberately has no G5 verdict. Codec/projection verification
// does not prove Kafka coverage, Audit ACKs, Python equivalence or an Epoch.
type OfflineOutput struct {
	Status             string                             `json:"status"`
	Evidence           []*contract.FinalResultEvidenceV1  `json:"evidence"`
	Receipts           []*contract.ChainCoverageReceiptV1 `json:"receipts"`
	IncompleteReceipts int                                `json:"incomplete_receipts"`
}

func DecodeOfflineInput(payload []byte, maxBytes int) (OfflineInput, error) {
	if maxBytes <= 0 || len(payload) > maxBytes {
		return OfflineInput{}, errors.New("alarmd shadow: offline input exceeds bound")
	}
	// Canonical parsing rejects duplicate keys and malformed/trailing JSON.
	if _, err := contract.CanonicalJSONV2(json.RawMessage(payload)); err != nil {
		return OfflineInput{}, err
	}
	var in OfflineInput
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return OfflineInput{}, errors.New("alarmd shadow: invalid offline fields")
	}
	return in, nil
}

func ValidateOfflineInput(in OfflineInput, maxBytes int) (OfflineOutput, error) {
	if err := contract.ValidateValidationEpochManifestV1(&in.Manifest); err != nil {
		return OfflineOutput{}, err
	}
	if in.GoEvents == nil || in.Receipts == nil {
		return OfflineOutput{}, errors.New("alarmd shadow: explicit input arrays required")
	}
	out := OfflineOutput{Status: "CONTRACTS_VALID_NOT_EPOCH_VERDICT", Evidence: []*contract.FinalResultEvidenceV1{}, Receipts: []*contract.ChainCoverageReceiptV1{}}
	for _, input := range in.GoEvents {
		if input.EpochID != in.Manifest.EpochID || input.IdentityVersion != in.Manifest.IdentityVersion || input.Event.TenantID != in.Manifest.Target.TenantID || input.Event.BusinessID != in.Manifest.Target.BusinessID || input.Context.EvaluationTime < in.Manifest.EligibleFrom || input.Context.EvaluationTime >= in.Manifest.ExpectedEnd {
			return OfflineOutput{}, errors.New("alarmd shadow: evidence is outside frozen Epoch")
		}
		e, err := BuildGoFinalEvidence(input, maxBytes)
		if err != nil {
			return OfflineOutput{}, err
		}
		out.Evidence = append(out.Evidence, e)
	}
	for _, input := range in.Receipts {
		if input.EpochID != in.Manifest.EpochID || input.TenantID != in.Manifest.Target.TenantID || input.BusinessID != in.Manifest.Target.BusinessID || input.Context.EvaluationTime < in.Manifest.EligibleFrom || input.Context.EvaluationTime >= in.Manifest.ExpectedEnd {
			return OfflineOutput{}, errors.New("alarmd shadow: receipt is outside frozen Epoch")
		}
		r, err := BuildCoverageReceipt(input, maxBytes)
		if err != nil {
			return OfflineOutput{}, err
		}
		out.Receipts = append(out.Receipts, r)
		if !r.CoverageComplete {
			out.IncompleteReceipts++
		}
	}
	return out, nil
}
