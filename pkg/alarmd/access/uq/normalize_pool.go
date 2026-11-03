package uq

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// normalizeWorkersBusy is how many goroutines every answer of this process
// has handed series to make canonical and that have not finished. There are
// at most one fewer than the process's CPUs - GOMAXPROCS, which is set to the
// container's limit at start - so they are the process's, not each answer's:
// while every CPU is busy with answers of their own, a takeover's, each answer
// makes its series canonical itself, as it always did, and none takes more
// CPU than its share. A process of one CPU hands nothing over.
var normalizeWorkersBusy atomic.Int64

// normalizeWorkers is how many there may be.
func normalizeWorkers() int64 {
	return int64(max(runtime.GOMAXPROCS(0)-1, 0))
}

// tryNormalizeWorker takes a worker when one is free, and false when none is.
func tryNormalizeWorker() bool {
	if normalizeWorkersBusy.Add(1) > normalizeWorkers() {
		normalizeWorkersBusy.Add(-1)
		return false
	}
	return true
}

func releaseNormalizeWorker() { normalizeWorkersBusy.Add(-1) }

// normalizeChunkSeries is the fewest series a worker is handed: fewer are
// made canonical faster than a goroutine is started and waited for.
const normalizeChunkSeries = 32

// normalizing is one series of an answer and what normalizeSeries made of it.
type normalizing struct {
	series       responseSeries
	encodedBytes uint64
	batch        execution.ProviderSeriesBatch
	nullFields   uint64
	err          error
}

// normalizeSideBySide makes every series of jobs canonical: in contiguous
// chunks, one on this goroutine and one on each worker free, or all here
// when none is. Each job keeps what its own series made, so the order they
// are handed on in is the order they came, whoever made them.
func normalizeSideBySide(jobs []normalizing, spec execution.PhysicalQuerySpec, ref execution.ProviderResultRef, receivedAt int64) {
	normalize := func(chunk []normalizing) {
		for index := range chunk {
			job := &chunk[index]
			job.batch, job.nullFields, job.err = normalizeSeries(spec, ref, job.series, receivedAt)
		}
	}
	chunks := 1
	for chunks < len(jobs)/normalizeChunkSeries && tryNormalizeWorker() {
		chunks++
	}
	if chunks == 1 {
		normalize(jobs)
		return
	}
	// Chunk k of n is the series from k/n to (k+1)/n of the way: every chunk
	// has a series, as there are at least normalizeChunkSeries for each, so
	// every worker taken is given back by the chunk it made.
	bound := func(chunk int) int { return chunk * len(jobs) / chunks }
	var workers sync.WaitGroup
	for chunk := 1; chunk < chunks; chunk++ {
		part := jobs[bound(chunk):bound(chunk+1)]
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer releaseNormalizeWorker()
			normalize(part)
		}()
	}
	normalize(jobs[:bound(1)])
	workers.Wait()
}
