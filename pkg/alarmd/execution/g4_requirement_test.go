package execution

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestG4InputProjectionSeparatesValueDimensionAndIdentity(t *testing.T) {
	projection := InputProjection{
		ValueFields:     []string{"value"},
		DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
		IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	want := []string{"bind_ip", "bk_target_cloud_id", "bk_target_ip", "display_name", "listen", "nonlisten", "not_accurate_listen", "protocol", "value"}
	if got := projection.RequiredColumns(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredColumns() = %v, want %v", got, want)
	}
	if reflect.DeepEqual(projection.DimensionFields, projection.IdentityFields) {
		t.Fatal("dynamic dimensions were silently promoted to stable identity")
	}
}

func TestG4DataRequirementTemplateDigestBindsWindowQueryAndProjection(t *testing.T) {
	base, err := BuildDataRequirementTemplate(DataRequirementTemplate{
		DatasetName:         "previous",
		Role:                InputRoleAlgorithmDependency,
		ConsumerLevelID:     1,
		LogicalQueryRef:     "query-primary",
		RelativeWindow:      RelativeQueryWindow{StartOffsetSeconds: -120, EndOffsetSeconds: -60, HalfOpen: true},
		StepMillis:          60_000,
		AlignmentMillis:     60_000,
		ResultWindowPolicy:  ResultWindowExactHalfOpen,
		ReadinessClass:      ReadinessFinalizedRequired,
		PointOffsetsSeconds: []int64{60},
		NamedPoints:         []NamedInputPoint{{Name: "previous", OffsetSeconds: 60}},
		InputProjection: InputProjection{
			ValueFields: []string{"value"}, IdentityFields: []string{"host"},
		},
	})
	if err != nil {
		t.Fatalf("BuildDataRequirementTemplate() error = %v", err)
	}
	if len(base.RequirementID) != 64 || !reflect.DeepEqual(base.RequiredColumns, []string{"host", "value"}) {
		t.Fatalf("template = %+v", base)
	}

	changedWindow := base
	changedWindow.RequirementID = ""
	changedWindow.RelativeWindow.StartOffsetSeconds--
	changedWindow, err = BuildDataRequirementTemplate(changedWindow)
	if err != nil {
		t.Fatal(err)
	}
	changedIdentity := base
	changedIdentity.RequirementID = ""
	changedIdentity.InputProjection.IdentityFields = []string{"host", "namespace"}
	changedIdentity, err = BuildDataRequirementTemplate(changedIdentity)
	if err != nil {
		t.Fatal(err)
	}
	changedOffsets := base
	changedOffsets.RequirementID = ""
	changedOffsets.PointOffsetsSeconds = []int64{60, 600}
	changedOffsets.NamedPoints = []NamedInputPoint{
		{Name: "previous", OffsetSeconds: 60},
		{Name: "previous_10m", OffsetSeconds: 600},
	}
	changedOffsets, err = BuildDataRequirementTemplate(changedOffsets)
	if err != nil {
		t.Fatal(err)
	}
	if base.RequirementID == changedWindow.RequirementID || base.RequirementID == changedIdentity.RequirementID ||
		base.RequirementID == changedOffsets.RequirementID {
		t.Fatal("requirement digest ignored window, point offsets, or identity projection")
	}
}

func TestG4QueryPlanDerivesIndependentRawHistoryRevision(t *testing.T) {
	facts, err := BuildQueryPlanFacts(QueryPlanFacts{
		Provider: ProviderUQ, ProviderRouteRef: "uq", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList:   []QueryClause{{Driver: "influxdb", TimeField: "time", ReferenceName: "a"}},
		MetricMerge: "a <= 3600", StepMillis: 60_000, AlignmentMillis: 60_000,
		DownSampleRange: DownSampleNone, Timezone: "UTC",
		Normalization: DatasetNormalizationSpec{
			DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_at"},
			SourceTimeUnit:  TimeUnitMillisecond, CanonicalSourceTimeUnit: TimeUnitSecond,
			SeriesIdentityMode: SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: GroupKeyStripTableSuffixV1,
			ValueSelectionMode: ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: ReceivedTimeProviderReceivedAt, Version: "test-v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := facts.WithMetricMerge("a")
	if err != nil {
		t.Fatalf("WithMetricMerge() error = %v", err)
	}
	if raw.MetricMerge != "a" || raw.QueryRevision == facts.QueryRevision {
		t.Fatalf("raw facts = %+v, primary revision = %q", raw, facts.QueryRevision)
	}
	if err := facts.Validate(); err != nil {
		t.Fatalf("source facts mutated: %v", err)
	}
}
