// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package memoryline

import "testing"

// An operator's read is never refused by the line: past it, Observe still
// grants, counts the bytes as admitted and the ask as over the line, and
// counts no refusal.
func TestAnObservedAskPastTheLineGoesAheadAndIsReported(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: 1000, live: 990, cycles: 1}}).read)
	if !line.Observe(ConsumerFleetView, 500) {
		t.Fatal("an observed ask past the line was refused")
	}
	reading := line.Read()
	if reading.RefusedTotal[ConsumerFleetView] != 0 || reading.OverTotal[ConsumerFleetView] != 1 || reading.AdmittedBytes[ConsumerFleetView] != 500 {
		t.Fatalf("refused %d, over %d, admitted %d; want 0, 1, 500", reading.RefusedTotal[ConsumerFleetView],
			reading.OverTotal[ConsumerFleetView], reading.AdmittedBytes[ConsumerFleetView])
	}
	if !line.Observe(ConsumerFleetView, 1) || line.Read().OverTotal[ConsumerFleetView] != 2 {
		t.Fatal("a second ask over the line was not reported over")
	}
	room := newLine((&fakeHeap{heap{limit: 1000, live: 100, cycles: 1}}).read)
	if !room.Observe(ConsumerDiagnosisProgress, 10) || room.Read().OverTotal[ConsumerDiagnosisProgress] != 0 {
		t.Fatal("an ask with room was reported over the line")
	}
}

// An observed hold past the line is held, counted against the line until
// released, and reported over; its release gives it back.
func TestAnObservedHoldPastTheLineIsHeldAndReported(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: 1000, live: 990, cycles: 1}}).read)
	release := line.HoldObserved(ConsumerFleetView, 300)
	reading := line.Read()
	if reading.HeldBytes != 300 || reading.OverTotal[ConsumerFleetView] != 1 || reading.RefusedTotal[ConsumerFleetView] != 0 {
		t.Fatalf("held %d, over %d, refused %d; want 300, 1, 0", reading.HeldBytes, reading.OverTotal[ConsumerFleetView], reading.RefusedTotal[ConsumerFleetView])
	}
	release()
	release()
	if held := line.Read().HeldBytes; held != 0 {
		t.Fatalf("held %d after release, want 0", held)
	}
}
