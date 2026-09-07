// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type ProviderRouteRef string
type PhysicalQueryDigest string
type ProviderResultRef string
type ProviderKind string
type DownSampleRange string

const (
	ProviderUQ     ProviderKind    = "uq"
	DownSampleNone DownSampleRange = ""
)

// QueryWindow is always a half-open source-time range [Start, End).
type QueryWindow struct {
	Start int64
	End   int64
}

func (window QueryWindow) Validate() error {
	if window.Start >= window.End {
		return errors.New("alarmd execution: query window must be a non-empty half-open range")
	}
	return nil
}

// PhysicalQuerySpec is the provider-neutral normalized request compiled by
// Access. The UQ provider owns the small /query/ts wire DTO conversion.
type PhysicalQuerySpec struct {
	Digest          PhysicalQueryDigest
	PlanFacts       QueryPlanFacts
	LogicalWindow   QueryWindow
	ProviderRange   QueryWindow
	AcceptedRange   QueryWindow
	RequiredColumns []string
}

func (spec PhysicalQuerySpec) Validate() error {
	if spec.Digest == "" {
		return errors.New("alarmd execution: complete physical query identity is required")
	}
	if err := spec.PlanFacts.Validate(); err != nil {
		return err
	}
	for _, window := range []QueryWindow{spec.LogicalWindow, spec.ProviderRange, spec.AcceptedRange} {
		if err := window.Validate(); err != nil {
			return err
		}
	}
	if spec.ProviderRange.Start > spec.LogicalWindow.Start || spec.ProviderRange.End < spec.LogicalWindow.End ||
		spec.AcceptedRange.Start < spec.ProviderRange.Start || spec.AcceptedRange.End > spec.ProviderRange.End {
		return errors.New("alarmd execution: physical query ranges are inconsistent")
	}
	if len(spec.RequiredColumns) == 0 {
		return errors.New("alarmd execution: complete normalized physical query is required")
	}
	for _, column := range spec.RequiredColumns {
		if column == "" {
			return errors.New("alarmd execution: empty required column")
		}
	}
	expected, err := DerivePhysicalQueryDigest(spec)
	if err != nil {
		return err
	}
	if spec.Digest != expected {
		return errors.New("alarmd execution: physical query digest does not match normalized request fields")
	}
	return nil
}

func DerivePhysicalQueryDigest(spec PhysicalQuerySpec) (PhysicalQueryDigest, error) {
	if err := spec.PlanFacts.Validate(); err != nil {
		return "", errors.New("alarmd execution: cannot derive incomplete physical query identity")
	}
	spec.Digest = ""
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-physical-query-v1", spec)
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive physical query digest: %w", err)
	}
	return PhysicalQueryDigest(digest), nil
}

func BuildPhysicalQuerySpec(spec PhysicalQuerySpec) (PhysicalQuerySpec, error) {
	if spec.Digest != "" {
		return PhysicalQuerySpec{}, errors.New("alarmd execution: physical query builder owns the digest")
	}
	digest, err := DerivePhysicalQueryDigest(spec)
	if err != nil {
		return PhysicalQuerySpec{}, err
	}
	spec.Digest = digest
	if err := spec.Validate(); err != nil {
		return PhysicalQuerySpec{}, err
	}
	return spec, nil
}

type RecoveryPermit struct {
	PermitID           string
	Slot               SlotIdentity
	Operation          Operation
	ExpiresAtUnixMilli int64
}

type QueryAttempt struct {
	Spec              PhysicalQuerySpec
	Slot              SlotIdentity
	Operation         Operation
	AttemptNo         uint32
	DeadlineUnixMilli int64
	RecoveryPermit    *RecoveryPermit
}

func (attempt QueryAttempt) Validate() error {
	if err := attempt.Spec.Validate(); err != nil {
		return err
	}
	if attempt.Slot.QueryGroup == "" || attempt.Slot.EvaluationTime <= 0 {
		return errors.New("alarmd execution: complete query attempt Slot is required")
	}
	if err := attempt.Operation.Validate(); err != nil {
		return err
	}
	if attempt.AttemptNo == 0 || attempt.DeadlineUnixMilli <= 0 {
		return errors.New("alarmd execution: complete query attempt execution facts are required")
	}
	if attempt.Operation == OperationNormal {
		if attempt.RecoveryPermit != nil {
			return errors.New("alarmd execution: normal query must not consume a recovery permit")
		}
	} else if attempt.RecoveryPermit == nil || attempt.RecoveryPermit.PermitID == "" ||
		attempt.RecoveryPermit.Slot != attempt.Slot || attempt.RecoveryPermit.Operation != attempt.Operation ||
		attempt.RecoveryPermit.ExpiresAtUnixMilli < attempt.DeadlineUnixMilli {
		return errors.New("alarmd execution: retry, replay and probe require a matching live recovery permit")
	}
	return nil
}

