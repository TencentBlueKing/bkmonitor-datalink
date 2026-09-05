package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestLegacyPrimaryQueryCompilerReproducesPythonThresholdUQFacts(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "Asia/Shanghai", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	query := json.RawMessage(`{
		"data_source_label":"bk_monitor","data_type_label":"time_series",
		"metric_field":"CPUUsage","alias":"A","agg_method":"AVG","agg_interval":60,
		"agg_dimension":["host", ""],
		"agg_condition":[
			{"key":"host","method":"eq","value":"node-1"},
			{"condition":"or","key":"module","method":"include","value":["api",2]}
		],
		"result_table_id":"System.CPU","functions":[]
	}`)
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "12", ItemID: "1", QueryMD5: "query-1", Expression: "A",
		Functions:    []json.RawMessage{json.RawMessage(`{"id":"abs","params":[]}`)},
		QueryConfigs: []json.RawMessage{query},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Validate(); err != nil {
		t.Fatalf("compiled query facts are invalid: %v", err)
	}
	if facts.Provider != execution.ProviderUQ || facts.ProviderRouteRef != "uq-primary-v1" || facts.TenantID != "tenant-a" || facts.BusinessID != "2" || facts.SpaceScope != "bkcc__2" {
		t.Fatalf("identity/provider facts=%+v", facts)
	}
	if facts.StepMillis != 60_000 || facts.AlignmentMillis != 60_000 || facts.Timezone != "Asia/Shanghai" || facts.NotTimeAlign || facts.DownSampleRange != execution.DownSampleNone {
		t.Fatalf("schedule/query facts=%+v", facts)
	}
	if facts.MetricMerge != "abs(a)" {
		t.Fatalf("metric_merge=%q", facts.MetricMerge)
	}
	if len(facts.QueryList) != 1 {
		t.Fatalf("query_list=%+v", facts.QueryList)
	}
	clause := facts.QueryList[0]
	if clause.DataSource != "" || clause.Driver != "influxdb" || clause.TableID != "system.cpu" || clause.FieldName != "CPUUsage" || clause.TimeField != "time" || clause.ReferenceName != "a" {
		t.Fatalf("query clause=%+v", clause)
	}
	if len(clause.Functions) != 1 || clause.Functions[0].Method != "mean" || len(clause.Functions[0].Dimensions) != 1 || clause.Functions[0].Dimensions[0] != "host" {
		t.Fatalf("query functions=%+v", clause.Functions)
	}
	if clause.TimeAggregation.Method != "avg_over_time" || clause.TimeAggregation.Window != "60s" {
		t.Fatalf("time aggregation=%+v", clause.TimeAggregation)
	}
	if len(clause.Conditions.Fields) != 2 || len(clause.Conditions.Connectors) != 1 || clause.Conditions.Connectors[0] != "or" {
		t.Fatalf("conditions=%+v", clause.Conditions)
	}
	if clause.Conditions.Fields[0].Operator != "contains" || clause.Conditions.Fields[0].Values[0].StringValue != "node-1" || clause.Conditions.Fields[1].Operator != "req" || clause.Conditions.Fields[1].Values[1].StringValue != "2" {
		t.Fatalf("condition mapping=%+v", clause.Conditions)
	}
	if got := clause.KeepColumns; len(got) != 3 || got[0] != "_time" || got[1] != "a" || got[2] != "host" {
		t.Fatalf("keep_columns=%v", got)
	}
	normalization := facts.Normalization
	if len(normalization.DatasetContract.IdentityFields) != 1 || normalization.DatasetContract.IdentityFields[0] != "host" || normalization.DatasetContract.SourceTimeField != "_time" || normalization.DatasetContract.ReceivedTimeField != "_received_time" {
		t.Fatalf("dataset contract=%+v", normalization.DatasetContract)
	}
	if normalization.SourceTimeUnit != execution.TimeUnitMillisecond || normalization.CanonicalSourceTimeUnit != execution.TimeUnitSecond || normalization.SeriesIdentityMode != execution.SeriesIdentityUQGroupKeysValuesV1 || normalization.GroupKeyRule != execution.GroupKeyStripTableSuffixV1 || normalization.ValueSelectionMode != execution.ValueSelectionResultOrFirstReferenceV1 || normalization.CanonicalValueField != "value" || normalization.ReceivedTimeMode != execution.ReceivedTimeProviderReceivedAt || normalization.Version != "uq-threshold-normalization-v1" {
		t.Fatalf("normalization=%+v", normalization)
	}
	if normalization.DatasetContract.SchemaDigest == "" || normalization.DatasetContract.NormalizationDigest == "" {
		t.Fatal("content-derived dataset digests are required")
	}
}

