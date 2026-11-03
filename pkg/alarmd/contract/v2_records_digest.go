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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"
)

// DeriveRecordsDigestV2 is DeriveCanonicalDigestV2(domain, records), the same
// digest of the same bytes, for the records of one series.
//
// Every record of a series carries the series' business, dimension identity,
// dimensions and received time, and the canonical encoding of the slice
// encoded all four again for every point -- the dimensions, the widest of
// them, once per record. Here the four are encoded once, the record id, the
// source time and the values once per record, and the canonical bytes are
// assembled from those parts in the order canonical encoding puts an
// object's keys; they are hashed as they are assembled, never held whole.
// Records that do not all share the four -- a slice that is not one series --
// take DeriveCanonicalDigestV2 itself, and so does anything the parts cannot
// answer: an error here is the generic path's to report.
//
// One call in recordsShadowStride also derives the digest the generic way
// and compares the two. The generic answer is the definition: on a
// difference it is the one returned, the difference is counted
// (ReadRecordsDigestShadowCounts) and reported at most once a minute, and
// the call succeeds either way.
func DeriveRecordsDigestV2(domain string, records []CanonicalRecordV2) (string, error) {
	return DeriveSeriesRecordsDigestV2(domain, records, DimensionIdentityEncodingV2{})
}

// DeriveSeriesRecordsDigestV2 is DeriveRecordsDigestV2 for a series whose
// dimension identity was derived with EncodeDimensionIdentityV2: the fields
// the identity digest was derived from are the fields every record carries
// in its dimension identity, and their canonical encoding is taken from the
// identity rather than made again. It is taken only when the records carry
// that identity -- its digest, and the very list it was encoded from -- and
// otherwise the fields are encoded here, as DeriveRecordsDigestV2 does.
func DeriveSeriesRecordsDigestV2(domain string, records []CanonicalRecordV2, identity DimensionIdentityEncodingV2) (string, error) {
	if !isOpaqueASCII(domain) {
		return "", invalid("canonical_digest.domain", "must be non-empty opaque ASCII")
	}
	digest, shared := assembleRecordsDigest(domain, records, identity)
	if !shared {
		return DeriveCanonicalDigestV2(domain, records)
	}
	if !shouldSampleRecordsShadow() {
		return digest, nil
	}
	recordsShadowCompared.Add(1)
	established, err := DeriveCanonicalDigestV2(domain, records)
	if err == nil && established == digest {
		return digest, nil
	}
	recordsShadowDiffered.Add(1)
	reportRecordsDivergence(RecordsDigestDivergence{Domain: domain, Records: len(records), Served: digest, Established: established})
	return established, err
}

// assembleRecordsDigest is deriveSharedRecordsDigest, held in a variable so
// a test can stand in a wrong one and see the shadow catch it.
var assembleRecordsDigest = deriveSharedRecordsDigest

// deriveSharedRecordsDigest is the digest assembled from the series' shared
// parts, and false when the records do not share them or a part cannot be
// encoded.
func deriveSharedRecordsDigest(domain string, records []CanonicalRecordV2, encoded DimensionIdentityEncodingV2) (string, bool) {
	if len(records) == 0 {
		return "", false
	}
	first := records[0]
	for index := range records {
		record := &records[index]
		if record.CollectionTime != nil || record.BusinessID != first.BusinessID || record.ReceivedTime != first.ReceivedTime ||
			record.DimensionIdentity.Digest != first.DimensionIdentity.Digest ||
			!sameSlice(record.DimensionIdentity.Fields, first.DimensionIdentity.Fields) || !sameMap(record.Dimensions, first.Dimensions) {
			return "", false
		}
	}
	business, err := CanonicalJSONV2(first.BusinessID)
	if err != nil {
		return "", false
	}
	identity, err := dimensionIdentityPart(first.DimensionIdentity, encoded)
	if err != nil {
		return "", false
	}
	dimensions, err := CanonicalJSONV2(first.Dimensions)
	if err != nil {
		return "", false
	}
	// The object's keys in the order canonical encoding sorts them:
	// business_id, dimension_identity, dimensions, received_time, record_id,
	// source_time, values. collection_time is omitted when absent, and every
	// record here has it absent.
	prefix := make([]byte, 0, len(business)+len(identity)+len(dimensions)+96)
	prefix = append(prefix, `{"business_id":`...)
	prefix = append(prefix, business...)
	prefix = append(prefix, `,"dimension_identity":`...)
	prefix = append(prefix, identity...)
	prefix = append(prefix, `,"dimensions":`...)
	prefix = append(prefix, dimensions...)
	prefix = append(prefix, `,"received_time":`...)
	prefix = strconv.AppendInt(prefix, first.ReceivedTime, 10)
	prefix = append(prefix, `,"record_id":`...)

	varying := make([]byte, 0, len(records)*160)
	ends := make([]int, len(records))
	for index := range records {
		record := &records[index]
		id, err := CanonicalJSONV2(record.RecordID)
		if err != nil {
			return "", false
		}
		values, err := CanonicalJSONV2(record.Values)
		if err != nil {
			return "", false
		}
		varying = append(varying, id...)
		varying = append(varying, `,"source_time":`...)
		varying = strconv.AppendInt(varying, record.SourceTime, 10)
		varying = append(varying, `,"values":`...)
		varying = append(varying, values...)
		varying = append(varying, '}')
		ends[index] = len(varying)
	}
	// "[" + records joined by "," + "]".
	total := 2 + len(records)*len(prefix) + len(varying) + len(records) - 1
	if uint64(total) > math.MaxUint32 {
		return "", false
	}
	// The framing deriveLengthPrefixedSHA256 gives the domain and the
	// document: each length-prefixed, big-endian, in that order.
	hash := sha256.New()
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(domain)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(domain))
	binary.BigEndian.PutUint32(length[:], uint32(total))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte{'['})
	start := 0
	for index := range records {
		if index > 0 {
			_, _ = hash.Write([]byte{','})
		}
		_, _ = hash.Write(prefix)
		_, _ = hash.Write(varying[start:ends[index]])
		start = ends[index]
	}
	_, _ = hash.Write([]byte{']'})
	return hex.EncodeToString(hash.Sum(nil)), true
}

