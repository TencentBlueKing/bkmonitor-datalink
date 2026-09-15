package metadata

import "sync/atomic"

// FTAEventTagsV1 maps logical tags.<key> fields to the FTA event nested key/value schema.
const FTAEventTagsV1 = "fta_event_tags/v1"

// FieldSemanticsExecution is request-local proof of successful ES evaluation.
// Copies of a physical query share this receipt; it is never serialized.
type FieldSemanticsExecution struct{ completed atomic.Bool }

func (e *FieldSemanticsExecution) Complete() {
	if e != nil {
		e.completed.Store(true)
	}
}
func (e *FieldSemanticsExecution) Completed() bool { return e != nil && e.completed.Load() }
