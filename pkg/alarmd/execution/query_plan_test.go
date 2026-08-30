package execution_test

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryPlanFactsDigestIncludesOrderedQueryAndNormalization(t *testing.T) {
	facts := validQueryPlanFacts()
	built, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	reordered := facts
	reordered.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	reordered.QueryList[0].Functions = []execution.QueryFunction{facts.QueryList[0].Functions[1], facts.QueryList[0].Functions[0]}
	reorderedBuilt, err := execution.BuildQueryPlanFacts(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if built.QueryRevision == reorderedBuilt.QueryRevision {
		t.Fatal("ordered functions must participate in query revision")
	}

	changedNormalization := facts
	changedNormalization.Normalization.Version = "uq-threshold-normalization-v2"
	changedBuilt, err := execution.BuildQueryPlanFacts(changedNormalization)
	if err != nil {
		t.Fatal(err)
	}
	if built.QueryRevision == changedBuilt.QueryRevision {
		t.Fatal("dataset normalization must participate in query revision")
	}
}

func TestDatasetNormalizationConvertsUQMillisecondsToCoreSeconds(t *testing.T) {
	normalization := validQueryPlanFacts().Normalization
	got, err := normalization.NormalizeSourceTime(1_700_123_456_789)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1_700_123_456 {
		t.Fatalf("source_time=%d", got)
	}
}

func TestQueryPlanFactsDigestCoversTypedUQSemantics(t *testing.T) {
	base := validQueryPlanFacts()
	built, err := execution.BuildQueryPlanFacts(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*execution.QueryPlanFacts)
	}{
		{name: "time aggregation", mutate: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].TimeAggregation.Window = "120s" }},
		{name: "conditions", mutate: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].Conditions.Fields[0].Operator = "neq" }},
		{name: "offset", mutate: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].Offset = "1h" }},
		{name: "offset forward", mutate: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].OffsetForward = "5m" }},
		{name: "keep columns", mutate: func(facts *execution.QueryPlanFacts) {
			facts.QueryList[0].KeepColumns = append(facts.QueryList[0].KeepColumns, "bk_target_ip")
		}},
		{name: "driver", mutate: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].Driver = "prometheus" }},
		{name: "time field", mutate: func(facts *execution.QueryPlanFacts) { facts.QueryList[0].TimeField = "timestamp" }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			changed := validQueryPlanFacts()
			test.mutate(&changed)
			got, err := execution.BuildQueryPlanFacts(changed)
			if err != nil {
				t.Fatal(err)
			}
			if got.QueryRevision == built.QueryRevision {
				t.Fatal("typed UQ semantic fact must participate in query revision")
			}
		})
	}
}

func TestQueryPlanFactsAcceptsRealUQFunctionPositions(t *testing.T) {
	facts := validQueryPlanFacts()
	facts.QueryList[0].Functions = []execution.QueryFunction{
		{Method: "abs", Position: 0},
		{Method: "ceil", Position: 0},
	}
	if _, err := execution.BuildQueryPlanFacts(facts); err != nil {
		t.Fatalf("real UQ functions may omit position: %v", err)
	}
}

func TestQueryPlanFactsAcceptsPythonEmptyFunctionShapes(t *testing.T) {
	tests := []struct {
		name      string
		functions []execution.QueryFunction
	}{
		{name: "ordinary avg", functions: []execution.QueryFunction{{Method: "mean", Position: 0}}},
		{name: "real time", functions: []execution.QueryFunction{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := validQueryPlanFacts()
			facts.QueryList[0].Functions = test.functions
			facts.QueryList[0].TimeAggregation = execution.QueryFunction{}
			built, err := execution.BuildQueryPlanFacts(facts)
			if err != nil {
				t.Fatalf("real Python UQ shape must be accepted: %v", err)
			}
			if err := built.Validate(); err != nil {
				t.Fatalf("built facts must preserve a stable query revision: %v", err)
			}
		})
	}
}

func TestQueryPlanFactsRejectsNonEmptyInvalidFunctionShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*execution.QueryClause)
	}{
		{name: "function without method", mutate: func(clause *execution.QueryClause) {
			clause.Functions = []execution.QueryFunction{{Window: "60s"}}
			clause.TimeAggregation = execution.QueryFunction{}
		}},
		{name: "time aggregation without method", mutate: func(clause *execution.QueryClause) {
			clause.Functions = []execution.QueryFunction{}
			clause.TimeAggregation = execution.QueryFunction{Window: "60s"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := validQueryPlanFacts()
			test.mutate(&facts.QueryList[0])
			if _, err := execution.BuildQueryPlanFacts(facts); err == nil {
				t.Fatal("non-empty invalid function shape must be rejected")
			}
		})
	}
}

func TestQueryPlanFactsRequiresQueryExecutionFields(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*execution.QueryClause)
	}{
		{name: "driver", mutate: func(clause *execution.QueryClause) { clause.Driver = "" }},
		{name: "time field", mutate: func(clause *execution.QueryClause) { clause.TimeField = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := validQueryPlanFacts()
			test.mutate(&facts.QueryList[0])
			if _, err := execution.BuildQueryPlanFacts(facts); err == nil {
				t.Fatalf("empty %s must be rejected", test.name)
			}
		})
	}
}

func TestQueryPlanFactsAllowsOptionalUQClauseFieldsToBeEmpty(t *testing.T) {
	facts := validQueryPlanFacts()
	clause := &facts.QueryList[0]
	clause.DataSource = ""
	clause.TableID = ""
	clause.FieldName = ""
	clause.ReferenceName = ""
	clause.Functions = []execution.QueryFunction{}
	clause.TimeAggregation = execution.QueryFunction{}
	if _, err := execution.BuildQueryPlanFacts(facts); err != nil {
		t.Fatalf("only driver and time_field are mandatory UQ clause facts: %v", err)
	}
}

func validQueryPlanFacts() execution.QueryPlanFacts {
	return execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{
			DataSource: "bkmonitor", TableID: "2_bkmonitor_time_series_50010.base", FieldName: "usage", ReferenceName: "a",
			Driver: "influxdb", TimeField: "time",
			Functions:       []execution.QueryFunction{{Method: "sum", Position: 1}, {Method: "rate", Position: 2, Window: "1m"}},
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 1, Window: "60s"},
			Dimensions:      []string{"bk_target_ip", "bk_target_cloud_id"},
			Conditions: execution.QueryConditions{
				Fields: []execution.QueryConditionField{{Field: "bk_target_ip", Operator: "eq", Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "127.0.0.1"}}}},
			},
			KeepColumns: []string{"_time", "usage"},
		}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, Timezone: "Asia/Shanghai",
		Normalization: execution.DatasetNormalizationSpec{
			DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64), IdentityFields: []string{"bk_target_cloud_id", "bk_target_ip"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit:  execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1",
		},
	}
}