func TestLegacyPrimaryQueryCompilerAcceptsPythonUnifyQueryWithoutResultTable(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity: controlplane.SourceIdentity{
			TenantID:   "default",
			BusinessID: "2",
			SpaceScope: "bkcc__2",
		},
		StrategyID: "8565",
		ItemID:     "8683",
		QueryMD5:   "python-query-md5",
		Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_id":"bk_monitor..bmw_metadata_rt_metric_num",
			"metric_field":"bmw_metadata_rt_metric_num","alias":"a",
			"agg_method":"AVG","agg_interval":600,
			"agg_dimension":["table_id"],"agg_condition":[],
			"result_table_id":"","functions":[]
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Validate(); err != nil {
		t.Fatalf("compiled empty-table query facts are invalid: %v", err)
	}
	if facts.StepMillis != 600_000 || facts.AlignmentMillis != 600_000 || facts.MetricMerge != "a" {
		t.Fatalf("schedule/merge facts=%+v", facts)
	}
	if len(facts.QueryList) != 1 {
		t.Fatalf("query_list=%+v", facts.QueryList)
	}
	clause := facts.QueryList[0]
	if clause.TableID != "" || clause.FieldName != "bmw_metadata_rt_metric_num" || clause.ReferenceName != "a" || clause.Driver != "influxdb" || clause.TimeField != "time" {
		t.Fatalf("query clause=%+v", clause)
	}
	if len(clause.Dimensions) != 1 || clause.Dimensions[0] != "table_id" {
		t.Fatalf("dimensions=%v", clause.Dimensions)
	}
	if len(clause.Functions) != 1 || clause.Functions[0].Method != "mean" || clause.TimeAggregation.Method != "avg_over_time" || clause.TimeAggregation.Window != "600s" {
		t.Fatalf("aggregation facts functions=%+v time_aggregation=%+v", clause.Functions, clause.TimeAggregation)
	}
	if len(clause.KeepColumns) != 3 || clause.KeepColumns[0] != "_time" || clause.KeepColumns[1] != "a" || clause.KeepColumns[2] != "table_id" {
		t.Fatalf("keep_columns=%v", clause.KeepColumns)
	}
}

func TestLegacyPrimaryQueryCompilerPreservesOrderedMultiQueryAndExpressionFunctions(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	source := controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "-3", SpaceScope: "bkci__project"},
		StrategyID: "13", ItemID: "2", QueryMD5: "query-2", Expression: "A OR B",
		Functions: []json.RawMessage{
			json.RawMessage(`{"id":"topk","params":[{"id":"k","value":5}]}`),
			json.RawMessage(`{"id":"abs","params":[]}`),
		},
		QueryConfigs: []json.RawMessage{
			json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"one","alias":"A","agg_method":"REAL_TIME","agg_interval":120,"agg_dimension":["pod","namespace"],"agg_condition":[],"result_table_id":"Table.One","functions":[]}`),
			json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"two","alias":"B","agg_method":"SUM","agg_interval":60,"agg_dimension":["namespace","pod"],"agg_condition":[],"result_table_id":"Table.Two","functions":[]}`),
		},
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if facts.MetricMerge != "abs(topk(5,a or b))" || facts.StepMillis != 60_000 {
		t.Fatalf("metric_merge=%q step=%d", facts.MetricMerge, facts.StepMillis)
	}
	if len(facts.QueryList) != 2 || facts.QueryList[0].ReferenceName != "a" || facts.QueryList[1].ReferenceName != "b" {
		t.Fatalf("query order changed: %+v", facts.QueryList)
	}
	if len(facts.QueryList[0].Functions) != 0 || facts.QueryList[0].TimeAggregation.Method != "" {
		t.Fatalf("REAL_TIME must retain Python empty function shapes: %+v", facts.QueryList[0])
	}
	if facts.QueryList[1].Functions[0].Method != "sum" || facts.QueryList[1].TimeAggregation.Method != "sum_over_time" || facts.QueryList[1].TimeAggregation.Window != "60s" {
		t.Fatalf("SUM mapping=%+v", facts.QueryList[1])
	}
	gotIdentity := facts.Normalization.DatasetContract.IdentityFields
	if len(gotIdentity) != 2 || gotIdentity[0] != "namespace" || gotIdentity[1] != "pod" {
		t.Fatalf("identity fields=%v", gotIdentity)
	}
}

