package execution

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"testing"
)

// The content key is what the payload says: the same payload keyed twice
// gives the same key, and a change to any one of its fields gives another.
// The cases are one per field of the payload type, and the count is checked
// against the type, so a field added to the payload and not to this test
// fails here rather than going unkeyed unnoticed.
func TestAStateMutationsContentKeyIsItsContent(t *testing.T) {
	payload := func() stateMutationDigestPayload {
		return stateMutationDigestPayloadOf(normalizeStateMutation(sealTestMutation(3)))
	}
	first, keyed := stateMutationContentKey(payload())
	again, keyedAgain := stateMutationContentKey(payload())
	if !keyed || !keyedAgain || first != again {
		t.Fatalf("one payload keyed twice: %x (%v) and %x (%v)", first, keyed, again, keyedAgain)
	}
	changes := map[string]func(*stateMutationDigestPayload){
		"Identity":        func(p *stateMutationDigestPayload) { p.Identity.SeriesIdentityDigest += "0" },
		"ApplyVersion":    func(p *stateMutationDigestPayload) { p.ApplyVersion.StateApplyEpoch++ },
		"AffectedRecords": func(p *stateMutationDigestPayload) { p.AffectedRecords[0].SourceTime++ },
		"SeriesGuard":     func(p *stateMutationDigestPayload) { p.SeriesGuard = &StateGuardFact{} },
		"Levels":          func(p *stateMutationDigestPayload) { p.Levels[0].LastProcessedEventTime++ },
		"Points":          func(p *stateMutationDigestPayload) { p.Points[0].SourceTime++ },
		"RetentionPoints": func(p *stateMutationDigestPayload) { p.RetentionPoints++ },
	}
	if fields := reflect.TypeOf(stateMutationDigestPayload{}).NumField(); fields != len(changes) {
		t.Fatalf("the payload has %d fields and the test changes %d: key the new one here", fields, len(changes))
	}
	for field, change := range changes {
		changed := payload()
		change(&changed)
		key, keyed := stateMutationContentKey(changed)
		if !keyed || key == first {
			t.Fatalf("a changed %s kept the key %x (keyed %v)", field, key, keyed)
		}
	}
}

// The key is the payload encoded as Marshal encodes it, HTML escaped, with
// the newline the encoder ends a document with: the one function every seal
// is written and checked through, not a key any other code derives.
func TestTheContentKeyIsMarshalsEncodingWithTheEncodersNewline(t *testing.T) {
	payload := stateMutationDigestPayloadOf(normalizeStateMutation(sealTestMutation(2)))
	payload.Identity.Plan.StrategyID = "<script>&amp;"
	marshalled, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	key, keyed := stateMutationContentKey(payload)
	if want := sha256.Sum256(append(marshalled, '\n')); !keyed || key != want {
		t.Fatalf("content key %x, want the digest of Marshal's bytes and a newline %x", key, want)
	}
}

// marshalledContentKey is the key as it was derived before: the payload
// marshalled into a document, and the document hashed.
func marshalledContentKey(payload stateMutationDigestPayload) ([sha256.Size]byte, bool) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(encoded), true
}

// Keying keeps nothing of the document: it allocates at least the
// document's length less than marshalling it did.
func TestTheContentKeyDoesNotKeepTheDocument(t *testing.T) {
	if raceEnabled {
		// Both arms encode into a buffer from the encoder's pool. The race
		// detector's pool drops a quarter of the buffers put back, so calls
		// grow a new one at random and the difference is lost in it: one run
		// in five read less than the document's length.
		t.Skip("allocation is not measurable through a pooled buffer under the race detector")
	}
	payload := stateMutationDigestPayloadOf(normalizeStateMutation(sealTestMutation(30)))
	document, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	perCall := func(call func()) uint64 {
		const calls = 500
		call()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range calls {
			call()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / calls
	}
	keyed := perCall(func() { _, _ = stateMutationContentKey(payload) })
	marshalled := perCall(func() { _, _ = marshalledContentKey(payload) })
	// The encoder and the hash state are the allowance: a few hundred bytes.
	if keyed+512 > marshalled || marshalled-keyed+512 < uint64(len(document)) {
		t.Fatalf("keying allocates %d bytes per call, marshalling %d, for a %d-byte document", keyed, marshalled, len(document))
	}
}

func BenchmarkStateMutationContentKey(b *testing.B) {
	for _, points := range []int{1, 30} {
		payload := stateMutationDigestPayloadOf(normalizeStateMutation(sealTestMutation(points)))
		for _, arm := range []struct {
			name string
			key  func(stateMutationDigestPayload) ([sha256.Size]byte, bool)
		}{{"marshalled", marshalledContentKey}, {"encoded", stateMutationContentKey}} {
			b.Run(fmt.Sprintf("%s/points=%d", arm.name, points), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					arm.key(payload)
				}
			})
		}
	}
}
