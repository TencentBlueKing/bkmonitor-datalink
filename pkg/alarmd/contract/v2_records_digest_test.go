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
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const recordsTestDomain = "alarmd-provider-series-delivery-v1"

// recordsTexts are the strings a dimension carries: plain, quoted, escaped,
// HTML the encoder does not escape here, the two line separators the
// canonical form writes literally, control characters, non-ASCII, and bytes
// that are not UTF-8.
var recordsTexts = []string{
	"host-1", `quo"te`, `back\slash`, "<a&b>", "line sep", "para sep", "tab\tand\nnewline",
	"\x01ctrl", "主机", "emoji \U0001F600", "", "not \xff utf-8", "trailing space ",
}

// recordsNumbers are value tokens as a query returns them, each of which the
// canonical form keeps as written.
var recordsNumbers = []string{"1", "1.0", "-0", "1e5", "1E-7", "12345678901234567890", "0.1", "3.14159", "null", "true", `"42"`}

// seriesRecords builds one series' records the way a provider does: every
// record pointing at the one field list and the one dimension map.
func seriesRecords(random *rand.Rand, points int) []CanonicalRecordV2 {
	text := func() string { return recordsTexts[random.Intn(len(recordsTexts))] }
	raw := func(value string) json.RawMessage {
		encoded, _ := json.Marshal(value)
		return encoded
	}
	fields := make([]DimensionFieldV2, random.Intn(6))
	for index := range fields {
		fields[index] = DimensionFieldV2{Name: fmt.Sprintf("field_%d_%s", index, text()), Value: raw(text())}
	}
	var dimensions map[string]json.RawMessage
	switch random.Intn(3) {
	case 0:
		// nil: no dimensions at all.
	case 1:
		dimensions = map[string]json.RawMessage{}
	default:
		dimensions = map[string]json.RawMessage{}
		for index := 0; index < 1+random.Intn(12); index++ {
			dimensions[fmt.Sprintf("dim_%d_%s", index, text())] = raw(text())
		}
	}
	business := []string{"2", "10", "业务", "", "line\u2028sep", "para\u2029sep", `quo"te`, "<tag>&", "tab\there"}[random.Intn(9)]
	received := []int64{0, 1_788_000_123, -5, 9_007_199_254_740_993}[random.Intn(4)]
	identity := DimensionIdentityV2{Fields: fields, Digest: strings.Repeat("d", 64)}
	records := make([]CanonicalRecordV2, points)
	for index := range records {
		var values map[string]json.RawMessage
		if random.Intn(10) != 0 {
			values = map[string]json.RawMessage{"value": json.RawMessage(recordsNumbers[random.Intn(len(recordsNumbers))])}
		}
		records[index] = CanonicalRecordV2{
			RecordID: recordID(random, index), SourceTime: 1_788_000_000 + int64(index)*60,
			BusinessID: business, DimensionIdentity: identity, Values: values, Dimensions: dimensions, ReceivedTime: received,
		}
	}
	return records
}

// recordID is a record id as a provider derives it, and now and then one
// carrying the characters canonical encoding treats specially, so the
// record id's own encoding is held to the canonical one too.
func recordID(random *rand.Rand, index int) string {
	if random.Intn(4) == 0 {
		return fmt.Sprintf("%d %s", index, recordsTexts[random.Intn(len(recordsTexts))])
	}
	return fmt.Sprintf("%064x", random.Uint64())
}

// The digest assembled from a series' shared parts is the generic canonical
// digest of its records, byte for byte, under both the established encoder
// and the single-pass one: for series of every width, with every kind of
// dimension text and value token, and for one point and sixty.
func TestAssembledRecordsDigestIsTheCanonicalOne(t *testing.T) {
	random := rand.New(rand.NewSource(20260928))
	assembled := 0
	for index := 0; index < 600; index++ {
		records := seriesRecords(random, 1+random.Intn(60))
		want, wantErr := DeriveCanonicalDigestV2(recordsTestDomain, records)
		got, shared := deriveSharedRecordsDigest(recordsTestDomain, records, DimensionIdentityEncodingV2{})
		if wantErr != nil {
			if shared {
				t.Fatalf("case %d: assembled %s where the canonical digest refuses: %v", index, got, wantErr)
			}
			continue
		}
		if !shared {
			t.Fatalf("case %d: a series was not assembled", index)
		}
		if got != want {
			t.Fatalf("case %d: assembled %s, canonical %s", index, got, want)
		}
		assembled++
	}
	if assembled < 500 {
		t.Fatalf("only %d of 600 series assembled; the corpus is not exercising the fast path", assembled)
	}
}

// A slice that is not one series -- records that do not share the field
// list, the dimensions, the business or the received time, or carry a
// collection time -- is not assembled, and DeriveRecordsDigestV2 gives the
// canonical digest for it all the same; an empty slice too.
func TestRecordsThatAreNotOneSeriesTakeTheCanonicalDigest(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	collected := int64(5)
	for name, split := range map[string]func([]CanonicalRecordV2){
		"another field list": func(r []CanonicalRecordV2) {
			r[1].DimensionIdentity.Fields = append([]DimensionFieldV2(nil), r[0].DimensionIdentity.Fields...)
		},
		"another dimension map": func(r []CanonicalRecordV2) { r[1].Dimensions = map[string]json.RawMessage{"x": json.RawMessage(`"y"`)} },
		"another business":      func(r []CanonicalRecordV2) { r[1].BusinessID = "other" },
		"another received time": func(r []CanonicalRecordV2) { r[1].ReceivedTime++ },
		"another identity":      func(r []CanonicalRecordV2) { r[1].DimensionIdentity.Digest = strings.Repeat("e", 64) },
		"a collection time":     func(r []CanonicalRecordV2) { r[0].CollectionTime = &collected },
		"no records":            func([]CanonicalRecordV2) {},
	} {
		t.Run(name, func(t *testing.T) {
			records := seriesRecords(random, 3)
			records[0].DimensionIdentity.Fields = append(records[0].DimensionIdentity.Fields, DimensionFieldV2{Name: "z", Value: json.RawMessage(`"1"`)})
			for index := range records {
				records[index].DimensionIdentity.Fields = records[0].DimensionIdentity.Fields
			}
			if name == "no records" {
				records = records[:0]
			}
			split(records)
			if _, shared := deriveSharedRecordsDigest(recordsTestDomain, records, DimensionIdentityEncodingV2{}); shared {
				t.Fatal("records that are not one series were assembled")
			}
			want, wantErr := DeriveCanonicalDigestV2(recordsTestDomain, records)
			got, err := DeriveRecordsDigestV2(recordsTestDomain, records)
			if got != want || (err == nil) != (wantErr == nil) {
				t.Fatalf("DeriveRecordsDigestV2 = %s, %v; canonical %s, %v", got, err, want, wantErr)
			}
		})
	}
}

