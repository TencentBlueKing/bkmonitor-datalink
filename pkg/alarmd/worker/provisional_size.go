package worker

import "reflect"

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
