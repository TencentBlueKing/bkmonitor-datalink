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
	"sort"
	"testing"
)

// identityNames are dimension names a writer can send: the characters
// canonical encoding escapes or keeps, the line separators it restores,
// HTML, and text that is not ASCII. Every one is valid UTF-8, which the
// identity requires of a name.
var identityNames = []string{"bk_target_ip", "device", `quo"te`, `back\slash`, "<tag>&amp", "line sep", "para sep",
	"业务", "tab\there", "ctl\u0001x", "emoji\U0001F600", "a", "z"}

// identityValues are scalar value tokens as a query returns them: strings with
// the same characters, numbers as written, null and the two booleans.
var identityValues = []string{`"192.0.2.1"`, `"quo\"te"`, `"<b>&"`, `"line "`, `"业务"`, `"\u0001"`, "1", "1.0", "-0", "1e5",
	"12345678901234567890", "null", "true", "false", `""`}

// identitySeries builds one series the way the provider does: identity
// fields sorted by name, a dimension map holding them and some that are not
// in the identity, and every record pointing at the one field list and the
// one map; with the identity encoded from those fields.
func identitySeries(t testing.TB, random *rand.Rand, points int) ([]CanonicalRecordV2, DimensionIdentityEncodingV2) {
	t.Helper()
	names := append([]string(nil), identityNames...)
	random.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	names = names[:random.Intn(len(names))]
	sort.Strings(names)
	dimensions := map[string]json.RawMessage{}
	fields := make([]DimensionFieldV2, 0, len(names))
	for _, name := range names {
		value := json.RawMessage(identityValues[random.Intn(len(identityValues))])
		fields = append(fields, DimensionFieldV2{Name: name, Value: value})
		dimensions[name] = value
	}
	for index := 0; index < random.Intn(4); index++ {
		dimensions[fmt.Sprintf("extra_%d", index)] = json.RawMessage(identityValues[random.Intn(len(identityValues))])
	}
	business := []string{"2", "10", "-7", "0"}[random.Intn(4)]
	identity, err := EncodeDimensionIdentityV2("system", business, fields)
	if err != nil {
		t.Fatalf("encode identity %v: %v", names, err)
	}
	records := make([]CanonicalRecordV2, points)
	for index := range records {
		records[index] = CanonicalRecordV2{RecordID: fmt.Sprintf("%064x", random.Uint64()), SourceTime: 1_788_000_000 + int64(index)*60,
			BusinessID: business, DimensionIdentity: DimensionIdentityV2{Fields: fields, Digest: identity.Digest},
			Values: map[string]json.RawMessage{"value": json.RawMessage(recordsNumbers[random.Intn(len(recordsNumbers))])}, Dimensions: dimensions,
			ReceivedTime: 1_788_000_123}
	}
	return records, identity
}

// The identity digest is the one it was before the encoding was kept: a
// digest pinned from the derivation as it stood, the same digest from the
// length-prefixed hash of the fields' canonical encoding computed here, and
// the same refusals from both entry points.
func TestTheIdentityEncodingDerivesTheSameDigest(t *testing.T) {
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	fields := []DimensionFieldV2{{Name: "bk_target_ip", Value: raw(`"192.0.2.1"`)}, {Name: "device", Value: raw(`null`)},
		{Name: "line sep", Value: raw(`1.50`)}, {Name: "quo\"te<tag>", Value: raw(`"业务& "`)}, {Name: "z", Value: raw(`true`)}}
	const pinned = "e0c33492b166c81b981997cb4cb6c21a12ab1dbc9d3955b172f532926c76781a"
	identity, err := EncodeDimensionIdentityV2("system", "-7", fields)
	if err != nil || identity.Digest != pinned {
		t.Fatalf("encoded digest = %q, %v; want the pinned %s", identity.Digest, err, pinned)
	}
	if digest, err := DeriveDimensionIdentityDigestV2("system", "-7", fields); err != nil || digest != pinned {
		t.Fatalf("derived digest = %q, %v; want the pinned %s", digest, err, pinned)
	}
	canonical, err := CanonicalJSONV2(fields)
	if err != nil {
		t.Fatal(err)
	}
	if reference, err := deriveLengthPrefixedSHA256("dimension_identity.digest", "dimension-identity-v1",
		[]byte("system"), []byte("-7"), canonical); err != nil || reference != pinned || string(identity.canonical) != string(canonical) {
		t.Fatalf("reference %q, %v; kept encoding %s, want %s", reference, err, identity.canonical, canonical)
	}

	for name, call := range map[string]func() (string, string, []DimensionFieldV2){
		"empty tenant": func() (string, string, []DimensionFieldV2) { return "", "2", fields },
		"business":     func() (string, string, []DimensionFieldV2) { return "system", "02", fields },
		"nil fields":   func() (string, string, []DimensionFieldV2) { return "system", "2", nil },
		"unsorted fields": func() (string, string, []DimensionFieldV2) {
			return "system", "2", []DimensionFieldV2{fields[1], fields[0]}
		},
		"object value": func() (string, string, []DimensionFieldV2) {
			return "system", "2", []DimensionFieldV2{{Name: "a", Value: raw(`{"b":1}`)}}
		},
	} {
		tenant, business, list := call()
		encoded, encodeErr := EncodeDimensionIdentityV2(tenant, business, list)
		_, deriveErr := DeriveDimensionIdentityDigestV2(tenant, business, list)
		if encodeErr == nil || deriveErr == nil || encodeErr.Error() != deriveErr.Error() || encoded.canonical != nil || encoded.Digest != "" {
			t.Fatalf("%s: encode %v, derive %v; want the same refusal and no encoding", name, encodeErr, deriveErr)
		}
	}
}