func TestLegacyPrimaryQueryCompilerReproducesPythonTimeShiftDirection(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	compile := func(shift string) execution.QueryClause {
		t.Helper()
		facts, compileErr := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
			Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
			StrategyID: "15", ItemID: "4", QueryMD5: "query-4", Expression: "a",
			QueryConfigs: []json.RawMessage{json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,"agg_dimension":["host"],"agg_condition":[],"result_table_id":"system.cpu","functions":[{"id":"time_shift","params":[{"id":"n","value":"` + shift + `"}]}]}`)},
		})
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		return facts.QueryList[0]
	}
	backward := compile("1h")
	if backward.Offset != "3600s" || backward.OffsetForward != "false" {
		t.Fatalf("backward offset=%q forward=%q", backward.Offset, backward.OffsetForward)
	}
	forward := compile("-1h")
	if forward.Offset != "3600s" || forward.OffsetForward != "true" {
		t.Fatalf("forward offset=%q forward=%q", forward.Offset, forward.OffsetForward)
	}
}

func TestLegacyPrimaryQueryCompilerPreservesNoDimensionAndEmptyStringCondition(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "16", ItemID: "5", QueryMD5: "query-no-dimension", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,
			"agg_dimension":[],
			"agg_condition":[{"key":"namespace","method":"neq","value":[""]}],
			"result_table_id":"system.cpu","functions":[]
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Validate(); err != nil {
		t.Fatalf("compiled no-dimension facts are invalid: %v", err)
	}
	identity := facts.Normalization.DatasetContract.IdentityFields
	if identity == nil || len(identity) != 0 {
		t.Fatalf("no-dimension identity=%#v, want explicit empty", identity)
	}
	values := facts.QueryList[0].Conditions.Fields[0].Values
	if len(values) != 1 || values[0].Kind != execution.QueryScalarString || values[0].StringValue != "" {
		t.Fatalf("empty string condition=%+v", values)
	}
}

func TestLegacyPrimaryQueryCompilerReturnsTypedUnsupportedFact(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	_, err = planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "14", ItemID: "3", QueryMD5: "query-3", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{"data_source_label":"prometheus","data_type_label":"time_series","promql":"up","agg_interval":60,"agg_dimension":["instance"]}`)},
	})
	if err == nil {
		t.Fatal("unsupported provider shape must be explicit")
	}
	var compileErr *controlplane.QueryPlanCompileError
	if !errors.As(err, &compileErr) || compileErr.Disposition != controlplane.DispositionUnsupported || compileErr.Reason != "QUERY_SOURCE_NOT_MIGRATED" {
		t.Fatalf("typed compile error=%T %v", err, err)
	}
}

