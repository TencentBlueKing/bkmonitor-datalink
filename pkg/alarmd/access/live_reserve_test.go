package access

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The worker's measured post-query tail sizes the query deadline, but never takes a consumer out of execution: a
// tail that would leave the query no time falls back to the frozen reserve, as the plan was built.
func TestPrepareQueryDeadlineFollowsTheLiveReserveWithoutDroppingConsumers(t *testing.T) {
	ref, frozen := frozenExecution(t)
	live := &execution.LiveReserve{}
	consumer := frozen.Requirements[0].Consumers[0]
	frozenDeadline := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec

	prepare := func(t *testing.T) PlannedQuery {
		t.Helper()
		prepared, err := prepare(ref, frozen, 30*time.Second, false, live)
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared.Queries) != 1 || len(prepared.Queries[0].Requirements) != 1 || len(prepared.Queries[0].ReadinessInvalidRequirements) != 0 {
			t.Fatalf("the consumer must stay in execution: %+v", prepared.Queries)
		}
		return prepared.Queries[0]
	}

	live.Set(0)
	query := prepare(t)
	if query.DeadlineUnixMilli != frozenDeadline || query.FrozenDeadlineUnixMilli != frozenDeadline {
		t.Fatalf("no tail measured: deadline=%d frozen=%d, want both %d", query.DeadlineUnixMilli, query.FrozenDeadlineUnixMilli, frozenDeadline)
	}

	live.Set(time.Second)
	query = prepare(t)
	if want := consumer.ConsumerDeadlineUnixMilli - 1000; query.DeadlineUnixMilli != want {
		t.Fatalf("1 s tail: deadline=%d, want %d", query.DeadlineUnixMilli, want)
	}

	before := live.Fallbacks()
	live.Set(time.Duration(consumer.ConsumerDeadlineUnixMilli) * time.Millisecond)
	query = prepare(t)
	if query.DeadlineUnixMilli != frozenDeadline {
		t.Fatalf("a tail past the deadline: deadline=%d, want the frozen %d", query.DeadlineUnixMilli, frozenDeadline)
	}
	if live.Fallbacks() <= before {
		t.Fatal("the fallback must be counted")
	}
}