// What the canonical digest refuses, DeriveRecordsDigestV2 refuses: a value
// that is not JSON, a dimension holding a duplicated key.
func TestRecordsTheCanonicalDigestRefusesAreRefused(t *testing.T) {
	random := rand.New(rand.NewSource(11))
	for name, spoil := range map[string]func([]CanonicalRecordV2){
		"a value that is not JSON": func(r []CanonicalRecordV2) { r[1].Values = map[string]json.RawMessage{"value": json.RawMessage(`1 2`)} },
		"a duplicated key": func(r []CanonicalRecordV2) {
			dimensions := map[string]json.RawMessage{"d": json.RawMessage(`{"a":1,"a":2}`)}
			for index := range r {
				r[index].Dimensions = dimensions
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			records := seriesRecords(random, 3)
			spoil(records)
			if _, wantErr := DeriveCanonicalDigestV2(recordsTestDomain, records); wantErr == nil {
				t.Fatal("setup: the canonical digest accepts it")
			}
			if digest, err := DeriveRecordsDigestV2(recordsTestDomain, records); err == nil {
				t.Fatalf("DeriveRecordsDigestV2 = %s, want the refusal", digest)
			}
		})
	}
}

// The shadow compares one call in the stride with the generic derivation.
// When the two differ it returns the generic digest, counts the difference,
// and reports it once however many follow within the interval.
func TestTheShadowServesTheCanonicalDigestAndReportsADifferenceOnce(t *testing.T) {
	previousStride, previousAssemble := recordsShadowStride, assembleRecordsDigest
	defer func() {
		recordsShadowStride, assembleRecordsDigest = previousStride, previousAssemble
		SetRecordsDigestDivergenceReporter(nil)
		recordsDivergenceReported.Store(0)
	}()
	recordsShadowStride = 1
	records := seriesRecords(rand.New(rand.NewSource(3)), 5)
	want, err := DeriveCanonicalDigestV2(recordsTestDomain, records)
	if err != nil {
		t.Fatal(err)
	}

	compared, differed := ReadRecordsDigestShadowCounts()
	if got, err := DeriveRecordsDigestV2(recordsTestDomain, records); err != nil || got != want {
		t.Fatalf("DeriveRecordsDigestV2 = %s, %v, want %s", got, err, want)
	}
	if nowCompared, nowDiffered := ReadRecordsDigestShadowCounts(); nowCompared != compared+1 || nowDiffered != differed {
		t.Fatalf("an agreeing call counted compared %d->%d, differed %d->%d", compared, nowCompared, differed, nowDiffered)
	}

	assembleRecordsDigest = func(string, []CanonicalRecordV2, DimensionIdentityEncodingV2) (string, bool) {
		return strings.Repeat("0", 64), true
	}
	var reports []RecordsDigestDivergence
	SetRecordsDigestDivergenceReporter(func(divergence RecordsDigestDivergence) { reports = append(reports, divergence) })
	recordsDivergenceReported.Store(0)
	_, differed = ReadRecordsDigestShadowCounts()
	for call := 0; call < 3; call++ {
		if got, err := DeriveRecordsDigestV2(recordsTestDomain, records); err != nil || got != want {
			t.Fatalf("call %d with a wrong assembly = %s, %v, want the canonical %s", call, got, err, want)
		}
	}
	if _, nowDiffered := ReadRecordsDigestShadowCounts(); nowDiffered != differed+3 {
		t.Fatalf("differences counted %d, want 3", nowDiffered-differed)
	}
	if len(reports) != 1 || reports[0].Served != strings.Repeat("0", 64) || reports[0].Established != want || reports[0].Records != 5 {
		t.Fatalf("reports = %+v, want one naming the served and the canonical digest", reports)
	}
}

// Assembling a series' digest allocates a fraction of what the canonical
// encoding of its records does: the dimensions are encoded once, not once
// per point.
func TestAssemblingASeriesDigestAllocatesAFractionOfTheCanonicalOne(t *testing.T) {
	random := rand.New(rand.NewSource(5))
	var records []CanonicalRecordV2
	for len(records) == 0 || len(records[0].Dimensions) < 8 {
		records = seriesRecords(random, 60)
	}
	canonical := testing.AllocsPerRun(20, func() { _, _ = DeriveCanonicalDigestV2(recordsTestDomain, records) })
	assembled := testing.AllocsPerRun(20, func() { _, _ = deriveSharedRecordsDigest(recordsTestDomain, records, DimensionIdentityEncodingV2{}) })
	if assembled*2 > canonical {
		t.Fatalf("assembling allocates %.0f times against %.0f for the canonical encoding", assembled, canonical)
	}
}
