// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"strconv"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The lengths below are those encoding/json gives the same document, counted
// rather than encoded: a series' bindings were encoded only to be measured,
// which was a twentieth of reading a large answer. A field added to a type
// here is a field the count must add too; the property test draws every
// field and holds the count to the encoding.

// jsonStringLength is the length of s encoded as a JSON string, quotes
// included, escaped as encoding/json escapes it (HTML characters as well).
func jsonStringLength(s string) int {
	length := 2
	for index := 0; index < len(s); {
		if b := s[index]; b < utf8.RuneSelf {
			switch {
			case b == '\\' || b == '"' || b == '\b' || b == '\f' || b == '\n' || b == '\r' || b == '\t':
				length += 2
			case b < 0x20 || b == '<' || b == '>' || b == '&':
				length += 6
			default:
				length++
			}
			index++
			continue
		}
		c, size := utf8.DecodeRuneInString(s[index:])
		switch {
		case c == utf8.RuneError && size == 1, c == '\u2028', c == '\u2029':
			length += 6
		default:
			length += size
		}
		index += size
	}
	return length
}

// jsonKeyLength is a field's name with its quotes and colon; jsonBoolLength
// and jsonIntLength a value's.
func jsonKeyLength(name string) int { return len(name) + 3 }

func jsonBoolLength(value bool) int {
	if value {
		return 4
	}
	return 5
}

func jsonIntLength(value int64) int   { return len(strconv.AppendInt(make([]byte, 0, 20), value, 10)) }
func jsonUintLength(value uint64) int { return len(strconv.AppendUint(make([]byte, 0, 20), value, 10)) }

// jsonObject sums an object's fields: braces, the commas between them, and
// each field's key and value.
func jsonObject(fields ...int) int {
	length := 2 + len(fields) - 1
	for _, field := range fields {
		length += field
	}
	return length
}

func jsonField(name string, value int) int { return jsonKeyLength(name) + value }

const jsonNullLength = 4

func planIdentityLength(identity execution.PlanIdentity) int {
	return jsonObject(jsonField("TenantID", jsonStringLength(identity.TenantID)),
		jsonField("BusinessID", jsonStringLength(identity.BusinessID)),
		jsonField("StrategyID", jsonStringLength(identity.StrategyID)))
}

func consumerRefLength(consumer execution.ConsumerRef) int {
	return jsonObject(jsonField("Plan", planIdentityLength(consumer.Plan)),
		jsonField("LevelID", jsonUintLength(uint64(consumer.LevelID))),
		jsonField("HasLevel", jsonBoolLength(consumer.HasLevel)))
}

func queryWindowLength(window execution.QueryWindow) int {
	return jsonObject(jsonField("Start", jsonIntLength(window.Start)), jsonField("End", jsonIntLength(window.End)))
}

// recordFactLength is an InputQualityFact's or an InputTerminal's: the two
// carry the same fields.
func recordFactLength(reason execution.ReasonCode, impact execution.ImpactScope, recordID string, sourceTime int64,
	series execution.SeriesIdentityDigest) int {
	return jsonObject(jsonField("ReasonCode", jsonStringLength(string(reason))),
		jsonField("ImpactScope", jsonStringLength(string(impact))),
		jsonField("RecordID", jsonStringLength(recordID)),
		jsonField("SourceTime", jsonIntLength(sourceTime)),
		jsonField("SeriesIdentity", jsonStringLength(string(series))))
}

// jsonArray sums an array: brackets, commas, items; a nil slice is null.
func jsonArray(isNil bool, items []int) int {
	if isNil {
		return jsonNullLength
	}
	length := 2
	if len(items) > 0 {
		length += len(items) - 1
	}
	for _, item := range items {
		length += item
	}
	return length
}

func qualityFactsLength(facts []execution.InputQualityFact) int {
	items := make([]int, len(facts))
	for index, fact := range facts {
		items[index] = recordFactLength(fact.ReasonCode, fact.ImpactScope, fact.RecordID, fact.SourceTime, fact.SeriesIdentity)
	}
	return jsonArray(facts == nil, items)
}

