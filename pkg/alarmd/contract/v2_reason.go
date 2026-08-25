// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "sort"

type ReasonClassV2 string

const (
	ReasonClassDeterministic ReasonClassV2 = "DETERMINISTIC"
	ReasonClassRetryable     ReasonClassV2 = "RETRYABLE"
	ReasonClassCoverage      ReasonClassV2 = "COVERAGE"
)

type ReasonDomainsV2 uint32

const (
	ReasonDomainQueryResult ReasonDomainsV2 = 1 << iota
	ReasonDomainValidationIssue
	ReasonDomainReceipt
	ReasonDomainSummary
	ReasonDomainObservation
)

func (domains ReasonDomainsV2) Has(domain ReasonDomainsV2) bool {
	return domain != 0 && domains&domain == domain
}

type ReasonDefinitionV2 struct {
	Code    string
	Class   ReasonClassV2
	Domains ReasonDomainsV2
}

const (
	reasonOutcomeDomainsV2 = ReasonDomainValidationIssue | ReasonDomainReceipt | ReasonDomainObservation
	reasonMessageDomainsV2 = ReasonDomainReceipt | ReasonDomainObservation
	reasonQueryDomainsV2   = ReasonDomainQueryResult | ReasonDomainReceipt | ReasonDomainSummary | ReasonDomainObservation
)

var reasonCatalogV2 = map[string]ReasonDefinitionV2{
	ReasonMalformedJSON:              {ReasonMalformedJSON, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonSchemaMajorUnsupported:     {ReasonSchemaMajorUnsupported, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonRequiredFeatureUnsupported: {ReasonRequiredFeatureUnsupported, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonTenantInvalid:              {ReasonTenantInvalid, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonPayloadDigestMismatch:      {ReasonPayloadDigestMismatch, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonPlanSetConflict:            {ReasonPlanSetConflict, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonSelectorOrdinalInvalid:     {ReasonSelectorOrdinalInvalid, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonMessageBudgetExceeded:      {ReasonMessageBudgetExceeded, ReasonClassDeterministic, reasonMessageDomainsV2},

	ReasonPlanInvalid:              {ReasonPlanInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonPlanDuplicateLevelID:     {ReasonPlanDuplicateLevelID, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonPlanBudgetExceeded:       {ReasonPlanBudgetExceeded, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonProjectionInvalid:        {ReasonProjectionInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonSelectorInvalid:          {ReasonSelectorInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonLevelInvalid:             {ReasonLevelInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonAlgorithmUnsupported:     {ReasonAlgorithmUnsupported, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonLevelBudgetExceeded:      {ReasonLevelBudgetExceeded, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonRecordInvalid:            {ReasonRecordInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonRecordIdentityConflict:   {ReasonRecordIdentityConflict, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonTimeInvalid:              {ReasonTimeInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonLateOutOfWindow:          {ReasonLateOutOfWindow, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonValidationBudgetExceeded: {ReasonValidationBudgetExceeded, ReasonClassDeterministic, reasonOutcomeDomainsV2},

	ReasonRequiredValueMissing:             {ReasonRequiredValueMissing, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonRequiredValueTypeMismatch:        {ReasonRequiredValueTypeMismatch, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonRequiredValueNormalizationFailed: {ReasonRequiredValueNormalizationFailed, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},

	ReasonConfigDrift:      {ReasonConfigDrift, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryPartial:     {ReasonQueryPartial, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryTimeout:     {ReasonQueryTimeout, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryUnavailable: {ReasonQueryUnavailable, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonRecordTooLarge:   {ReasonRecordTooLarge, ReasonClassCoverage, ReasonDomainSummary | ReasonDomainObservation},
	ReasonAuditDrop:        {ReasonAuditDrop, ReasonClassCoverage, ReasonDomainObservation},

	ReasonKafkaUnavailable:    {ReasonKafkaUnavailable, ReasonClassRetryable, ReasonDomainSummary | ReasonDomainObservation},
	ReasonRedisUnavailable:    {ReasonRedisUnavailable, ReasonClassRetryable, ReasonDomainObservation},
	ReasonResourceHardStop:    {ReasonResourceHardStop, ReasonClassRetryable, ReasonDomainObservation},
	ReasonOutputACKUnknown:    {ReasonOutputACKUnknown, ReasonClassRetryable, ReasonDomainObservation},
	ReasonStateWriteRetryable: {ReasonStateWriteRetryable, ReasonClassRetryable, ReasonDomainObservation},
}

func ReasonCatalogV2() []ReasonDefinitionV2 {
	definitions := make([]ReasonDefinitionV2, 0, len(reasonCatalogV2))
	for _, definition := range reasonCatalogV2 {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(left, right int) bool { return definitions[left].Code < definitions[right].Code })
	return definitions
}

func LookupReasonV2(code string) (ReasonDefinitionV2, bool) {
	definition, ok := reasonCatalogV2[code]
	return definition, ok
}

func IsKnownReasonV2(code string) bool {
	_, ok := LookupReasonV2(code)
	return ok
}

func ReasonAllowedForV2(code string, domain ReasonDomainsV2) bool {
	definition, ok := LookupReasonV2(code)
	return ok && definition.Domains.Has(domain)
}