func TestBuildCatalogPreservesTypedQueryCompilerDisposition(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	document := json.RawMessage(`{"id":14,"bk_biz_id":2,"update_time":1,"items":[{"id":3,"query_md5":"query-3","expression":"a","functions":[],"query_configs":[{"data_source_label":"prometheus","data_type_label":"time_series","promql":"up","agg_interval":60,"agg_dimension":["instance"]}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":0}]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}}]}`)
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Planner: planner, Strategies: []controlplane.SourceStrategy{{
		SourceID: "14", Document: document,
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 || catalog.Dispositions[0].Disposition != controlplane.DispositionUnsupported || catalog.Dispositions[0].Reason != "QUERY_SOURCE_NOT_MIGRATED" {
		t.Fatalf("catalog dispositions=%+v", catalog.Dispositions)
	}
}

func TestLegacyPrimaryQueryCompilerReproducesPythonRuntimeFiltersAndIgnoresCachedFilterDict(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "5", SpaceScope: "bkcc__5"},
		StrategyID: "1", ItemID: "1", QueryMD5: "query-system-disk", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"in_use","alias":"a","agg_method":"AVG","agg_interval":300,
			"agg_dimension":["bk_target_ip","bk_target_cloud_id","mount_point"],
			"agg_condition":[{"key":"mount_point","method":"neq","value":["/tmp"]}],
			"filter_dict":{"must_be_ignored":"legacy-cache-field"},
			"result_table_id":"system.disk","functions":[]
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conditions := facts.QueryList[0].Conditions
	if len(conditions.Fields) != 2 || len(conditions.Connectors) != 1 || conditions.Connectors[0] != "and" {
		t.Fatalf("conditions=%+v", conditions)
	}
	runtimeFilter := conditions.Fields[0]
	if runtimeFilter.Field != "device_type" || runtimeFilter.Operator != "ncontains" || len(runtimeFilter.Values) != 3 || runtimeFilter.Values[0].StringValue != "iso9660" || runtimeFilter.Values[1].StringValue != "tmpfs" || runtimeFilter.Values[2].StringValue != "udf" {
		t.Fatalf("runtime filter=%+v", runtimeFilter)
	}
	if conditions.Fields[1].Field != "mount_point" {
		t.Fatalf("cached filter_dict leaked or query condition order changed: %+v", conditions)
	}
}

func TestLegacyPrimaryQueryCompilerIsolatesMissingRuntimeFilterFacts(t *testing.T) {
	runtimeFacts := testLegacyQueryRuntimeFacts()
	runtimeFacts.SystemDiskFilter.Values = nil
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", runtimeFacts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "5", SpaceScope: "bkcc__5"},
		StrategyID: "1", ItemID: "1", QueryMD5: "query-system-disk", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"in_use","alias":"a","agg_method":"AVG","agg_interval":300,
			"agg_dimension":["mount_point"],"agg_condition":[],"result_table_id":"system.disk"
		}`)},
	})
	if err == nil {
		t.Fatal("missing runtime filter fact must isolate the affected plan")
	}
	var compileErr *controlplane.QueryPlanCompileError
	if !errors.As(err, &compileErr) || compileErr.Disposition != controlplane.DispositionSourceIncomplete || compileErr.Reason != "QUERY_RUNTIME_FILTER_FACT_MISSING" {
		t.Fatalf("typed compile error=%T %v", err, err)
	}
}

func TestLegacyPrimaryQueryCompilerAcceptsExplicitEmptyRuntimeFilter(t *testing.T) {
	runtimeFacts := testLegacyQueryRuntimeFacts()
	runtimeFacts.SystemDiskFilter.Values = []string{}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", runtimeFacts)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "5", SpaceScope: "bkcc__5"},
		StrategyID: "1", ItemID: "1", QueryMD5: "query-system-disk", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"in_use","alias":"a","agg_method":"AVG","agg_interval":300,
			"agg_dimension":["mount_point"],
			"agg_condition":[{"key":"mount_point","method":"neq","value":["/tmp"]}],
			"result_table_id":"system.disk"
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conditions := facts.QueryList[0].Conditions
	if len(conditions.Fields) != 1 || conditions.Fields[0].Field != "mount_point" {
		t.Fatalf("explicit empty runtime filter should not inject a condition: %+v", conditions)
	}
}

func TestLegacyPrimaryQueryCompilerUsesExplicitCMDBLevelRuntimeFacts(t *testing.T) {
	query := json.RawMessage(`{
		"data_source_label":"bk_monitor","data_type_label":"time_series",
		"metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,
		"agg_dimension":["bk_obj_id","bk_inst_id"],"agg_condition":[],
		"result_table_id":"system.cpu_cmdb_level","time_field":"time"
	}`)
	compile := func(t *testing.T, facts controlplane.LegacyQueryRuntimeFacts) (execution.QueryPlanFacts, error) {
		t.Helper()
		planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", facts)
		if err != nil {
			t.Fatal(err)
		}
		return planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
			Identity:     controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
			StrategyID:   "1",
			ItemID:       "1",
			QueryMD5:     "query-cmdb-level",
			Expression:   "a",
			QueryConfigs: []json.RawMessage{query},
		})
	}

	missing := testLegacyQueryRuntimeFacts()
	missing.AccessBKData = nil
	if _, err := compile(t, missing); err == nil {
		t.Fatal("missing IS_ACCESS_BK_DATA fact must isolate the affected Plan")
	} else {
		var compileErr *controlplane.QueryPlanCompileError
		if !errors.As(err, &compileErr) || compileErr.Disposition != controlplane.DispositionSourceIncomplete || compileErr.Reason != "QUERY_ACCESS_BK_DATA_FACT_MISSING" {
			t.Fatalf("typed missing Access fact=%T %v", err, err)
		}
	}

	bypass := testLegacyQueryRuntimeFacts()
	bypass.BKDataCMDBLevelTables = []string{"system.cpu_cmdb_level"}
	if _, err := compile(t, bypass); err == nil {
		t.Fatal("Python datasource bypass must not be represented as UQ")
	} else {
		var compileErr *controlplane.QueryPlanCompileError
		if !errors.As(err, &compileErr) || compileErr.Disposition != controlplane.DispositionUnsupported || compileErr.Reason != "QUERY_CMDB_LEVEL_BYPASSES_UQ" {
			t.Fatalf("typed bypass fact=%T %v", err, err)
		}
	}

	compiled, err := compile(t, testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.QueryList) != 1 || compiled.QueryList[0].TableID != "system.cpu" || compiled.QueryList[0].TimeField != "dtEventTimeStamp" {
		t.Fatalf("CMDB level UQ projection=%+v", compiled.QueryList)
	}

	disabled := testLegacyQueryRuntimeFacts()
	accessBKData := false
	disabled.AccessBKData = &accessBKData
	unmodified, err := compile(t, disabled)
	if err != nil {
		t.Fatal(err)
	}
	if unmodified.QueryList[0].TableID != "system.cpu_cmdb_level" || unmodified.QueryList[0].TimeField != "time" {
		t.Fatalf("disabled BKData access changed query=%+v", unmodified.QueryList[0])
	}
}

func TestLegacyPrimaryQueryCompilerFreezesRuntimeFactsAtConstruction(t *testing.T) {
	accessBKData := false
	runtimeFacts := testLegacyQueryRuntimeFacts()
	runtimeFacts.AccessBKData = &accessBKData
	runtimeFacts.SystemDiskFilter.Values = []string{"iso9660"}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", runtimeFacts)
	if err != nil {
		t.Fatal(err)
	}
	accessBKData = true
	runtimeFacts.SystemDiskFilter.Values[0] = "mutated"
	cmdbFacts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "1", ItemID: "1", QueryMD5: "query-cmdb", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,
			"agg_dimension":["bk_obj_id"],"agg_condition":[],
			"result_table_id":"system.cpu_cmdb_level","time_field":"time"
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if clause := cmdbFacts.QueryList[0]; clause.TableID != "system.cpu_cmdb_level" || clause.TimeField != "time" {
		t.Fatalf("constructor did not freeze AccessBKData: %+v", clause)
	}
	diskFacts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "1", ItemID: "1", QueryMD5: "query-disk", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,
			"agg_dimension":["mount_point"],"agg_condition":[],"result_table_id":"system.disk"
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := diskFacts.QueryList[0].Conditions.Fields[0].Values[0].StringValue; got != "iso9660" {
		t.Fatalf("constructor did not freeze runtime filter: %q", got)
	}
}

func TestLegacyPrimaryQueryCompilerRepeatsRuntimeFilterAcrossPythonORGroups(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "5", SpaceScope: "bkcc__5"},
		StrategyID: "1", ItemID: "1", QueryMD5: "query-system-disk-or", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series",
			"metric_field":"in_use","alias":"a","agg_method":"AVG","agg_interval":300,
			"agg_dimension":["mount_point"],
			"agg_condition":[
				{"key":"mount_point","method":"eq","value":["/data"]},
				{"condition":"or","key":"mount_point","method":"eq","value":["/opt"]}
			],
			"result_table_id":"system.disk"
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	conditions := facts.QueryList[0].Conditions
	if len(conditions.Fields) != 4 || len(conditions.Connectors) != 3 {
		t.Fatalf("conditions=%+v", conditions)
	}
	if conditions.Fields[0].Field != "device_type" || conditions.Fields[1].Field != "mount_point" || conditions.Fields[2].Field != "device_type" || conditions.Fields[3].Field != "mount_point" {
		t.Fatalf("runtime filter was not repeated per OR group: %+v", conditions)
	}
	if conditions.Connectors[0] != "and" || conditions.Connectors[1] != "or" || conditions.Connectors[2] != "and" {
		t.Fatalf("OR group connectors=%v", conditions.Connectors)
	}
}

func testLegacyQueryRuntimeFacts() controlplane.LegacyQueryRuntimeFacts {
	accessBKData := true
	return controlplane.LegacyQueryRuntimeFacts{
		AccessBKData:          &accessBKData,
		BKDataCMDBLevelTables: []string{},
		SystemDiskFilter:      controlplane.LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660", "tmpfs", "udf"}},
		SystemNetworkFilter:   controlplane.LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}},
	}
}