func terminalsLength(terminals []execution.InputTerminal) int {
	items := make([]int, len(terminals))
	for index, terminal := range terminals {
		items[index] = recordFactLength(terminal.ReasonCode, terminal.ImpactScope, terminal.RecordID, terminal.SourceTime, terminal.SeriesIdentity)
	}
	return jsonArray(terminals == nil, items)
}

func partialEvidenceLength(evidence *execution.PartialEvidence) int {
	if evidence == nil {
		return jsonNullLength
	}
	return jsonObject(jsonField("Kind", jsonStringLength(evidence.Kind)),
		jsonField("Version", jsonUintLength(uint64(evidence.Version))),
		jsonField("EvidenceDigest", jsonStringLength(evidence.EvidenceDigest)),
		jsonField("OmissionOnly", jsonBoolLength(evidence.OmissionOnly)),
		jsonField("ReturnedRecordsStable", jsonBoolLength(evidence.ReturnedRecordsStable)))
}

func provenanceLength(provenance execution.InputProvenance) int {
	return jsonObject(jsonField("PhysicalQuery", jsonStringLength(string(provenance.PhysicalQuery))),
		jsonField("AttemptNo", jsonUintLength(uint64(provenance.AttemptNo))),
		jsonField("TraceID", jsonStringLength(provenance.TraceID)))
}

// retainedBindingLength is a binding's length with its Dataset and View left
// out, as the retained size counts it: both encode as null.
func retainedBindingLength(binding execution.NamedInputBinding) int {
	return jsonObject(jsonField("Consumer", consumerRefLength(binding.Consumer)),
		jsonField("RequirementID", jsonStringLength(string(binding.RequirementID))),
		jsonField("DatasetName", jsonStringLength(string(binding.DatasetName))),
		jsonField("Role", jsonStringLength(string(binding.Role))),
		jsonField("ProviderResult", jsonStringLength(string(binding.ProviderResult))),
		jsonField("QueryWindow", queryWindowLength(binding.QueryWindow)),
		jsonField("Dataset", jsonNullLength),
		jsonField("View", jsonNullLength),
		jsonField("Completeness", jsonStringLength(string(binding.Completeness))),
		jsonField("DataState", jsonStringLength(string(binding.DataState))),
		jsonField("Disposition", jsonStringLength(string(binding.Disposition))),
		jsonField("ReasonCode", jsonStringLength(string(binding.ReasonCode))),
		jsonField("ImpactScope", jsonStringLength(string(binding.ImpactScope))),
		jsonField("QualityFacts", qualityFactsLength(binding.QualityFacts)),
		jsonField("Terminals", terminalsLength(binding.Terminals)),
		jsonField("PartialEvidence", partialEvidenceLength(binding.PartialEvidence)),
		jsonField("Provenance", provenanceLength(binding.Provenance)),
		jsonField("UnavailableAttribution", jsonStringLength(string(binding.UnavailableAttribution))))
}

func seriesDeliveryLength(delivery execution.SeriesDelivery) int {
	return jsonObject(jsonField("PhysicalQuery", jsonStringLength(string(delivery.PhysicalQuery))),
		jsonField("QueryRevision", jsonStringLength(string(delivery.QueryRevision))),
		jsonField("Series", jsonUintLength(delivery.Series)),
		jsonField("Records", jsonUintLength(delivery.Records)),
		jsonField("Bytes", jsonUintLength(delivery.Bytes)),
		jsonField("Digest", jsonStringLength(delivery.Digest)))
}

// retainedDocumentLength is the length of {"Bindings":[...],"Delivery":{...}}
// for bindings without their datasets: what streamedRetainedSize measured by
// encoding it.
func retainedDocumentLength(bindings []execution.NamedInputBinding, delivery execution.SeriesDelivery) int {
	items := make([]int, len(bindings))
	for index, binding := range bindings {
		items[index] = retainedBindingLength(binding)
	}
	// The bindings were copied into a slice of their length before they were
	// encoded, so none is an empty array, not null.
	return jsonObject(jsonField("Bindings", jsonArray(false, items)), jsonField("Delivery", seriesDeliveryLength(delivery)))
}