type RouteAttemptResult string

const (
	RouteAttemptSucceeded RouteAttemptResult = "SUCCEEDED"
	RouteAttemptFailed    RouteAttemptResult = "FAILED"
)

// RouteAttemptFact records one provider attempt. Detail is a bounded,
// machine-readable failure detail ("http_status=503" or
// "transport=connection_refused"); it never carries response bodies, URLs or
// free error text, and it does not change completeness semantics.
type RouteAttemptFact struct {
	AttemptNo  uint32
	Endpoint   string
	Result     RouteAttemptResult
	ReasonCode ReasonCode
	Detail     string
}

const (
	RouteDetailKindHTTPStatus = "http_status"
	RouteDetailKindTransport  = "transport"
	RouteDetailKindResponse   = "response"

	ResponseFailureIsPartialMissing = "is_partial_missing"

	TransportFailureTimeout           = "timeout"
	TransportFailureConnectionRefused = "connection_refused"
	TransportFailureConnectionReset   = "connection_reset"
	TransportFailureDNS               = "dns"
	TransportFailureTLS               = "tls"
	TransportFailureEOF               = "eof"
	TransportFailureOther             = "other"
)

// HTTPStatusRouteDetail encodes a non-success HTTP status as attempt detail.
func HTTPStatusRouteDetail(status int) string {
	if status <= 0 || status > 999 {
		return RouteDetailKindHTTPStatus + "=other"
	}
	return RouteDetailKindHTTPStatus + "=" + strconv.Itoa(status)
}

// TransportRouteDetail encodes a classified transport failure as attempt detail.
// Unknown classes collapse to "other" so the value stays a bounded enum.
func TransportRouteDetail(class string) string {
	switch class {
	case TransportFailureTimeout, TransportFailureConnectionRefused, TransportFailureConnectionReset,
		TransportFailureDNS, TransportFailureTLS, TransportFailureEOF:
		return RouteDetailKindTransport + "=" + class
	default:
		return RouteDetailKindTransport + "=" + TransportFailureOther
	}
}

// ResponseRouteDetail encodes a 200 response that violated the wire contract
// (for example a missing is_partial flag) as attempt detail.
func ResponseRouteDetail(class string) string {
	switch class {
	case ResponseFailureIsPartialMissing:
		return RouteDetailKindResponse + "=" + class
	default:
		return RouteDetailKindResponse + "=other"
	}
}

// RouteDetailKind returns the detail kind prefix ("http_status", "transport"
// or "response") or "" when the detail is empty or not one of the known shapes.
func RouteDetailKind(detail string) string {
	for _, kind := range []string{RouteDetailKindHTTPStatus, RouteDetailKindTransport, RouteDetailKindResponse} {
		if strings.HasPrefix(detail, kind+"=") {
			return kind
		}
	}
	return ""
}

type ProviderRouteFacts struct {
	ProviderRouteRef ProviderRouteRef
	ResultTableIDs   []string
	Attempts         []RouteAttemptFact
}

// ProviderQualityFact is a physical query fact. It deliberately has no
// Plan/Level impact scope; Access projects it onto a consumer binding.
type ProviderQualityFact struct {
	ReasonCode     ReasonCode
	RecordID       string
	SourceTime     int64
	SeriesIdentity SeriesIdentityDigest
}

type ProviderRecordTerminal struct {
	ReasonCode     ReasonCode
	RecordID       string
	SourceTime     int64
	SeriesIdentity SeriesIdentityDigest
}

// InputQualityFact is the consumer-scoped projection produced by Access.
type InputQualityFact struct {
	ReasonCode     ReasonCode
	ImpactScope    ImpactScope
	RecordID       string
	SourceTime     int64
	SeriesIdentity SeriesIdentityDigest
}

type ProviderStats struct {
	Series       uint64
	Records      uint64
	Bytes        uint64
	QueryMillis  uint64
	DecodeMillis uint64
}

// PartialEvidence is a physical Provider fact. It does not itself authorize a
// business result; Evaluation must match it with a compiled proof reference.
type PartialEvidence struct {
	Kind                  string
	Version               uint32
	EvidenceDigest        string
	OmissionOnly          bool
	ReturnedRecordsStable bool
}

func (evidence *PartialEvidence) Validate() error {
	if evidence == nil || evidence.Kind != PartialEvidenceOmissionStable || evidence.Version == 0 ||
		evidence.EvidenceDigest == "" || !evidence.OmissionOnly || !evidence.ReturnedRecordsStable {
		return errors.New("alarmd execution: invalid typed PARTIAL evidence")
	}
	return nil
}
