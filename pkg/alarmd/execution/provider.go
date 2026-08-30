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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
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

type PhysicalQueryFilter struct {
	Field    string
	Operator string
	Values   []string
}

type PhysicalQueryClause struct {
	ReferenceName             string
	DataSourceLabel           string
	DataTypeLabel             string
	ResultTableID             string
	MetricField               string
	AggregationMethod         string
	AggregationIntervalMillis int64
	Dimensions                []string
	Filters                   []PhysicalQueryFilter
}

// PhysicalQuerySpec is the provider-neutral normalized request compiled by
// Access. The UQ provider owns the small /query/ts wire DTO conversion.
type PhysicalQuerySpec struct {
	Digest           PhysicalQueryDigest
	Provider         ProviderKind
	ProviderRouteRef ProviderRouteRef
	TenantID         string
	SpaceScope       string
	QueryRevision    QueryRevision
	LogicalWindow    QueryWindow
	ProviderRange    QueryWindow
	AcceptedRange    QueryWindow
	QueryList        []PhysicalQueryClause
	MetricMerge      string
	DownSampleRange  DownSampleRange
	StepMillis       int64
	AlignmentMillis  int64
	Timezone         string
	NotTimeAlign     bool
	RequiredColumns  []string
}

func (spec PhysicalQuerySpec) Validate() error {
	if spec.Digest == "" || spec.Provider != ProviderUQ || spec.ProviderRouteRef == "" ||
		spec.TenantID == "" || spec.SpaceScope == "" || spec.QueryRevision == "" {
		return errors.New("alarmd execution: complete physical query identity is required")
	}
	if spec.DownSampleRange != DownSampleNone {
		return errors.New("alarmd execution: phase two only supports explicit no-downsample")
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
	if len(spec.QueryList) == 0 || spec.StepMillis <= 0 || spec.AlignmentMillis <= 0 || len(spec.RequiredColumns) == 0 {
		return errors.New("alarmd execution: complete normalized physical query is required")
	}
	for _, clause := range spec.QueryList {
		if clause.ReferenceName == "" || clause.DataSourceLabel == "" || clause.DataTypeLabel == "" ||
			clause.ResultTableID == "" || clause.MetricField == "" || clause.AggregationMethod == "" ||
			clause.AggregationIntervalMillis <= 0 {
			return errors.New("alarmd execution: incomplete physical query clause")
		}
		for _, filter := range clause.Filters {
			if filter.Field == "" || filter.Operator == "" || len(filter.Values) == 0 {
				return errors.New("alarmd execution: incomplete physical query filter")
			}
		}
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
	if spec.Provider != ProviderUQ || spec.ProviderRouteRef == "" || spec.QueryRevision == "" {
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
	if attempt.Slot.QueryGroup == "" || attempt.Slot.ScheduleRevision == "" || attempt.Slot.EvaluationTime <= 0 {
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

type RouteAttemptFact struct {
	AttemptNo  uint32
	Endpoint   string
	Result     RouteAttemptResult
	ReasonCode ReasonCode
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

// ProviderResult is a physical query fact. Consumer-specific readiness and
// capability decisions belong to NamedInputBinding, not this object.
type ProviderResult struct {
	Ref             ProviderResultRef
	PhysicalQuery   PhysicalQueryDigest
	RequestedRange  QueryWindow
	Completeness    Completeness
	DataState       DataState
	Dataset         *Dataset
	TraceID         string
	RouteFacts      ProviderRouteFacts
	MaxEventTime    *int64
	QualityFacts    []ProviderQualityFact
	RecordTerminals []ProviderRecordTerminal
	PartialEvidence *PartialEvidence
	Stats           ProviderStats
}

func (result ProviderResult) Validate(attempt QueryAttempt) error {
	if err := attempt.Validate(); err != nil {
		return err
	}
	if result.Ref == "" || result.PhysicalQuery != attempt.Spec.Digest || result.RequestedRange != attempt.Spec.ProviderRange ||
		result.RouteFacts.ProviderRouteRef != attempt.Spec.ProviderRouteRef {
		return errors.New("alarmd execution: ProviderResult does not match QueryAttempt")
	}
	expectedTables := make([]string, 0, len(attempt.Spec.QueryList))
	seenTables := make(map[string]struct{}, len(attempt.Spec.QueryList))
	for _, query := range attempt.Spec.QueryList {
		if _, found := seenTables[query.ResultTableID]; found {
			continue
		}
		seenTables[query.ResultTableID] = struct{}{}
		expectedTables = append(expectedTables, query.ResultTableID)
	}
	sort.Strings(expectedTables)
	if !equalStrings(expectedTables, result.RouteFacts.ResultTableIDs) {
		return errors.New("alarmd execution: ProviderResult route tables do not match QueryAttempt")
	}
	return result.ValidateFacts()
}

// ValidateFacts validates the immutable physical result without requiring the
// transient QueryAttempt. It is used at the Access-to-Evaluation boundary;
// attempt/permit validation remains inside the query engine.
func (result ProviderResult) ValidateFacts() error {
	if result.Ref == "" || result.PhysicalQuery == "" || result.RouteFacts.ProviderRouteRef == "" {
		return errors.New("alarmd execution: incomplete ProviderResult identity")
	}
	if err := result.RequestedRange.Validate(); err != nil {
		return err
	}
	if err := validateRouteFacts(result.RouteFacts, result.Completeness); err != nil {
		return err
	}
	switch result.Completeness {
	case CompletenessFull:
		if result.Dataset == nil || (result.DataState != DataStateData && result.DataState != DataStateEmpty) {
			return errors.New("alarmd execution: invalid FULL ProviderResult")
		}
		if err := validateDatasetState(result.Dataset, result.DataState); err != nil {
			return err
		}
		if result.PartialEvidence != nil {
			return errors.New("alarmd execution: FULL ProviderResult must not carry PARTIAL evidence")
		}
	case CompletenessPartial:
		if result.Dataset == nil || (result.DataState != DataStateData && result.DataState != DataStateEmpty) {
			return errors.New("alarmd execution: invalid PARTIAL ProviderResult")
		}
		if err := validateDatasetState(result.Dataset, result.DataState); err != nil {
			return err
		}
		if result.PartialEvidence != nil {
			if err := result.PartialEvidence.Validate(); err != nil {
				return err
			}
		}
	case CompletenessUnavailable:
		if result.Dataset != nil || result.DataState != DataStateUnknown || result.MaxEventTime != nil ||
			len(result.QualityFacts) != 0 || len(result.RecordTerminals) != 0 {
			return errors.New("alarmd execution: invalid UNAVAILABLE ProviderResult")
		}
		if result.PartialEvidence != nil {
			return errors.New("alarmd execution: UNAVAILABLE ProviderResult must not carry PARTIAL evidence")
		}
	default:
		return errors.New("alarmd execution: invalid ProviderResult completeness")
	}
	for _, fact := range result.QualityFacts {
		if err := validateProviderFact(fact.ReasonCode, fact.RecordID, fact.SourceTime, fact.SeriesIdentity, false); err != nil {
			return err
		}
	}
	for _, terminal := range result.RecordTerminals {
		if err := validateProviderFact(terminal.ReasonCode, terminal.RecordID, terminal.SourceTime, terminal.SeriesIdentity, true); err != nil {
			return err
		}
	}
	if result.Completeness == CompletenessPartial && result.PartialEvidence == nil &&
		len(result.QualityFacts) == 0 && len(result.RecordTerminals) == 0 {
		return errors.New("alarmd execution: PARTIAL ProviderResult requires typed physical evidence")
	}
	return nil
}

func validateProviderFact(reason ReasonCode, recordID string, sourceTime int64, series SeriesIdentityDigest, terminal bool) error {
	reasonClass := contract.ReasonClassCoverage
	if terminal {
		reasonClass = contract.ReasonClassDeterministic
	}
	if err := requireReasonClass(reason, reasonClass); err != nil {
		return err
	}
	if recordID == "" || sourceTime <= 0 || series == "" {
		return errors.New("alarmd execution: physical Provider fact requires a stable record anchor and series locator")
	}
	return nil
}

func validateRouteFacts(facts ProviderRouteFacts, completeness Completeness) error {
	if len(facts.ResultTableIDs) == 0 {
		return errors.New("alarmd execution: ProviderResult requires routed result tables")
	}
	for index, tableID := range facts.ResultTableIDs {
		if tableID == "" || (index > 0 && facts.ResultTableIDs[index-1] >= tableID) {
			return errors.New("alarmd execution: routed result tables must be canonical, unique and non-empty")
		}
	}
	if len(facts.Attempts) == 0 {
		return errors.New("alarmd execution: ProviderResult requires ordered route attempt facts")
	}
	succeeded := false
	for index, attempt := range facts.Attempts {
		if attempt.AttemptNo != uint32(index+1) || attempt.Endpoint == "" {
			return errors.New("alarmd execution: route attempts must be ordered and fully identified")
		}
		switch attempt.Result {
		case RouteAttemptSucceeded:
			if succeeded || index != len(facts.Attempts)-1 ||
				(attempt.ReasonCode != "" && attempt.ReasonCode != observability.ReasonNone) {
				return errors.New("alarmd execution: invalid successful route attempt")
			}
			succeeded = true
		case RouteAttemptFailed:
			if err := requireReasonClass(attempt.ReasonCode, contract.ReasonClassRetryable); err != nil {
				return err
			}
		default:
			return errors.New("alarmd execution: invalid route attempt result")
		}
	}
	if completeness != CompletenessUnavailable && !succeeded {
		return errors.New("alarmd execution: available ProviderResult requires a successful route attempt")
	}
	if completeness == CompletenessUnavailable && succeeded {
		return errors.New("alarmd execution: unavailable ProviderResult cannot claim a successful route attempt")
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
