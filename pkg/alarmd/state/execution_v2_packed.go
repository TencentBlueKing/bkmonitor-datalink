// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const executionStateSchemaV3 = "alarmd-runtime-state-v3"

// packedLevel is one Level's scalar state plus the detect fingerprint the
// envelope used to repeat on every point.
//
// The fingerprint is hoisted here rather than dropped because two readers
// compare it: the result contract compares the whole Level fact, and the
// load-time contract check compares the stored fingerprint against the current
// one. Hoisting is lossless because the key guarantees it: the state
// generation's closure covers each Level's detect fingerprint, so a Plan whose
// fingerprint moves is a different generation and a different key, and within
// one key a Level has exactly one.
type packedLevel struct {
	Mutation          execution.RuntimeLevelStateMutation `json:"mutation"`
	DetectFingerprint string                              `json:"detect_fingerprint"`
}

// packedHeader is the runtime envelope with the history taken out.
type packedHeader struct {
	Schema         string                     `json:"schema"`
	Identity       execution.StateKeyIdentity `json:"identity"`
	BlobRevision   uint64                     `json:"blob_revision"`
	ApplyVersion   execution.ApplyVersion     `json:"apply_version"`
	MutationDigest execution.MutationDigest   `json:"mutation_digest"`
	LastEventTime  int64                      `json:"last_event_time"`
	SeriesGuard    *execution.StateGuardFact  `json:"series_guard,omitempty"`
	Levels         []packedLevel              `json:"levels"`
	PointCount     int                        `json:"point_count"`
	// LegacyRecordIDs carries the ids of points the derivation cannot rebuild,
	// by their position in the history. Absent for every record whose points
	// all derive, which is the ordinary case and costs those records nothing.
	//
	// It exists because the envelope stored ids verbatim and never checked
	// them, so state written before this representation can hold points whose
	// id is not DeriveRecordIDV2(series, source time). Rebuilding those from
	// the derivation would hand back a different record than was stored, and
	// refusing them refuses the whole record on every round for as long as the
	// point is retained - which is a Plan that never writes state again.
	LegacyRecordIDs []packedLegacyRecordID `json:"legacy_record_ids,omitempty"`
}

// packedLegacyRecordID is one stored id the derivation cannot rebuild.
type packedLegacyRecordID struct {
	Index    int    `json:"index"`
	RecordID string `json:"record_id"`
}

// ErrPackedContract is a write refused for disagreeing with what the framed
// record can represent. It is a deterministic refusal, never a retry: the same
// bytes would be refused again.
var ErrPackedContract = errors.New("state: runtime record cannot be framed")

// The rules a framed write can be refused by, as bounded names.
//
// A closed vocabulary rather than the error's sentence: the refusal reaches
// the admission line, and a line carrying free text cannot be grouped or
// counted, and carries whatever the values happened to be. Every rule below
// appears in exactly one refusal, and PackedRuleNames is what a reader may
// see - a rule added without a name here reaches the line as empty, which the
// case on that list refuses.
const (
	PackedRuleLevelNotInMutation   = "level_not_in_mutation"
	PackedRuleNoDetectFingerprint  = "no_detect_fingerprint"
	PackedRuleTwoFingerprints      = "two_fingerprints_for_one_level"
	PackedRuleDuplicateLevel       = "duplicate_level"
	PackedRuleSourceTimeNotRising  = "source_time_not_rising"
	PackedRuleRecordIDUnderivable  = "record_id_underivable"
	PackedRuleRecordIDNotDerived   = "record_id_not_derived"
	PackedRuleUnencodableFactState = "unencodable_fact_state"
	// PackedRuleMutationDigestMismatch is not one of the framing rules: the
	// store refuses the mutation before it frames anything, because the digest
	// the producer computed does not cover the content it sent. It is named
	// here because it reaches the line under the same reason as the eight, and
	// an unnamed ninth way is exactly what makes the other eight worth naming.
	PackedRuleMutationDigestMismatch = "mutation_digest_mismatch"
	// PackedRuleIdentityKeyUnderivable is the store refusing before it frames
	// or writes anything: the mutation's identity does not produce a key. Its
	// own name rather than sharing the digest's, because the two send a reader
	// to different places - one to what the producer computed, one to the
	// identity it computed it for.
	PackedRuleIdentityKeyUnderivable = "identity_key_underivable"
	// PackedRuleLegacyRecordIDTooLong is an inherited id too wide for the
	// upper bound the compile-time ceiling is derived from. Every id this
	// deployment has ever written is 64 hexadecimal characters - the
	// derivation produces that, and so does the dimension digest the pre
	// derivation producer stored - so the bound costs each carried id at that
	// width. Refusing anything wider is what makes that a property of the
	// writer rather than an assumption about the data: without it one wider id
	// would put a record over a ceiling that had already admitted its Plan,
	// and the bound would quietly stop being a bound again.
	PackedRuleLegacyRecordIDTooLong = "legacy_record_id_too_long"
	// PackedRuleTwoRecordsOneSourceTime is the merge of this round's points
	// into the loaded record finding two different records at one source time.
	// The producer names the same thing first and refuses there; it is named
	// again here because this is where the two are actually brought together,
	// and a merge that picked one of them would decide a record's past by which
	// side of the merge it arrived on.
	PackedRuleTwoRecordsOneSourceTime = "two_records_one_source_time"
)

