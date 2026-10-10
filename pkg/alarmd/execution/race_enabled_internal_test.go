//go:build race

package execution

// raceEnabled is whether this test binary runs under the race detector, whose
// sync.Pool drops a quarter of what is put back: a test that measures
// allocation through a pooled buffer reads the pool, not the code.
const raceEnabled = true
