package uq

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// identityAttempt declares the given identity fields (in the given, possibly
// unsorted, order) on the otherwise valid attempt.
func identityAttempt(t *testing.T, fields ...string) execution.QueryAttempt {
	t.Helper()
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	facts.QueryList[0].Dimensions = append([]string(nil), fields...)
	facts.Normalization.DatasetContract.IdentityFields = append([]string(nil), fields...)
	var err error
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{
		PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow,
		ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange,
		RequiredColumns: attempt.Spec.RequiredColumns,
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func identityTestSeries(groupKeys, groupValues []string) responseSeries {
	return responseSeries{Name: "_result0", Columns: []string{"_time", "_result"}, Types: []string{"int64", "float64"},
		GroupKeys: groupKeys, GroupValues: groupValues,
		Values: [][]json.RawMessage{{json.RawMessage(`1700123456789`), json.RawMessage(`12.5`)}}}
}

// An identity dimension that the provider series does not carry is bound to
// JSON null, exactly as Python binds an absent dimension to None
// (dimensions[field] = raw_data.get(field)); the series is kept and the null
// takes part in the identity digest.
func TestNormalizeSeriesBindsAbsentIdentityFieldToNullLikePython(t *testing.T) {
	attempt := validAttempt(t) // declares identity field bk_target_ip
	batch, nullFields, err := normalizeSeries(attempt.Spec, "provider-result", identityTestSeries(nil, nil), 1_700_123_500)
	if err != nil {
		t.Fatalf("normalizeSeries() error = %v, want absent identity dimension bound to null", err)
	}
	if nullFields != 1 || batch.Dataset.Len() != 1 || batch.Delivery.Series != 1 || batch.Delivery.Records != 1 {
		t.Fatalf("null fields=%d dataset=%d delivery=%+v", nullFields, batch.Dataset.Len(), batch.Delivery)
	}
	record, ok := batch.Dataset.Record(0)
	if !ok {
		t.Fatal("record is missing")
	}
	identity := record.DimensionIdentity()
	if len(identity.Fields) != 1 || identity.Fields[0].Name != "bk_target_ip" || string(identity.Fields[0].Value) != "null" {
		t.Fatalf("identity fields=%+v, want bk_target_ip bound to null", identity.Fields)
	}
	if dimension, found := record.Dimensions()["bk_target_ip"]; !found || string(dimension) != "null" {
		t.Fatalf("dimensions=%v, want bk_target_ip null like the Python None dimension value", record.Dimensions())
	}

	// Absent field and explicit null are the same identity: in Python None is
	// the dimension value in both cases and the dimensions md5 includes it.
	explicitNull, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2",
		[]contract.DimensionFieldV2{{Name: "bk_target_ip", Value: json.RawMessage("null")}})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Digest != explicitNull {
		t.Fatalf("absent-field digest %q differs from explicit-null digest %q", identity.Digest, explicitNull)
	}

	// The digest is deterministic across runs and pinned so a canonical
	// encoding change of null cannot silently re-key existing series.
	again, _, err := normalizeSeries(attempt.Spec, "provider-result", identityTestSeries(nil, nil), 1_700_123_500)
	if err != nil {
		t.Fatal(err)
	}
	againRecord, _ := again.Dataset.Record(0)
	if againRecord.DimensionIdentity().Digest != identity.Digest || againRecord.RecordID() != record.RecordID() ||
		again.Delivery.Digest != batch.Delivery.Digest {
		t.Fatalf("identity is not stable across runs: %q vs %q", againRecord.DimensionIdentity().Digest, identity.Digest)
	}
	const wantIdentityDigest = "50b081c3a5439523fa38cdfc7eb6eaed9af1edbc07847e029f96646e524a3c98"
	if identity.Digest != wantIdentityDigest {
		t.Fatalf("null identity digest=%q, want pinned %q", identity.Digest, wantIdentityDigest)
	}

	// A series that carries the field keeps its own, different identity.
	present, presentNulls, err := normalizeSeries(attempt.Spec, "provider-result", identityTestSeries([]string{"bk_target_ip"}, []string{"127.0.0.1"}), 1_700_123_500)
	if err != nil || presentNulls != 0 {
		t.Fatalf("present field: nulls=%d error=%v", presentNulls, err)
	}
	presentRecord, _ := present.Dataset.Record(0)
	wantPresent, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2",
		[]contract.DimensionFieldV2{{Name: "bk_target_ip", Value: json.RawMessage(`"127.0.0.1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if presentRecord.DimensionIdentity().Digest != wantPresent || presentRecord.DimensionIdentity().Digest == identity.Digest {
		t.Fatalf("present-field digest=%q want=%q (null digest %q)", presentRecord.DimensionIdentity().Digest, wantPresent, identity.Digest)
	}
}

func TestNormalizeSeriesSortsNullAndPresentIdentityFieldsDeterministically(t *testing.T) {
	attempt := identityAttempt(t, "target", "le") // declared unsorted; only target is carried
	batch, nullFields, err := normalizeSeries(attempt.Spec, "provider-result", identityTestSeries([]string{"target"}, []string{"node-a"}), 1_700_123_500)
	if err != nil || nullFields != 1 {
		t.Fatalf("nulls=%d error=%v", nullFields, err)
	}
	record, _ := batch.Dataset.Record(0)
	fields := record.DimensionIdentity().Fields
	if len(fields) != 2 || fields[0].Name != "le" || string(fields[0].Value) != "null" ||
		fields[1].Name != "target" || string(fields[1].Value) != `"node-a"` {
		t.Fatalf("identity fields=%+v, want sorted [le=null target=node-a]", fields)
	}
	want, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2", []contract.DimensionFieldV2{
		{Name: "le", Value: json.RawMessage("null")}, {Name: "target", Value: json.RawMessage(`"node-a"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.DimensionIdentity().Digest != want {
		t.Fatalf("digest=%q want=%q", record.DimensionIdentity().Digest, want)
	}
}

func TestClientCountsNullIdentityFieldsWithoutAbortingTheQuery(t *testing.T) {
	present := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,12.5]]}`
	absent := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[],"group_values":[],"values":[[1700123456789,13.5]]}`
	client := fixtureClient(t, http.StatusOK, `{"series":[`+present+`,`+absent+`],"is_partial":false}`, DefaultLimits())
	client.now = func() time.Time { return time.Unix(1_700_123_500, 0) }
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), validAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData ||
		len(sink.batches) != 2 || completion.Stats.Series != 2 || completion.Stats.NullIdentityFields != 1 {
		t.Fatalf("completion=%+v batches=%d", completion, len(sink.batches))
	}
	first, _ := sink.batches[0].Dataset.Record(0)
	second, _ := sink.batches[1].Dataset.Record(0)
	if first.DimensionIdentity().Digest == second.DimensionIdentity().Digest {
		t.Fatal("present and absent identity dimension collided")
	}
}
