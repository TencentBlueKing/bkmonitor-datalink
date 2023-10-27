// +build T

package storage_test

import (
	"testing"

	"github.com/cheekybits/genny/generic"
)

// T : gen type
type T generic.Type

// BenchmarkStoreSet_T :
func BenchmarkStoreSet_T(b *testing.B) {
	withClosingStore(benchmarkStoreSet, b, newT())
}

// BenchmarkStoreUpdate_T :
func BenchmarkStoreUpdate_T(b *testing.B) {
	withClosingStore(benchmarkStoreUpdate, b, newT())
}


// BenchmarkStoreGet_T :
func BenchmarkStoreGet_T(b *testing.B) {
	withClosingStore(benchmarkStoreGet, b, newT())
}


// BenchmarkStoreGetHotPot_T :
func BenchmarkStoreGetHotPot_T(b *testing.B) {
	withClosingStore(benchmarkStoreGetHotPot, b, newT())
}


// benchmarkStoreExistsMissing_T :
func BenchmarkStoreExistsMissing_T(b *testing.B) {
	withClosingStore(benchmarkStoreExistsMissing, b, newT())
}

// BenchmarkStoreExists_T :
func BenchmarkStoreExists_T(b *testing.B) {
	withClosingStore(benchmarkStoreExists, b, newT())
}

// BenchmarkStoreDelete_T :
func BenchmarkStoreDelete_T(b *testing.B) {
	withClosingStore(benchmarkStoreDelete, b, newT())
}

// BenchmarkStoreScan_T :
func BenchmarkStoreScan_T(b *testing.B) {
	withClosingStore(benchmarkStoreScan, b, newT())
}

// BenchmarkStoreCommit_T :
func BenchmarkStoreCommit_T(b *testing.B) {
	withClosingStore(benchmarkStoreCommit, b, newT())
}