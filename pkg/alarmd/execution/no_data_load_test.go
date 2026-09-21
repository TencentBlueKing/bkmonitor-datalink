// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func noDataIdentity(strategy string) PlanNoDataIdentity {
	return PlanNoDataIdentity{
		Plan:            PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategy},
		StateGeneration: "generation-1",
	}
}

func noDataLoadRequest(identities ...PlanNoDataIdentity) NoDataLoadRequest {
	items := make([]PlanNoDataLoadItem, 0, len(identities))
	for _, identity := range identities {
		items = append(items, PlanNoDataLoadItem{
			Identity: identity, ApplyVersion: noDataApplyVersion(), ScheduleRevision: "revision-1",
		})
	}
	return NoDataLoadRequest{Items: items}
}

func foundSnapshot(identity PlanNoDataIdentity, groups ...NoDataGroupMemory) NoDataMemorySnapshot {
	return NoDataMemorySnapshot{
		Identity:                identity,
		MarkerRevision:          4,
		PersistedApplyVersion:   noDataApplyVersion(),
		PersistedMutationDigest: "digest",
		Status:                  NoDataMemoryFound,
		SchemaVersion:           NoDataMemorySchemaV1,
		LastScheduleRevision:    "revision-1",
		RosterVersion:           "TARGET_STATIC/1",
		Representation:          NoDataRepresentationPerGroup,
		Groups:                  groups,
	}
}

func TestNoDataLoadAcceptsAStoredMemory(t *testing.T) {
	identity := noDataIdentity("7")
	request := noDataLoadRequest(identity)
	result := NoDataLoadResult{Items: []NoDataMemorySnapshot{
		foundSnapshot(identity,
			NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
			NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
		),
	}}
	if err := ValidateNoDataLoad(request, result); err != nil {
		t.Fatalf("ValidateNoDataLoad() = %v", err)
	}
	if _, ok := result.Find(identity); !ok {
		t.Fatal("Find() did not return the snapshot it validated")
	}
}

// A Plan that has never written a memory is the normal state before its first
// no-data round, and it loads as empty rather than as an error.
func TestNoDataLoadAcceptsAPlanWithNoStoredMemory(t *testing.T) {
	identity := noDataIdentity("7")
	result := NoDataLoadResult{Items: []NoDataMemorySnapshot{
		{Identity: identity, Status: NoDataMemoryMissing},
	}}
	if err := ValidateNoDataLoad(noDataLoadRequest(identity), result); err != nil {
		t.Fatalf("ValidateNoDataLoad() = %v", err)
	}
}

// A record written by a newer build than this one comes back with its schema
// and nothing else.
//
// The schema is kept because it is the only thing that names the build that
// wrote the record. The payload is refused because a reader that took the
// groups out of a shape it has no definition for would be guessing - and the
// fields it silently dropped would be exactly the ones the newer schema added.
func TestNoDataLoadRefusesAPayloadItCannotRead(t *testing.T) {
	identity := noDataIdentity("7")
	request := noDataLoadRequest(identity)
	unreadable := NoDataMemorySnapshot{
		Identity: identity, Status: NoDataMemoryUnreadable,
		SchemaVersion: MaxSupportedNoDataMemorySchema + 1,
		ReasonCode:    observability.ReasonCode("STATE_SCHEMA_UNSUPPORTED"),
	}
	if err := ValidateNoDataLoad(request, NoDataLoadResult{Items: []NoDataMemorySnapshot{unreadable}}); err != nil {
		t.Fatalf("ValidateNoDataLoad() = %v; an unreadable record is a state, not a failure", err)
	}

	carrying := unreadable
	carrying.Groups = []NoDataGroupMemory{{GroupKey: "a", LastSeen: 90}}
	if err := ValidateNoDataLoad(request, NoDataLoadResult{Items: []NoDataMemorySnapshot{carrying}}); err == nil {
		t.Fatal("ValidateNoDataLoad() accepted groups decoded out of a record this build cannot read")
	}

	// And a schema this build can read must not arrive labelled unreadable:
	// that would pause a Plan's no-data detection over a record that was fine.
	readable := unreadable
	readable.SchemaVersion = NoDataMemorySchemaV1
	if err := ValidateNoDataLoad(request, NoDataLoadResult{Items: []NoDataMemorySnapshot{readable}}); err == nil {
		t.Fatal("ValidateNoDataLoad() accepted a readable schema reported as unreadable")
	}
}