// A series' delivery digest with its identity's encoding is the canonical
// digest of its records, byte for byte, under both encoders: for identities
// of every width -- none to thirteen fields -- with names and values that
// escape, carry line separators, HTML, control characters and text that is
// not ASCII, numbers as written, nulls and booleans; for one point and sixty.
// The identity part is the encoding's own every time, so the corpus is the
// path that takes it.
func TestASeriesDigestWithItsIdentityEncodingIsTheCanonicalOne(t *testing.T) {
	random := rand.New(rand.NewSource(20260929))
	for index := 0; index < 600; index++ {
		records, identity := identitySeries(t, random, 1+random.Intn(60))
		want, err := DeriveCanonicalDigestV2(recordsTestDomain, records)
		if err != nil {
			t.Fatalf("case %d: canonical digest refused: %v", index, err)
		}
		part, err := dimensionIdentityPart(records[0].DimensionIdentity, identity)
		wantPart, wantErr := CanonicalJSONV2(records[0].DimensionIdentity)
		if err != nil || wantErr != nil || string(part) != string(wantPart) {
			t.Fatalf("case %d: identity part %s (%v), canonical %s (%v)", index, part, err, wantPart, wantErr)
		}
		if !identityPartIsTheEncodings(records[0].DimensionIdentity, identity) {
			t.Fatalf("case %d: the identity part was not taken from the encoding", index)
		}
		if got, err := DeriveSeriesRecordsDigestV2(recordsTestDomain, records, identity); err != nil || got != want {
			t.Fatalf("case %d: series digest %s (%v), canonical %s", index, got, err, want)
		}
	}
}

// identityPartIsTheEncodings is whether dimensionIdentityPart takes the
// identity's part from encoded, by the conditions it takes it under.
func identityPartIsTheEncodings(identity DimensionIdentityV2, encoded DimensionIdentityEncodingV2) bool {
	return encoded.canonical != nil && encoded.Digest == identity.Digest && sameSlice(encoded.fields, identity.Fields)
}

// An encoding answers only for the identity it was made from. Records whose
// fields are an equal list in another array, whose digest is another, or an
// encoding whose digest was rewritten to something that is not a digest,
// have their identity encoded whole -- and the series' digest is the
// canonical one all the same. The zero encoding is DeriveRecordsDigestV2.
func TestAnIdentityEncodingAnswersOnlyForItsOwnIdentity(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	records, identity := identitySeries(t, random, 3)
	for len(records[0].DimensionIdentity.Fields) == 0 {
		records, identity = identitySeries(t, random, 3)
	}
	copied := append([]DimensionFieldV2(nil), records[0].DimensionIdentity.Fields...)
	other, otherIdentity := identitySeries(t, random, 3)
	rewritten := identity
	rewritten.Digest = `"},"fields":[]}`
	for name, series := range map[string]struct {
		records  []CanonicalRecordV2
		identity DimensionIdentityEncodingV2
	}{
		"an equal list in another array": {withIdentity(records, DimensionIdentityV2{Fields: copied, Digest: identity.Digest}), identity},
		"another digest": {withIdentity(records, DimensionIdentityV2{Fields: records[0].DimensionIdentity.Fields,
			Digest: otherIdentity.Digest}), identity},
		"another series' encoding": {other, identity},
		"a rewritten digest": {withIdentity(records, DimensionIdentityV2{Fields: records[0].DimensionIdentity.Fields,
			Digest: rewritten.Digest}), rewritten},
		"no encoding": {records, DimensionIdentityEncodingV2{}},
	} {
		if identityPartIsTheEncodings(series.records[0].DimensionIdentity, series.identity) && name != "a rewritten digest" {
			t.Fatalf("%s: the fixture takes the encoding's part; it is meant not to", name)
		}
		part, err := dimensionIdentityPart(series.records[0].DimensionIdentity, series.identity)
		wantPart, wantErr := CanonicalJSONV2(series.records[0].DimensionIdentity)
		if err != nil || wantErr != nil || string(part) != string(wantPart) {
			t.Fatalf("%s: identity part %s (%v), canonical %s (%v)", name, part, err, wantPart, wantErr)
		}
		want, wantErr := DeriveCanonicalDigestV2(recordsTestDomain, series.records)
		got, err := DeriveSeriesRecordsDigestV2(recordsTestDomain, series.records, series.identity)
		if got != want || (err == nil) != (wantErr == nil) {
			t.Fatalf("%s: series digest %s (%v), canonical %s (%v)", name, got, err, want, wantErr)
		}
	}
	if got, err := DeriveRecordsDigestV2(recordsTestDomain, records); err != nil ||
		got != must(DeriveSeriesRecordsDigestV2(recordsTestDomain, records, DimensionIdentityEncodingV2{})) {
		t.Fatalf("DeriveRecordsDigestV2 = %s, %v; want the series digest with no encoding", got, err)
	}
}

