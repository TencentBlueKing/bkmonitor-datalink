package execution

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type QueryScalarKind string

const (
	QueryScalarString  QueryScalarKind = "STRING"
	QueryScalarNumber  QueryScalarKind = "NUMBER"
	QueryScalarBoolean QueryScalarKind = "BOOLEAN"
)

type QueryScalar struct {
	Kind        QueryScalarKind
	StringValue string
	NumberValue string
	BoolValue   bool
}

type QueryFunction struct {
	Method     string
	Field      string
	Without    bool
	Dimensions []string
	Position   int32
	Arguments  []QueryScalar
	Window     string
	Subquery   bool
	Step       string
}

type QueryConditionField struct {
	Field    string
	Operator string
	Values   []QueryScalar
	Wildcard string
	Prefix   string
	Suffix   string
}

type QueryConditions struct {
	Fields     []QueryConditionField
	Connectors []string
}

type QueryClause struct {
	SourceConditions *QueryConditions `json:"SourceConditions,omitempty"`
	FieldSemantics   string           `json:"FieldSemantics,omitempty"`
	DataSource       string
	Driver           string
	TableID          string
	FieldName        string
	TimeField        string
	IsRegexp         bool
	ReferenceName    string
	Functions        []QueryFunction
	TimeAggregation  QueryFunction
	Dimensions       []string
	Conditions       QueryConditions
	Offset           string
	OffsetForward    string
	KeepColumns      []string
	QueryString      string
}

// PromQLQuery preserves the independent UQ PromQL endpoint contract.
type PromQLQuery struct {
	Expression string
	Match      string
}

type QueryTimeField struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
	Unit string `json:"unit" yaml:"unit"`
}

// QueryStorage carries registered routing facts, never storage credentials.
type QueryStorage struct {
	TableID     string         `json:"table_id" yaml:"table_id"`
	StorageID   string         `json:"storage_id" yaml:"storage_id"`
	StorageType string         `json:"storage_type" yaml:"storage_type"`
	DB          string         `json:"db" yaml:"db"`
	Measurement string         `json:"measurement" yaml:"measurement"`
	NeedAddTime bool           `json:"need_add_time" yaml:"need_add_time"`
	TimeField   QueryTimeField `json:"time_field" yaml:"time_field"`
	SourceType  string         `json:"source_type" yaml:"source_type"`
}

type TimeUnit string
type SeriesIdentityMode string
type GroupKeyRule string
type ValueSelectionMode string
type ReceivedTimeMode string

const (
	TimeUnitMillisecond TimeUnit = "MILLISECOND"
	TimeUnitSecond      TimeUnit = "SECOND"

	SeriesIdentityUQGroupKeysValuesV1      SeriesIdentityMode = "UQ_GROUP_KEYS_VALUES_V1"
	GroupKeyStripTableSuffixV1             GroupKeyRule       = "STRIP_TABLE_SUFFIX_V1"
	ValueSelectionResultOrFirstReferenceV1 ValueSelectionMode = "RESULT_OR_FIRST_REFERENCE_V1"
	ReceivedTimeProviderReceivedAt         ReceivedTimeMode   = "PROVIDER_RECEIVED_AT"
)

type DatasetNormalizationSpec struct {
	DimensionAliases        map[string]string `json:"DimensionAliases,omitempty"`
	DatasetContract         contract.DatasetContractV2
	SourceTimeUnit          TimeUnit
	CanonicalSourceTimeUnit TimeUnit
	SeriesIdentityMode      SeriesIdentityMode
	GroupKeyRule            GroupKeyRule
	ValueSelectionMode      ValueSelectionMode
	CanonicalValueField     string
	ReceivedTimeMode        ReceivedTimeMode
	Version                 string
}

func (spec DatasetNormalizationSpec) Validate() error {
	if spec.DatasetContract.DynamicDimensions && len(spec.DatasetContract.IdentityFields) != 0 {
		return errors.New("alarmd execution: dynamic identity cannot declare fixed fields")
	}
	if spec.DatasetContract.SchemaDigest == "" || spec.DatasetContract.NormalizationDigest == "" ||
		spec.DatasetContract.IdentityFields == nil || spec.DatasetContract.SourceTimeField == "" ||
		spec.DatasetContract.ReceivedTimeField == "" || spec.SourceTimeUnit != TimeUnitMillisecond ||
		spec.CanonicalSourceTimeUnit != TimeUnitSecond || spec.SeriesIdentityMode != SeriesIdentityUQGroupKeysValuesV1 ||
		spec.GroupKeyRule != GroupKeyStripTableSuffixV1 || spec.ValueSelectionMode != ValueSelectionResultOrFirstReferenceV1 ||
		spec.CanonicalValueField == "" || spec.ReceivedTimeMode != ReceivedTimeProviderReceivedAt || spec.Version == "" {
		return errors.New("alarmd execution: incomplete dataset normalization contract")
	}
	return nil
}