// Nothing but the schema survives a record this build cannot read, and nothing
// at all survives a Plan that never wrote one - field by field, read off the
// snapshot rather than listed here.
//
// Listing the fields would guard the ones someone thought of. A field added to
// the snapshot later and not added to the payload check would be carried
// through both states in silence: for UNREADABLE that is data decoded out of a
// shape this build has no definition for, and for MISSING it is a payload
// attached to a record that does not exist. Walking the type means adding a
// field fails here until somebody says which kind it is.
func TestNoDataLoadKeepsNothingButTheSchemaOnARecordItCannotUse(t *testing.T) {
	identity := noDataIdentity("7")
	request := noDataLoadRequest(identity)
	// The fields each state legitimately carries, and so the ones a payload
	// check must not refuse.
	carried := map[string]bool{"Identity": true, "Status": true, "SchemaVersion": true, "ReasonCode": true}

	for _, state := range []struct {
		name string
		base NoDataMemorySnapshot
	}{
		{
			name: "unreadable",
			base: NoDataMemorySnapshot{
				Identity: identity, Status: NoDataMemoryUnreadable,
				SchemaVersion: MaxSupportedNoDataMemorySchema + 1,
				ReasonCode:    observability.ReasonCode("STATE_SCHEMA_UNSUPPORTED"),
			},
		},
		{
			name: "missing",
			base: NoDataMemorySnapshot{Identity: identity, Status: NoDataMemoryMissing},
		},
	} {
		// The base state has to be accepted, or every field below would "fail"
		// for a reason that has nothing to do with the field.
		if err := ValidateNoDataLoad(request, NoDataLoadResult{Items: []NoDataMemorySnapshot{state.base}}); err != nil {
			t.Fatalf("%s: the state itself is refused, so this test proves nothing about its fields: %v",
				state.name, err)
		}
		snapshotType := reflect.TypeOf(NoDataMemorySnapshot{})
		checked := 0
		for index := 0; index < snapshotType.NumField(); index++ {
			field := snapshotType.Field(index)
			if carried[field.Name] {
				continue
			}
			checked++
			t.Run(state.name+"/"+field.Name, func(t *testing.T) {
				carrying := state.base
				reflect.ValueOf(&carrying).Elem().Field(index).Set(nonZeroFor(t, field.Type))
				if err := ValidateNoDataLoad(request,
					NoDataLoadResult{Items: []NoDataMemorySnapshot{carrying}}); err == nil {
					t.Fatalf("a %s record carrying %s was accepted", state.name, field.Name)
				}
			})
		}
		if checked == 0 {
			t.Fatalf("%s: no payload field was checked; the carried list covers the whole type", state.name)
		}
	}
}

// nonZeroFor is a value of the given type that is not its zero, so that setting
// it onto a snapshot makes the payload check see something.
func nonZeroFor(t *testing.T, fieldType reflect.Type) reflect.Value {
	t.Helper()
	switch fieldType {
	case reflect.TypeOf(ApplyVersion{}):
		return reflect.ValueOf(noDataApplyVersion())
	case reflect.TypeOf([]NoDataGroupMemory(nil)):
		return reflect.ValueOf([]NoDataGroupMemory{{GroupKey: "a", LastSeen: 90}})
	}
	switch fieldType.Kind() {
	case reflect.String:
		return reflect.ValueOf("something").Convert(fieldType)
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(uint64(4)).Convert(fieldType)
	case reflect.Int, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(4)).Convert(fieldType)
	}
	t.Fatalf("no non-zero value defined for %s; this test cannot check that field", fieldType)
	return reflect.Value{}
}

// The reverse of the same confusion: a record this build cannot read must not
// arrive as FOUND, because FOUND is what makes the worker trust the payload.
func TestNoDataLoadRefusesAFoundRecordInAnUnreadableSchema(t *testing.T) {
	identity := noDataIdentity("7")
	snapshot := foundSnapshot(identity, NoDataGroupMemory{GroupKey: "a", LastSeen: 90})
	snapshot.SchemaVersion = MaxSupportedNoDataMemorySchema + 1
	if err := ValidateNoDataLoad(noDataLoadRequest(identity),
		NoDataLoadResult{Items: []NoDataMemorySnapshot{snapshot}}); err == nil {
		t.Fatal("ValidateNoDataLoad() accepted a record this build cannot read as FOUND")
	}
}

