package worker

import (
	"reflect"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// retainedStateResultBytes is what a Slot's pending state results cost it,
// counting the memory this round allocated rather than every byte reachable
// from them.
//
// The generic sizer below is wrong for exactly one field, and that field is
// the largest one in the Slot. A mutation's Points is the whole retained
// window: appendHistoryPoint rebuilds the window on every record, so the
// point array and the fact arrays inside it are allocated here. The strings
// are not. RecordID is copied from the loaded history as a header, and
// DetectFingerprint and Result are Plan level constants that every point of
// every series shares one backing array for. Charging their contents once per
// point charges the Slot for bytes nothing allocated, and the overcharge grows
// with retention times series while the memory does not.
//
// Measured on the shape that drove this: a two-level Plan retaining 1469
// points was charged 989,739 bytes per mutation, of which 564 KiB was string
// content shared with the loaded history. Two hundred mutations of that put a
// Slot near a pool limit it was nowhere close to, and the refusal lands on
// RESOURCE_HARD_STOP - which is to say the accounting stopped detection on
// memory that was never held.
//
// What remains is deliberately still conservative: every point's fact array is
// counted, though only the newest point's was allocated this round - the older
// ones are headers into the loaded history. Distinguishing them here would
// mean knowing which point is new, which this layer does not, and a budget is
// the wrong place to guess low.
func retainedStateResultBytes(results []execution.StateEvaluation) uint64 {
	total := uint64(cap(results)) * uint64(unsafe.Sizeof(execution.StateEvaluation{}))
	for index := range results {
		total += retainedStateMutationBytes(results[index].Mutation)
		total += retainedObjectBytes(results[index].Events)
		total += retainedObjectBytes(results[index].WithoutMessage)
	}
	return total
}

func retainedStateMutationBytes(mutation execution.StateMutation) uint64 {
	// Every field but the history, counted exactly as it always was. Blanking
	// the one field rather than listing the others keeps this from silently
	// dropping a field somebody adds later.
	withoutHistory := mutation
	withoutHistory.Points = nil
	// BaseHistory is blanked and never charged, which is the one place this
	// accounting deliberately counts zero for something that is reachable.
	//
	// It is the history the Slot loaded, held by the StatePreflightResult for
	// as long as the Slot runs - the result contract reads it after every
	// series is evaluated, and the apply needs it. The mutation points at that
	// slice; referencing it allocates nothing and frees nothing sooner. Before
	// this field existed the mutation carried a rebuilt copy of the same window
	// and that copy was charged, correctly, because it was a second array.
	//
	// So the charge dropping is the copy going away, not a window escaping the
	// budget. What has to stay true for that to hold is that nobody makes
	// BaseHistory a slice of its own; two tests pin the pair - a long base
	// costs no more than an empty one, and the round's own points still cost.
	withoutHistory.BaseHistory = nil
	total := retainedObjectBytes(withoutHistory)
	total += uint64(cap(mutation.Points)) * uint64(unsafe.Sizeof(execution.StateHistoryPoint{}))
	for index := range mutation.Points {
		total += uint64(cap(mutation.Points[index].Levels)) * uint64(unsafe.Sizeof(execution.StateLevelFact{}))
	}
	return total
}

// retainedObjectBytes accounts for the acyclic State/Gap/Event DTOs, without
// serializing and copying their payloads. It includes slice capacity and map
// storage, unlike wire length. Shared input Datasets are accounted once by
// SeriesDelivery and must not be passed here. Repeated output references are
// conservatively counted; allocator rounding and GC headroom remain part of
// the process capacity profile, not a claim that this is runtime.MemStats.
func retainedObjectBytes(object any) uint64 {
	value := reflect.ValueOf(object)
	if !value.IsValid() {
		return 0
	}
	return uint64(value.Type().Size()) + retainedChildrenBytes(value)
}

func retainedChildrenBytes(value reflect.Value) uint64 {
	switch value.Kind() {
	case reflect.String:
		return uint64(value.Len())
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return 0
		}
		element := value.Elem()
		return uint64(element.Type().Size()) + retainedChildrenBytes(element)
	case reflect.Slice:
		total := uint64(value.Cap()) * uint64(value.Type().Elem().Size())
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return total
		}
		for index := 0; index < value.Len(); index++ {
			total += retainedChildrenBytes(value.Index(index))
		}
		return total
	case reflect.Array, reflect.Struct:
		var total uint64
		if value.Kind() == reflect.Array {
			for index := 0; index < value.Len(); index++ {
				total += retainedChildrenBytes(value.Index(index))
			}
		} else {
			for index := 0; index < value.NumField(); index++ {
				total += retainedChildrenBytes(value.Field(index))
			}
		}
		return total
	case reflect.Map:
		if value.IsNil() {
			return 0
		}
		// Allow two storage generations during map growth, including sparse
		// buckets. These DTO maps are append-only during construction.
		total := uint64(256) + uint64(value.Len())*4*(uint64(value.Type().Key().Size()+value.Type().Elem().Size())+16)
		iterator := value.MapRange()
		for iterator.Next() {
			total += retainedChildrenBytes(iterator.Key()) + retainedChildrenBytes(iterator.Value())
		}
		return total
	default:
		return 0
	}
}