func (spec DatasetNormalizationSpec) NormalizeSourceTime(sourceMillis int64) (int64, error) {
	if err := spec.Validate(); err != nil {
		return 0, err
	}
	if sourceMillis <= 0 {
		return 0, errors.New("alarmd execution: positive UQ source time is required")
	}
	return sourceMillis / 1000, nil
}

type QueryPlanFacts struct {
	QueryDelaySeconds int64    `json:"QueryDelaySeconds,omitempty"`
	SourceSemantics   []string `json:"SourceSemantics,omitempty"`
	QueryRevision     QueryRevision
	Provider          ProviderKind
	ProviderRouteRef  ProviderRouteRef
	TenantID          string
	BusinessID        string
	SpaceScope        string
	QueryList         []QueryClause
	PromQL            *PromQLQuery              `json:"PromQL,omitempty"`
	TSDBMap           map[string][]QueryStorage `json:"TSDBMap,omitempty"`
	MetricMerge       string
	StepMillis        int64
	AlignmentMillis   int64
	DownSampleRange   DownSampleRange
	Timezone          string
	NotTimeAlign      bool
	Normalization     DatasetNormalizationSpec
}

func BuildQueryPlanFacts(facts QueryPlanFacts) (QueryPlanFacts, error) {
	if facts.QueryRevision != "" {
		return QueryPlanFacts{}, errors.New("alarmd execution: QueryPlanFacts builder owns query revision")
	}
	if facts.Provider != ProviderUQ || facts.ProviderRouteRef == "" || facts.TenantID == "" ||
		facts.BusinessID == "" || facts.SpaceScope == "" || facts.StepMillis <= 0 || facts.AlignmentMillis <= 0 {
		return QueryPlanFacts{}, errors.New("alarmd execution: incomplete query plan facts")
	}
	if facts.QueryDelaySeconds < 0 {
		return QueryPlanFacts{}, errors.New("alarmd execution: invalid query delay")
	}
	if facts.PromQL != nil {
		if facts.PromQL.Expression == "" || len(facts.QueryList) != 0 || facts.MetricMerge != "" || len(facts.TSDBMap) != 0 || !facts.Normalization.DatasetContract.DynamicDimensions {
			return QueryPlanFacts{}, errors.New("alarmd execution: invalid independent PromQL facts")
		}
	} else if len(facts.QueryList) == 0 || facts.MetricMerge == "" {
		return QueryPlanFacts{}, errors.New("alarmd execution: structured query facts are required")
	}
	if err := facts.Normalization.Validate(); err != nil {
		return QueryPlanFacts{}, err
	}
	for _, clause := range facts.QueryList {
		if clause.SourceConditions != nil && clause.FieldSemantics != "fta_event_tags/v1" {
			return QueryPlanFacts{}, errors.New("alarmd execution: source conditions require FTA semantics")
		}
		if clause.FieldSemantics != "" && clause.FieldSemantics != "fta_event_tags/v1" {
			return QueryPlanFacts{}, errors.New("alarmd execution: unsupported query field semantics")
		}
		if clause.FieldSemantics != "" && len(facts.TSDBMap[clause.ReferenceName]) == 0 {
			return QueryPlanFacts{}, errors.New("alarmd execution: field semantics require registered ES routing")
		}
		if clause.Driver == "" || clause.TimeField == "" {
			return QueryPlanFacts{}, errors.New("alarmd execution: incomplete query clause")
		}
		if len(clause.Conditions.Connectors) != 0 && len(clause.Conditions.Connectors)+1 != len(clause.Conditions.Fields) {
			return QueryPlanFacts{}, errors.New("alarmd execution: condition connectors must join adjacent fields")
		}
		if err := validateQueryFunctions(clause.Functions); err != nil {
			return QueryPlanFacts{}, err
		}
		if !isZeroQueryFunction(clause.TimeAggregation) {
			if err := validateQueryFunction(clause.TimeAggregation); err != nil {
				return QueryPlanFacts{}, err
			}
		}
		conditionGroups := []QueryConditions{clause.Conditions}
		if clause.SourceConditions != nil {
			conditionGroups = append(conditionGroups, *clause.SourceConditions)
		}
		for _, group := range conditionGroups {
			if len(group.Connectors) != 0 && len(group.Connectors)+1 != len(group.Fields) {
				return QueryPlanFacts{}, errors.New("alarmd execution: invalid source condition connectors")
			}
			for _, condition := range group.Fields {
				if condition.Field == "" || condition.Operator == "" || len(condition.Values) == 0 {
					return QueryPlanFacts{}, errors.New("alarmd execution: incomplete typed query condition")
				}
				for _, value := range condition.Values {
					if err := value.Validate(); err != nil {
						return QueryPlanFacts{}, err
					}
				}
			}
		}
	}
	for reference, storages := range facts.TSDBMap {
		found := false
		for _, clause := range facts.QueryList {
			if clause.ReferenceName == reference {
				found = true
			}
		}
		if !found || len(storages) == 0 {
			return QueryPlanFacts{}, errors.New("alarmd execution: unbound storage routing")
		}
		for _, storage := range storages {
			if storage.StorageID == "" || storage.StorageType != "elasticsearch" || storage.DB == "" || storage.TableID == "" || storage.Measurement == "" || storage.TimeField.Name == "" || storage.TimeField.Type == "" || !validQueryTimeField(storage.TimeField) {
				return QueryPlanFacts{}, errors.New("alarmd execution: incomplete ES storage routing")
			}
		}
	}
	// The domain and the formula behind this digest are frozen. The revision
	// is persisted with every published Plan, and Validate re-derives it and
	// refuses a Plan whose persisted revision differs; Validate is asserted
	// when a worker reads a frozen Plan, when the runtime-executable Catalog
	// is built, and when frozen DataRequirements are checked. Changing the
	// digest therefore fails every published Plan at the moment a new
	// binary reads it, fleet-wide, and the Plans a Catalog build retains
	// from the last good publication would meet freshly compiled ones under
	// different revisions. A change here is a Catalog generation migration:
	// re-derive retained facts, accept both generations while a release
	// rolls, rebuild the DataRequirement templates keyed by the revision.
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-plan-facts-v1", facts)
	if err != nil {
		return QueryPlanFacts{}, fmt.Errorf("alarmd execution: derive query revision: %w", err)
	}
	facts.QueryRevision = QueryRevision(digest)
	return facts, nil
}