// MaxLegacyRecordIDLength is the width the upper bound costs a carried id at,
// and the width the encoder refuses beyond.
//
// Not a round number chosen for comfort: it is the widest id the ceiling can
// afford. Every extra character is paid once per point, so raising this lowers
// the derived ceiling for every record. Measured against a 512 KiB budget, a
// nine Level Plan admits 4680 points at this width and 4085 at 80 - below the
// configured max_required_history_points of 4096, which would make the ceiling
// refuse Plans that run today. So a wider id is not something this record can
// be made to hold by relaxing the check; it does not fit, and the refusal says
// so. The config ceiling baseline moves if this constant does, which is the
// coupling being relied on rather than repeated here.
const MaxLegacyRecordIDLength = 64

// PackedRuleNames is every rule a framed write can be refused by.
var PackedRuleNames = []string{
	PackedRuleLevelNotInMutation, PackedRuleNoDetectFingerprint, PackedRuleTwoFingerprints,
	PackedRuleDuplicateLevel, PackedRuleSourceTimeNotRising, PackedRuleRecordIDUnderivable,
	PackedRuleRecordIDNotDerived, PackedRuleUnencodableFactState, PackedRuleMutationDigestMismatch,
	PackedRuleIdentityKeyUnderivable, PackedRuleLegacyRecordIDTooLong, PackedRuleTwoRecordsOneSourceTime,
}

// PackedContractRefusal is a framed write refused by one named rule. The
// sentence stays for a human reading the error; the rule is what the line
// carries.
type PackedContractRefusal struct {
	Rule   string
	Detail string
}

func (err *PackedContractRefusal) Error() string {
	return fmt.Sprintf("%s: %s (%s)", ErrPackedContract.Error(), err.Detail, err.Rule)
}

func (err *PackedContractRefusal) Unwrap() error { return ErrPackedContract }

// PackedRefusalRule is the rule that refused a framed write, empty when err is
// not one of those refusals.
func PackedRefusalRule(err error) string {
	var refusal *PackedContractRefusal
	if !errors.As(err, &refusal) || refusal == nil {
		return ""
	}
	return refusal.Rule
}

func packedRefusal(rule, format string, args ...any) error {
	return &PackedContractRefusal{Rule: rule, Detail: fmt.Sprintf(format, args...)}
}

// walkRecordPoints visits the record this mutation leaves behind, oldest
// first: the history it was built against with this round's points merged in,
// bounded by the retention the mutation carries.
//
// It allocates nothing and holds no slice of its own. That is the point of it:
// a mutation names one new point, and building the window here rather than at
// the producer is what removes the per-round rebuild of the whole window. A
// caller that materializes what this emits has put the allocation back.
func walkRecordPoints(mutation execution.StateMutation, visit func(execution.StateHistoryPoint) error) error {
	err := execution.WalkMergedHistory(mutation.BaseHistory, mutation.Points, mutation.RetentionPoints, visit)
	var conflict *execution.HistoryRecordIdentityConflict
	if errors.As(err, &conflict) {
		return packedRefusal(PackedRuleTwoRecordsOneSourceTime,
			"source time %d is claimed by %s in the record and %s in this round", conflict.SourceTime, conflict.Stored, conflict.Fresh)
	}
	return err
}

