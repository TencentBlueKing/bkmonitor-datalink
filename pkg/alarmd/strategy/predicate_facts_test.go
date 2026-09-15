package strategy

import "testing"

func TestPredicateFactsDetachedAndNormalizerMultiplier(t *testing.T) {
	predicate, normalizer := compileThresholdForTest(t, thresholdConfig("80"))
	before := predicate.Digest()
	facts := predicate.Facts()
	if facts.Kind != PredicateAny || len(facts.Children) != 1 || len(facts.Children[0].Children) != 1 {
		t.Fatalf("unexpected compiled tree %+v", facts)
	}
	leaf := facts.Children[0].Children[0]
	if leaf.Kind != PredicateCompare || leaf.NormalizedThreshold == "" || normalizer.SourceMultiplier() != 1 {
		t.Fatalf("facts %+v normalizer %+v", leaf, normalizer)
	}
	facts.Children[0].Children[0].NormalizedThreshold = "0"
	facts.Children[0].Children = nil
	if predicate.Digest() != before || predicate.Facts().Children[0].Children[0].NormalizedThreshold != leaf.NormalizedThreshold {
		t.Fatal("caller mutated frozen predicate")
	}
}