func (value QueryScalar) Validate() error {
	switch value.Kind {
	case QueryScalarString:
		if value.NumberValue != "" || value.BoolValue {
			return errors.New("alarmd execution: invalid string query scalar")
		}
	case QueryScalarNumber:
		if value.NumberValue == "" || value.StringValue != "" || value.BoolValue {
			return errors.New("alarmd execution: invalid number query scalar")
		}
	case QueryScalarBoolean:
		if value.StringValue != "" || value.NumberValue != "" {
			return errors.New("alarmd execution: invalid boolean query scalar")
		}
	default:
		return errors.New("alarmd execution: unknown query scalar kind")
	}
	return nil
}

func validateQueryFunctions(functions []QueryFunction) error {
	for _, function := range functions {
		if err := validateQueryFunction(function); err != nil {
			return err
		}
	}
	return nil
}

func isZeroQueryFunction(function QueryFunction) bool {
	return function.Method == "" && function.Field == "" && !function.Without && len(function.Dimensions) == 0 &&
		function.Position == 0 && len(function.Arguments) == 0 && function.Window == "" && !function.Subquery &&
		function.Step == ""
}

func validateQueryFunction(function QueryFunction) error {
	if function.Method == "" || function.Position < 0 {
		return errors.New("alarmd execution: incomplete typed query function")
	}
	for _, argument := range function.Arguments {
		if err := argument.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (facts QueryPlanFacts) Validate() error {
	revision := facts.QueryRevision
	facts.QueryRevision = ""
	built, err := BuildQueryPlanFacts(facts)
	if err != nil {
		return err
	}
	if revision == "" || revision != built.QueryRevision {
		return errors.New("alarmd execution: query revision does not match query plan facts")
	}
	return nil
}

// WithMetricMerge derives an immutable logical query variant. It is used when
// one algorithm needs the same physical source facts with different expression
// semantics, such as OsRestart's filtered primary input and raw history input.
func (facts QueryPlanFacts) WithMetricMerge(metricMerge string) (QueryPlanFacts, error) {
	if err := facts.Validate(); err != nil {
		return QueryPlanFacts{}, err
	}
	if metricMerge == "" {
		return QueryPlanFacts{}, errors.New("alarmd execution: metric merge is required")
	}
	facts.QueryRevision = ""
	facts.MetricMerge = metricMerge
	return BuildQueryPlanFacts(facts)
}

func validQueryTimeField(field QueryTimeField) bool {
	if field.Type != "date" && field.Type != "long" {
		return false
	}
	switch field.Unit {
	case "second", "millisecond", "microsecond", "nanosecond":
		return true
	}
	return false
}
