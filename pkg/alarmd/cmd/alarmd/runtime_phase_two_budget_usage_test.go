package main

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every number a Slot's usage holds reaches the row, and reaches the field of
// the same name.
//
// Field by field over the whole struct rather than over the fields this change
// added. slotBudgetUsageFacts rebuilds its result from zero, so a field is
// carried only where somebody wrote the line that carries it: a field added to
// the source and not to the mapper compiles, passes every case that names the
// fields it knows about, and reports a confident zero. That is not a
// hypothetical - it is how the bytes a preflight read got as far as the line
// that reports them and arrived as nothing.
//
// Each source field gets a different value, so a mapper that copies the right
// number from the wrong field fails here too.
func TestEveryBudgetUsageNumberReachesTheRowItIsReportedOn(t *testing.T) {
	var usage execution.SlotBudgetUsage
	source := reflect.ValueOf(&usage).Elem()
	for index := range source.NumField() {
		field := source.Field(index)
		if field.Kind() != reflect.Uint64 {
			t.Fatalf("%s is a %s; this case gives every field a distinct number and only knows how to do that "+
				"for uint64", source.Type().Field(index).Name, field.Kind())
		}
		field.SetUint(uint64(index) + 1)
	}

	facts := slotBudgetUsageFacts(usage)
	if facts == nil {
		t.Fatal("a Slot's usage produced no facts to report")
	}
	rendered := reflect.ValueOf(facts).Elem()
	if rendered.NumField() != source.NumField() {
		t.Fatalf("the usage has %d fields and the row has %d; one of them carries something the other does not "+
			"and this case cannot say which", source.NumField(), rendered.NumField())
	}
	for index := range source.NumField() {
		name := source.Type().Field(index).Name
		carried := rendered.FieldByName(name)
		if !carried.IsValid() {
			t.Errorf("%s has no field of that name on the row, so nothing reports it", name)
			continue
		}
		if carried.Uint() != source.Field(index).Uint() {
			t.Errorf("%s = %d on the row, want the %d the Slot used", name, carried.Uint(), source.Field(index).Uint())
		}
	}
}

// The phases the row reports add up to the total it reports beside them.
//
// Read off the reported row rather than off the execution, because that is
// where the two are compared. A reader seeing a total that is not the sum has
// no way to tell which of the four numbers to believe.
func TestTheReportedPhasesAddUpToTheReportedTotal(t *testing.T) {
	facts := slotBudgetUsageFacts(execution.SlotBudgetUsage{
		RetainedBytes:      1638400 + 32768 + 67010560,
		RetainedInputBytes: 1638400, RetainedGapBytes: 32768, RetainedOutputBytes: 67010560,
	})
	sum := facts.RetainedInputBytes + facts.RetainedGapBytes + facts.RetainedOutputBytes
	if sum != facts.RetainedBytes {
		t.Fatalf("the row's phases sum to %d against its own total of %d", sum, facts.RetainedBytes)
	}
}