// A failure carries no payload, whichever kind it is. A retryable read that
// came back with groups would be a half-read record presented as the memory.
func TestNoDataLoadRefusesPayloadOnAFailure(t *testing.T) {
	identity := noDataIdentity("7")
	for name, status := range map[string]NoDataLoadStatus{
		"unavailable": NoDataMemoryUnavailable,
		"terminal":    NoDataMemoryTerminal,
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := foundSnapshot(identity, NoDataGroupMemory{GroupKey: "a", LastSeen: 90})
			snapshot.Status = status
			if err := ValidateNoDataLoad(noDataLoadRequest(identity),
				NoDataLoadResult{Items: []NoDataMemorySnapshot{snapshot}}); err == nil {
				t.Fatalf("ValidateNoDataLoad() accepted a %s result carrying a payload", status)
			}
		})
	}
}

// A stored memory is read back in the order it was written in. Accepting an
// unsorted one here would let a record whose groups were reordered validate,
// and the digest that names it was derived from the sorted form.
func TestNoDataLoadRefusesAStoredMemoryOutOfOrder(t *testing.T) {
	identity := noDataIdentity("7")
	for name, groups := range map[string][]NoDataGroupMemory{
		"out of order": {{GroupKey: "b", FirstAbsent: 100}, {GroupKey: "a", LastSeen: 90}},
		"duplicated":   {{GroupKey: "a", LastSeen: 90}, {GroupKey: "a", FirstAbsent: 100}},
		"no key":       {{GroupKey: "", LastSeen: 90}},
		"remembers nothing": {
			{GroupKey: "a"},
		},
		"negative": {{GroupKey: "a", LastSeen: -1}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateNoDataLoad(noDataLoadRequest(identity),
				NoDataLoadResult{Items: []NoDataMemorySnapshot{foundSnapshot(identity, groups...)}}); err == nil {
				t.Fatal("ValidateNoDataLoad() accepted a stored memory it should have refused")
			}
		})
	}
}

// Every Plan asked about is answered exactly once. A store that dropped one
// would otherwise leave that Plan loading as "no memory" - which is the state
// that restarts its absence clock.
func TestNoDataLoadRefusesAMismatchedResultSet(t *testing.T) {
	first, second := noDataIdentity("7"), noDataIdentity("8")
	found := foundSnapshot(first, NoDataGroupMemory{GroupKey: "a", LastSeen: 90})

	for name, test := range map[string]struct {
		request NoDataLoadRequest
		result  NoDataLoadResult
	}{
		"short": {
			request: noDataLoadRequest(first, second),
			result:  NoDataLoadResult{Items: []NoDataMemorySnapshot{found}},
		},
		"unknown identity": {
			request: noDataLoadRequest(first),
			result:  NoDataLoadResult{Items: []NoDataMemorySnapshot{foundSnapshot(second)}},
		},
		"duplicate answer": {
			request: noDataLoadRequest(first, second),
			result:  NoDataLoadResult{Items: []NoDataMemorySnapshot{found, found}},
		},
		"duplicate request": {
			request: noDataLoadRequest(first, first),
			result:  NoDataLoadResult{Items: []NoDataMemorySnapshot{found, found}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateNoDataLoad(test.request, test.result); err == nil {
				t.Fatal("ValidateNoDataLoad() accepted a result that does not answer the request")
			}
		})
	}
}

func TestNoDataLoadRefusesAnUnknownStatus(t *testing.T) {
	identity := noDataIdentity("7")
	snapshot := NoDataMemorySnapshot{Identity: identity, Status: NoDataLoadStatus("SOMETHING_ELSE")}
	if err := ValidateNoDataLoad(noDataLoadRequest(identity),
		NoDataLoadResult{Items: []NoDataMemorySnapshot{snapshot}}); err == nil {
		t.Fatal("ValidateNoDataLoad() accepted a status it does not define")
	}
}