func withIdentity(records []CanonicalRecordV2, identity DimensionIdentityV2) []CanonicalRecordV2 {
	out := append([]CanonicalRecordV2(nil), records...)
	for index := range out {
		out[index].DimensionIdentity = identity
	}
	return out
}

func must(value string, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return value
}

// Taking the fields' encoding from the identity saves encoding them again:
// the series' identity and delivery digests together allocate less with the
// encoding passed than without it, by at least the encoding of the fields.
func TestASeriesWithItsIdentityEncodingAllocatesLess(t *testing.T) {
	records, identity := benchmarkIdentitySeries(1)
	fields := records[0].DimensionIdentity.Fields
	separate := testing.AllocsPerRun(50, func() {
		_, _ = DeriveDimensionIdentityDigestV2("system", "2", fields)
		_, _ = DeriveSeriesRecordsDigestV2(recordsTestDomain, records, DimensionIdentityEncodingV2{})
	})
	shared := testing.AllocsPerRun(50, func() {
		encoded, _ := EncodeDimensionIdentityV2("system", "2", fields)
		_, _ = DeriveSeriesRecordsDigestV2(recordsTestDomain, records, encoded)
	})
	fieldsOnly := testing.AllocsPerRun(50, func() { _, _ = CanonicalJSONV2(DimensionIdentityV2{Fields: fields, Digest: identity.Digest}) })
	if shared > separate-fieldsOnly/2 {
		t.Fatalf("allocations: %v with the encoding shared, %v without; encoding the identity alone is %v", shared, separate, fieldsOnly)
	}
}

// benchmarkIdentitySeries is a series of ten dimensions, all in the identity,
// with points points: the shape of most series a sixty-second group reads.
func benchmarkIdentitySeries(points int) ([]CanonicalRecordV2, DimensionIdentityEncodingV2) {
	dimensions := map[string]json.RawMessage{}
	fields := make([]DimensionFieldV2, 0, 10)
	for index := 0; index < 10; index++ {
		value, _ := json.Marshal(fmt.Sprintf("value-%d-host-192.0.2.%d", index, index))
		name := fmt.Sprintf("label_%d", index)
		dimensions[name] = value
		fields = append(fields, DimensionFieldV2{Name: name, Value: value})
	}
	identity, err := EncodeDimensionIdentityV2("system", "2", fields)
	if err != nil {
		panic(err)
	}
	records := make([]CanonicalRecordV2, points)
	for index := range records {
		records[index] = CanonicalRecordV2{RecordID: fmt.Sprintf("%064x", index+1), SourceTime: 1_788_000_000 + int64(index)*60, BusinessID: "2",
			DimensionIdentity: DimensionIdentityV2{Fields: fields, Digest: identity.Digest},
			Values:            map[string]json.RawMessage{"value": json.RawMessage("12.5")}, Dimensions: dimensions, ReceivedTime: 1_788_000_123}
	}
	return records, identity
}

func BenchmarkSeriesIdentityAndDeliveryDigests(b *testing.B) {
	for _, points := range []int{1, 2, 5} {
		records, _ := benchmarkIdentitySeries(points)
		fields := records[0].DimensionIdentity.Fields
		b.Run(fmt.Sprintf("points=%d/separate", points), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_, _ = DeriveDimensionIdentityDigestV2("system", "2", fields)
				_, _ = DeriveSeriesRecordsDigestV2(recordsTestDomain, records, DimensionIdentityEncodingV2{})
			}
		})
		b.Run(fmt.Sprintf("points=%d/shared", points), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				encoded, _ := EncodeDimensionIdentityV2("system", "2", fields)
				_, _ = DeriveSeriesRecordsDigestV2(recordsTestDomain, records, encoded)
			}
		})
	}
}