// dimensionIdentityPart is the canonical encoding of a record's dimension
// identity. When encoded is that identity's own -- the same digest, and the
// very list it was encoded from -- the fields' encoding is encoded's and only
// the object around it is written: the keys in canonical order, digest before
// fields, and the digest, lowercase hexadecimal, as the string it is.
// Anything else is encoded whole.
func dimensionIdentityPart(identity DimensionIdentityV2, encoded DimensionIdentityEncodingV2) ([]byte, error) {
	if encoded.canonical == nil || encoded.Digest != identity.Digest || !sha256Pattern.MatchString(identity.Digest) ||
		!sameSlice(encoded.fields, identity.Fields) {
		return CanonicalJSONV2(identity)
	}
	part := make([]byte, 0, len(encoded.canonical)+len(identity.Digest)+24)
	part = append(part, `{"digest":"`...)
	part = append(part, identity.Digest...)
	part = append(part, `","fields":`...)
	part = append(part, encoded.canonical...)
	return append(part, '}'), nil
}

// sameSlice is whether two field lists are the one list: the records of a
// series are built pointing at it, and equal contents in different arrays
// are left to the generic path rather than compared.
func sameSlice(left, right []DimensionFieldV2) bool {
	return len(left) == len(right) && unsafe.SliceData(left) == unsafe.SliceData(right)
}

// sameMap is whether two maps are the one map, as sameSlice.
func sameMap(left, right map[string]json.RawMessage) bool {
	return reflect.ValueOf(left).UnsafePointer() == reflect.ValueOf(right).UnsafePointer()
}

// recordsShadowStride is how often DeriveRecordsDigestV2 checks itself
// against the generic derivation: one call in this many. The comparison
// costs one generic derivation, the thing the fast path exists to avoid, so
// at one in 4096 it adds under a thousandth of what the fast path saves.
var recordsShadowStride uint64 = 4096

var (
	recordsShadowTick     atomic.Uint64
	recordsShadowCompared atomic.Uint64
	recordsShadowDiffered atomic.Uint64
)

func shouldSampleRecordsShadow() bool {
	stride := recordsShadowStride
	return stride != 0 && recordsShadowTick.Add(1)%stride == 0
}

// ReadRecordsDigestShadowCounts is how many DeriveRecordsDigestV2 calls were
// checked against the generic derivation, and on how many the two differed.
func ReadRecordsDigestShadowCounts() (compared, differed uint64) {
	return recordsShadowCompared.Load(), recordsShadowDiffered.Load()
}

// RecordsDigestDivergence is one checked call on which the two derivations
// differed: the assembled digest, and the generic one that was returned.
type RecordsDigestDivergence struct {
	Domain      string
	Records     int
	Served      string
	Established string
}

var (
	recordsDivergenceReporter atomic.Pointer[func(RecordsDigestDivergence)]
	recordsDivergenceReported atomic.Int64
)

// recordsDivergenceReportInterval bounds how often a divergence is
// reported: the count says how many, the report says what one looked like.
const recordsDivergenceReportInterval = time.Minute

// SetRecordsDigestDivergenceReporter installs what hears of a divergence, at
// most once per recordsDivergenceReportInterval. Nil removes it.
func SetRecordsDigestDivergenceReporter(report func(RecordsDigestDivergence)) {
	if report == nil {
		recordsDivergenceReporter.Store(nil)
		return
	}
	recordsDivergenceReporter.Store(&report)
}

func reportRecordsDivergence(divergence RecordsDigestDivergence) {
	report := recordsDivergenceReporter.Load()
	if report == nil {
		return
	}
	now := time.Now().UnixNano()
	last := recordsDivergenceReported.Load()
	if last != 0 && now-last < int64(recordsDivergenceReportInterval) {
		return
	}
	if !recordsDivergenceReported.CompareAndSwap(last, now) {
		return
	}
	(*report)(divergence)
}