// seriesRecordIDs derives the record ids of one series' points, point by
// point, on one deriver built at the first point. Every point of a record is
// derived on both its read and its write, and deriving each from scratch was
// what reading a series with fourteen hundred retained points mostly cost.
// Built lazily so a record with no points refuses nothing it did not before,
// and a series the derivation refuses is refused at the point that asked,
// with the same error DeriveRecordIDV2 gives.
func seriesRecordIDs(series string) func(int64) (string, error) {
	var deriver *contract.RecordIDDeriverV2
	return func(sourceTime int64) (string, error) {
		if deriver == nil {
			built, err := contract.NewRecordIDDeriverV2(series)
			if err != nil {
				return "", err
			}
			deriver = built
		}
		return deriver.Derive(sourceTime)
	}
}

// levelFingerprints picks each Level's one detect fingerprint out of the points
// and refuses a mutation whose points disagree with each other.
//
// Refused here rather than at the reader. The window already treats a point
// whose fingerprint differs from its Level's as an invariant violation, but it
// only says so when the record is read back, which is a round later and in a
// different Slot than the one that produced it. Hoisting the value makes the
// disagreement unrepresentable, so it has to be named where it is created.
func levelFingerprints(mutation execution.StateMutation, levels []execution.RuntimeLevelStateMutation) ([]string, error) {
	fingerprints := make([]string, len(levels))
	position := make(map[uint32]int, len(levels))
	for index, level := range levels {
		position[level.LevelID] = index
	}
	// Over the record the write stores, not over the points the round added:
	// the header carries one fingerprint per Level for the whole record, and an
	// inherited point disagreeing with a fresh one is exactly the disagreement
	// this refuses.
	if err := walkRecordPoints(mutation, func(point execution.StateHistoryPoint) error {
		for _, fact := range point.Levels {
			index, known := position[fact.LevelID]
			if !known {
				return packedRefusal(PackedRuleLevelNotInMutation,
					"point at %d names Level %d, which the mutation does not carry", point.SourceTime, fact.LevelID)
			}
			if fact.DetectFingerprint == "" {
				return packedRefusal(PackedRuleNoDetectFingerprint,
					"point at %d carries no detect fingerprint for Level %d", point.SourceTime, fact.LevelID)
			}
			if fingerprints[index] == "" {
				fingerprints[index] = fact.DetectFingerprint
				continue
			}
			if fingerprints[index] != fact.DetectFingerprint {
				return packedRefusal(PackedRuleTwoFingerprints,
					"Level %d has two detect fingerprints in one record", fact.LevelID)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return fingerprints, nil
}

// encodeRuntimePacked writes the framed record.
func encodeRuntimePacked(mutation execution.StateMutation, revision uint64) ([]byte, error) {
	encoded, _, err := encodeRuntimePackedCounted(mutation, revision)
	return encoded, err
}

// encodeRuntimePackedCounted also reports how many of the record's points
// carried an id the derivation could not rebuild, which is how the deployment
// learns how much such state it holds and which objects hold it.
func encodeRuntimePackedCounted(mutation execution.StateMutation, revision uint64) ([]byte, int, error) {
	levels := append([]execution.RuntimeLevelStateMutation(nil), mutation.Levels...)
	sort.Slice(levels, func(i, j int) bool { return levels[i].LevelID < levels[j].LevelID })
	for index := 1; index < len(levels); index++ {
		if levels[index].LevelID == levels[index-1].LevelID {
			return nil, 0, packedRefusal(PackedRuleDuplicateLevel, "Level %d appears twice", levels[index].LevelID)
		}
	}
	fingerprints, err := levelFingerprints(mutation, levels)
	if err != nil {
		return nil, 0, err
	}
	last := int64(0)
	for _, level := range levels {
		if level.LastProcessedEventTime > last {
			last = level.LastProcessedEventTime
		}
	}
	bitmapBytes := (len(levels) + 7) / 8
	index := make(map[uint32]int, len(levels))
	for position, level := range levels {
		index[level.LevelID] = position
	}
	// The anchors this round touched, which is what separates a point this
	// write is responsible for from one it inherited.
	affected := make(map[execution.RecordAnchor]struct{}, len(mutation.AffectedRecords))
	for _, anchor := range mutation.AffectedRecords {
		affected[anchor] = struct{}{}
	}
	var legacy []packedLegacyRecordID
	var points []byte
	previous := int64(0)
	pointIndex := -1
	pointCount := 0
	derive := seriesRecordIDs(string(mutation.Identity.SeriesIdentityDigest))
	if err := walkRecordPoints(mutation, func(point execution.StateHistoryPoint) error {
		pointIndex++
		pointCount++
		if point.SourceTime < 0 || (pointIndex > 0 && point.SourceTime <= previous) {
			return packedRefusal(PackedRuleSourceTimeNotRising, "points must rise strictly by source time")
		}
		// The id is not stored. It is checked here against the derivation every
		// producer uses, so a producer that stops deriving it is refused where
		// it writes rather than read back as a different record later.
		expected, deriveErr := derive(point.SourceTime)
		if deriveErr != nil {
			return packedRefusal(PackedRuleRecordIDUnderivable, "derive record id at %d: %v", point.SourceTime, deriveErr)
		}
		if point.RecordID != expected {
			// A point this round produced must derive: that is the check that
			// catches a producer which has stopped deriving, and it has to
			// refuse where the write happens or the record reads back as a
			// different one later.
			//
			// A point already in the history is not this round's to refuse.
			// The envelope stored ids verbatim and never checked them, so
			// state written before this representation can hold one that does
			// not derive, and refusing it refuses every write this Plan makes
			// from now on - the record is rewritten whole every round, so the
			// point never ages out of the refusal either. Its id is stored
			// instead, which is what the envelope did for every point.
			if _, thisRound := affected[execution.RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}]; thisRound {
				return packedRefusal(PackedRuleRecordIDNotDerived,
					"point at %d carries a record id the series identity and source time do not derive", point.SourceTime)
			}
			if len(point.RecordID) > MaxLegacyRecordIDLength {
				return packedRefusal(PackedRuleLegacyRecordIDTooLong,
					"point at %d carries a %d character record id, over the %d the bound is derived from",
					point.SourceTime, len(point.RecordID), MaxLegacyRecordIDLength)
			}
			legacy = append(legacy, packedLegacyRecordID{Index: pointIndex, RecordID: point.RecordID})
		}
		delta := uint64(point.SourceTime)
		if pointIndex > 0 {
			delta = uint64(point.SourceTime - previous)
		}
		points = appendUvarint(points, delta)
		present := make([]byte, bitmapBytes)
		valid := make([]byte, bitmapBytes)
		anomalous := make([]byte, bitmapBytes)
		for _, fact := range point.Levels {
			position := index[fact.LevelID]
			setBit(present, position, true)
			switch fact.Result {
			case execution.LevelFactNormal:
				setBit(valid, position, true)
			case execution.LevelFactAnomalous:
				setBit(valid, position, true)
				setBit(anomalous, position, true)
			case execution.LevelFactUnavailable:
			case execution.LevelFactError:
				setBit(anomalous, position, true)
			default:
				return packedRefusal(PackedRuleUnencodableFactState, "Level %d fact result %q", fact.LevelID, fact.Result)
			}
		}
		points = append(points, present...)
		points = append(points, valid...)
		points = append(points, anomalous...)
		previous = point.SourceTime
		return nil
	}); err != nil {
		return nil, 0, err
	}

	// The header is built after the points, because only encoding them says
	// which ids the derivation could not rebuild.
	header := packedHeader{
		Schema: executionStateSchemaV3, Identity: mutation.Identity, BlobRevision: revision,
		ApplyVersion: mutation.ApplyVersion, MutationDigest: mutation.MutationDigest, LastEventTime: last,
		SeriesGuard: mutation.SeriesGuard, Levels: make([]packedLevel, len(levels)),
		PointCount: pointCount, LegacyRecordIDs: legacy,
	}
	for index, level := range levels {
		header.Levels[index] = packedLevel{Mutation: level, DetectFingerprint: fingerprints[index]}
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, 0, err
	}
	// Only a record that needs the table is written at the newer schema, so a
	// build that knows only the older one goes on reading every record that
	// does not - and refuses these few by name instead of the population.
	schema := packedFrameSchemaV1
	if len(legacy) > 0 {
		schema = packedFrameSchemaV2
	}
	buffer := make([]byte, 0, len(headerBytes)+len(points)+packedFrameHeaderLen+binary.MaxVarintLen64)
	buffer = append(buffer, packedFrameMagic...)
	buffer = append(buffer, schema, packedFrameCodecNone)
	buffer = appendUvarint(buffer, uint64(len(headerBytes)))
	buffer = append(buffer, headerBytes...)
	return append(buffer, points...), len(legacy), nil
}

// packedFrame reports whether these bytes are a framed record rather than the
// JSON envelope. The two are told apart by their first bytes, so a reader needs
// no version field to dispatch.
func packedFrame(raw []byte) bool {
	return len(raw) >= packedFrameHeaderLen && string(raw[:4]) == packedFrameMagic
}

// decodeRuntimePacked reads the framed record back into the same view the JSON
// envelope produces, reconstructing each point's record id and each fact's
// detect fingerprint.
func decodeRuntimePacked(raw []byte, identity execution.StateKeyIdentity) (execution.RuntimeStateView, error) {
	if !packedFrame(raw) {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: not a framed record", ErrCorruptState)
	}
	if (raw[4] != packedFrameSchemaV1 && raw[4] != packedFrameSchemaV2) || raw[5] != packedFrameCodecNone {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: frame schema %d codec %d", ErrUnsupportedState, raw[4], raw[5])
	}
	headerLen, rest, err := consumeUvarint(raw[packedFrameHeaderLen:])
	if err != nil {
		return execution.RuntimeStateView{}, err
	}
	if headerLen > uint64(len(rest)) {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: truncated header", ErrCorruptState)
	}
	var header packedHeader
	if err := json.Unmarshal(rest[:headerLen], &header); err != nil {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: header: %v", ErrCorruptState, err)
	}
	if header.Schema != executionStateSchemaV3 {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: schema %q", ErrUnsupportedState, header.Schema)
	}
	legacyByIndex := make(map[int]string, len(header.LegacyRecordIDs))
	for _, entry := range header.LegacyRecordIDs {
		if entry.Index < 0 || entry.Index >= header.PointCount || entry.RecordID == "" {
			return execution.RuntimeStateView{}, fmt.Errorf("%w: legacy record id at %d", ErrCorruptState, entry.Index)
		}
		if _, duplicate := legacyByIndex[entry.Index]; duplicate {
			return execution.RuntimeStateView{}, fmt.Errorf("%w: legacy record id %d listed twice", ErrCorruptState, entry.Index)
		}
		legacyByIndex[entry.Index] = entry.RecordID
	}
	if header.Identity != identity || header.BlobRevision == 0 {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: identity or revision", ErrCorruptState)
	}
	rest = rest[headerLen:]

	bitmapBytes := (len(header.Levels) + 7) / 8
	levels := make([]execution.RuntimeLevelStateView, len(header.Levels))
	for index, level := range header.Levels {
		levels[index] = execution.RuntimeLevelStateView{LevelID: level.Mutation.LevelID,
			LevelStateCompatibility: level.Mutation.LevelStateCompatibility,
			HistoryCompleteness:     level.Mutation.HistoryCompleteness, GapReasonCode: level.Mutation.GapReasonCode,
			WarmupRequirementRef:   level.Mutation.WarmupRequirementRef,
			LastProcessedEventTime: level.Mutation.LastProcessedEventTime}
	}
	history := make([]execution.StateHistoryPoint, 0, header.PointCount)
	previous := int64(0)
	derive := seriesRecordIDs(string(identity.SeriesIdentityDigest))
	for point := 0; point < header.PointCount; point++ {
		delta, next, decodeErr := consumeUvarint(rest)
		if decodeErr != nil {
			return execution.RuntimeStateView{}, decodeErr
		}
		rest = next
		if len(rest) < 3*bitmapBytes {
			return execution.RuntimeStateView{}, fmt.Errorf("%w: truncated point %d", ErrCorruptState, point)
		}
		sourceTime := int64(delta)
		if point > 0 {
			sourceTime = previous + int64(delta)
		}
		present, valid, anomalous := rest[:bitmapBytes], rest[bitmapBytes:2*bitmapBytes], rest[2*bitmapBytes:3*bitmapBytes]
		rest = rest[3*bitmapBytes:]
		// The stored id wins where there is one: a point the derivation cannot
		// rebuild carries its id in the header, and handing back the derived
		// one instead would return a different record than was written.
		recordID, stored := legacyByIndex[point]
		if !stored {
			derived, deriveErr := derive(sourceTime)
			if deriveErr != nil {
				return execution.RuntimeStateView{}, fmt.Errorf("%w: derive record id: %v", ErrCorruptState, deriveErr)
			}
			recordID = derived
		}
		facts := make([]execution.StateLevelFact, 0, len(header.Levels))
		for position, level := range header.Levels {
			if !bitSet(present, position) {
				continue
			}
			result := execution.LevelFactUnavailable
			switch {
			case bitSet(valid, position) && bitSet(anomalous, position):
				result = execution.LevelFactAnomalous
			case bitSet(valid, position):
				result = execution.LevelFactNormal
			case bitSet(anomalous, position):
				result = execution.LevelFactError
			}
			facts = append(facts, execution.StateLevelFact{LevelID: level.Mutation.LevelID,
				DetectFingerprint: level.DetectFingerprint, Result: result})
		}
		history = append(history, execution.StateHistoryPoint{RecordID: recordID, SourceTime: sourceTime, Levels: facts})
		previous = sourceTime
	}
	if len(rest) != 0 {
		return execution.RuntimeStateView{}, fmt.Errorf("%w: %d trailing bytes", ErrCorruptState, len(rest))
	}
	return execution.RuntimeStateView{Identity: identity, BlobRevision: header.BlobRevision,
		PersistedApplyVersion: header.ApplyVersion, PersistedMutationDigest: header.MutationDigest,
		LastProcessedEventTime: header.LastEventTime, SeriesGuard: header.SeriesGuard,
		Levels: levels, History: history}, nil
}

// runtimeViewSource says which key a loaded view came from, so the write side
// knows whether it is continuing a framed record or converting an envelope.
type runtimeViewSource uint8

const (
	runtimeViewNone runtimeViewSource = iota
	runtimeViewFramed
	runtimeViewEnvelope
)

// chooseRuntimeView picks between the two representations of one series.
//
// Not "prefer the new key". Ownership of a Query Group moves between replicas
// while a rollout is in progress, and an old binary that takes a Query Group
// back writes the envelope key after a new one has already written the framed
// one - so the envelope can legitimately be the newer of the two, and taking
// the framed one on sight would throw away every round the old owner ran.
//
// Who still asks this. The sequential apply path does: it reads both keys to
// compare against exact bytes and has no per-Slot multiplier, so the second
// key costs it a value it already has the round trip for. The preflight does
// not any more - it asks for the envelope only of a series whose frame is
// missing or unreadable, so a frame that reads is the view whatever the older
// key holds. What that gives up is this function's first paragraph: during a
// rollout from a binary that predates the framed record, the rounds the old
// owner wrote are missing from the frame's history until the window slides
// past them. It is given up because the alternative is reading a 344 KB
// record for every series of every Slot for a representation nothing writes,
// and there is no reading today that says the shape still happens - the
// fleet-level fact that would settle it is every ready replica declaring it
// writes frames.
//
// Equal versions go to the framed key, and that choice is not arbitrary. Two
// records at one version describe the same evaluation and hold the same facts,
// so either is correct to read; but taking the envelope means deriving the
// framed record from it again next round, and the round after, for as long as
// both exist. The migration would never converge while looking entirely
// healthy.
func chooseRuntimeView(framed, envelope *execution.RuntimeStateView) (execution.RuntimeStateView, runtimeViewSource) {
	switch {
	case framed == nil && envelope == nil:
		return execution.RuntimeStateView{}, runtimeViewNone
	case framed == nil:
		return *envelope, runtimeViewEnvelope
	case envelope == nil:
		return *framed, runtimeViewFramed
	}
	switch execution.CompareApplyVersion(framed.PersistedApplyVersion, envelope.PersistedApplyVersion) {
	case execution.ApplyVersionPersistedOlder:
		return *envelope, runtimeViewEnvelope
	default:
		// Newer, and equal. CompareApplyVersion is a total order: when the
		// epoch and evaluation time match it falls back to comparing the Slot
		// digests, which is deterministic and the same on every replica but
		// says nothing about which happened later - so it is usable to break a
		// tie and must not be read as "this one is more recent".
		return *framed, runtimeViewFramed
	}
}
