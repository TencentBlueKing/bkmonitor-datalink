// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package uptime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestResolverAndProjectors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		dimensions       domain.DimensionMap
		reader           *uptimeReader
		wantStatus       domain.EnrichStatus
		wantModelInstID  string
		wantObject       string
		wantDisplayNames []string
		wantResourceDep  string
		wantDisplayDep   string
		wantLabels       []string
	}{
		{
			name: "task and node found",
			dimensions: domain.DimensionMap{
				rules.FieldTaskID: domain.NewStringScalar("007"), rules.FieldNodeID: domain.NewStringScalar("0:10.0.0.8"),
				rules.FieldTarget: domain.NewStringScalar("10.11.10.12"), rules.FieldTargetType: domain.NewStringScalar("ip"),
			},
			reader: &uptimeReader{
				task: models.UptimeTask{ID: 42, TaskID: 7, Name: "拨测任务", Protocol: models.UptimeProtocolICMP, BKBizID: 2, BKTenantID: "tenant-a"}, taskFound: true,
				node: models.UptimeNode{ID: 8, Name: "北京节点", PlatID: 0, IP: "10.0.0.8", BKTenantID: "tenant-a"}, nodeFound: true,
			},
			wantStatus: domain.EnrichStatusSucceeded, wantModelInstID: "42", wantObject: "拨测任务",
			wantDisplayNames: []string{"任务", "节点", "业务", "目标地址", "目标地址类型"},
			wantLabels:       []string{"bk_biz_id", "bk_biz_id|2", "cw-web_service", "cw-web_service|42"},
		},
		{
			name: "task missing keeps target",
			dimensions: domain.DimensionMap{
				rules.FieldTaskID: domain.NewStringScalar("7"), rules.FieldTarget: domain.NewStringScalar("10.11.10.12"),
			},
			reader: &uptimeReader{}, wantStatus: domain.EnrichStatusPartial,
			wantObject: "7（该拨测任务已被删除）", wantDisplayNames: []string{"任务", "目标地址"},
			wantResourceDep: rules.DependencyUptime,
		},
		{
			name: "node failure keeps raw node",
			dimensions: domain.DimensionMap{
				rules.FieldTaskID: domain.NewStringScalar("7"), rules.FieldNodeID: domain.NewStringScalar("0:10.0.0.8"),
			},
			reader: &uptimeReader{
				task: models.UptimeTask{ID: 42, TaskID: 7, Name: "拨测任务", Protocol: models.UptimeProtocolICMP, BKBizID: 2, BKTenantID: "tenant-a"}, taskFound: true,
				nodeErr: errors.New("unavailable"),
			},
			wantStatus: domain.EnrichStatusSucceeded, wantModelInstID: "42", wantObject: "拨测任务",
			wantDisplayNames: []string{"任务", "节点", "业务"}, wantDisplayDep: rules.DependencyUptime,
			wantLabels: []string{"bk_biz_id", "bk_biz_id|2", "cw-web_service", "cw-web_service|42"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := enrich.Sources{Uptime: test.reader, UptimeNode: test.reader, Model: test.reader, OneModel: test.reader}
			scope, err := enrich.NewScope(uptimeAlert(t, test.dimensions), sources)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, rules.UptimeModelCode, test.dimensions)
			if err != nil {
				t.Fatal(err)
			}
			if got := status(result.ResourceDiagnostics); got != test.wantStatus || result.Resource.ModelInstID != test.wantModelInstID ||
				dependency(result.ResourceDiagnostics) != test.wantResourceDep {
				t.Fatalf("result=%#v status=%q", result, got)
			}
			if !slices.Equal(result.Resource.CWLabels, test.wantLabels) {
				t.Fatalf("cw_labels=%#v, want %#v", result.Resource.CWLabels, test.wantLabels)
			}
			if result.Object != test.wantObject || dependency(result.DisplayDiagnostics) != test.wantDisplayDep || len(result.Dimensions) != len(test.wantDisplayNames) {
				t.Fatalf("result=%#v", result)
			}
			if test.wantDisplayDep == "" && test.name == "task and node found" {
				if got, _ := result.Dimensions[0].Value.StringValue(); got != "拨测任务" {
					t.Fatalf("task dimension=%#v", result.Dimensions[0])
				}
				if got, _ := result.Dimensions[1].Value.StringValue(); got != "北京节点" {
					t.Fatalf("node dimension=%#v", result.Dimensions[1])
				}
			}
			for index, name := range test.wantDisplayNames {
				if result.Dimensions[index].Name != name {
					t.Fatalf("dimensions[%d]=%#v", index, result.Dimensions[index])
				}
			}
		})
	}
}

func TestUptimeReadsOneModelBusinessAndTargetCloud(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{
		rules.FieldTaskID: domain.NewStringScalar("7"), rules.FieldTargetHost: domain.NewStringScalar("192.0.2.8"),
		rules.FieldBKTargetCloudID: domain.NewStringScalar("0"),
	}
	reader := &uptimeReader{
		task: models.UptimeTask{ID: 42, TaskID: 7, Name: "拨测任务", Protocol: models.UptimeProtocolTCP, BKBizID: 2, BKTenantID: "tenant-a"}, taskFound: true,
		business: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{rules.FieldBKBizID: int64(2), rules.FieldBKBizName: "业务二"}}, businessFound: true,
		cloudHost: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Attributes: map[string]any{rules.FieldBKHostInnerIP: "192.0.2.8", rules.FieldBKCloudID: json.Number("0"), rules.FieldBKCloudName: "默认区域"}}, cloudFound: true,
	}
	scope, err := enrich.NewScope(uptimeAlert(t, dimensions), enrich.Sources{Uptime: reader, UptimeNode: reader, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Enrich(context.Background(), scope, rules.UptimeModelCode, dimensions, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.BKBizID != int64(2) || result.Resource.BKBizName != "业务二" || result.Resource.BKCloudID != int64(0) || result.Resource.BKCloudName != "默认区域" ||
		!slices.Equal(result.Resource.CWLabels, []string{"bk_biz_id", "bk_biz_id|2", "cw-web_service", "cw-web_service|42"}) || len(result.ResourceDiagnostics) != 0 {
		t.Fatalf("resource=%#v diagnostics=%#v", result.Resource, result.ResourceDiagnostics)
	}
	if len(reader.instanceQueries) != 2 || reader.instanceQueries[0].ModelCode != "cw-biz" || reader.instanceQueries[0].InstanceID != "2" ||
		reader.instanceQueries[1].ModelCode != rules.HostModelCode || len(reader.instanceQueries[1].AttributeFilters) != 2 {
		t.Fatalf("queries=%#v", reader.instanceQueries)
	}
	if len(result.Dimensions) != 4 || result.Dimensions[1].Name != "业务" || result.Dimensions[2].Name != "云区域" {
		t.Fatalf("dimensions=%#v", result.Dimensions)
	}
	if got, _ := result.Dimensions[1].Value.StringValue(); got != "业务二" {
		t.Fatalf("business=%#v", result.Dimensions[1])
	}
	if got, _ := result.Dimensions[2].Value.StringValue(); got != "默认区域" {
		t.Fatalf("cloud=%#v", result.Dimensions[2])
	}
}

func TestDisplayProjectorSelectsTargetDimensionsByProtocol(t *testing.T) {
	t.Parallel()
	allTargets := domain.DimensionMap{
		rules.FieldTaskID:     domain.NewStringScalar("7"),
		rules.FieldURL:        domain.NewStringScalar("https://example.com/health"),
		rules.FieldTargetPort: domain.NewStringScalar("443"),
		rules.FieldTargetHost: domain.NewStringScalar("10.0.0.7"),
		rules.FieldTarget:     domain.NewStringScalar("example.com"),
		rules.FieldTargetType: domain.NewStringScalar("domain"),
	}
	for _, test := range []struct {
		name     string
		protocol models.UptimeProtocol
		want     []string
	}{
		{name: "http", protocol: models.UptimeProtocolHTTP, want: []string{"任务", "业务", "目标url"}},
		{name: "tcp", protocol: models.UptimeProtocolTCP, want: []string{"任务", "业务", "目标端口", "目标IP"}},
		{name: "udp", protocol: models.UptimeProtocolUDP, want: []string{"任务", "业务", "目标端口", "目标IP"}},
		{name: "icmp", protocol: models.UptimeProtocolICMP, want: []string{"任务", "业务", "目标地址", "目标地址类型"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolution := Resolution{
				tenantID: "tenant-a", taskInputState: taskInputValid, taskIdentity: "7",
				taskRaw: domain.NewStringScalar("7"), taskFound: true,
				task: models.UptimeTask{
					ID: 42, TaskID: 7, Name: "拨测任务", Protocol: test.protocol,
					BKBizID: 2, BKTenantID: "tenant-a",
				},
			}
			projection, err := projectDisplay(context.Background(), resolution, allTargets, models.ResourceValues{BKBizID: int64(2)})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(projection.Dimensions))
			for index, dimension := range projection.Dimensions {
				got[index] = dimension.Name
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("dimension names=%v, want %v", got, test.want)
			}
		})
	}
}

func TestUptimeRejectsMismatchedOneModelNames(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{
		rules.FieldTaskID: domain.NewStringScalar("7"), rules.FieldBKTargetIP: domain.NewStringScalar("192.0.2.8"),
		rules.FieldBKTargetCloudID: domain.NewStringScalar("0"),
	}
	for _, test := range []struct {
		name        string
		business    enrich.Instance
		cloudHost   enrich.Instance
		businessErr error
		cloudErr    error
	}{
		{name: "tenant and cloud mismatch", business: enrich.Instance{TenantID: "tenant-b", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{rules.FieldBKBizName: "foreign"}},
			cloudHost: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Attributes: map[string]any{rules.FieldBKHostInnerIP: "192.0.2.8", rules.FieldBKCloudID: int64(7), rules.FieldBKCloudName: "wrong"}}},
		{name: "reader errors", businessErr: errors.New("business unavailable"), cloudErr: errors.New("host unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &uptimeReader{
				task: models.UptimeTask{ID: 42, TaskID: 7, Name: "拨测任务", BKBizID: 2, BKTenantID: "tenant-a"}, taskFound: true,
				business: test.business, businessFound: test.businessErr == nil, businessErr: test.businessErr,
				cloudHost: test.cloudHost, cloudFound: test.cloudErr == nil, cloudErr: test.cloudErr,
			}
			scope, err := enrich.NewScope(uptimeAlert(t, dimensions), enrich.Sources{Uptime: reader, Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, rules.UptimeModelCode, dimensions, 2)
			if err != nil {
				t.Fatal(err)
			}
			if result.Resource.BKBizID != int64(2) || result.Resource.BKBizName != "" || result.Resource.BKCloudName != "" || len(result.ResourceDiagnostics) != 0 ||
				!slices.Equal(result.Resource.CWLabels, []string{"bk_biz_id", "bk_biz_id|2", "cw-web_service", "cw-web_service|42"}) {
				t.Fatalf("resource=%#v diagnostics=%#v", result.Resource, result.ResourceDiagnostics)
			}
		})
	}
}

func TestUptimeMissingTaskKeepsSourceBusinessLabel(t *testing.T) {
	t.Parallel()
	reader := &uptimeReader{business: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{rules.FieldBKBizID: int64(2), rules.FieldBKBizName: "业务二"}}, businessFound: true}
	dimensions := domain.DimensionMap{rules.FieldTarget: domain.NewStringScalar("example.com")}
	scope, err := enrich.NewScope(uptimeAlert(t, dimensions), enrich.Sources{Uptime: reader, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Enrich(context.Background(), scope, rules.UptimeModelCode, dimensions, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.ModelInstID != "" || result.Resource.BKBizID != int64(2) || result.Resource.BKBizName != "业务二" ||
		!slices.Equal(result.Resource.CWLabels, []string{"bk_biz_id", "bk_biz_id|2"}) || result.Object != "0（该拨测任务已被删除）" ||
		len(result.Dimensions) != 3 || result.Dimensions[1].Name != "业务" {
		t.Fatalf("result=%#v", result)
	}
}

func TestResolverCachesSharedDependenciesAcrossProjectors(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{
		rules.FieldTaskID: domain.NewStringScalar("7"), rules.FieldNodeID: domain.NewStringScalar("0:10.0.0.8"),
	}
	reader := &countingUptimeReader{uptimeReader: uptimeReader{
		task: models.UptimeTask{ID: 42, TaskID: 7, Name: "拨测任务", Protocol: models.UptimeProtocolICMP, BKBizID: 2, BKTenantID: "tenant-a"}, taskFound: true,
		node: models.UptimeNode{ID: 8, Name: "北京节点", PlatID: 0, IP: "10.0.0.8", BKTenantID: "tenant-a"}, nodeFound: true,
	}}
	scope, err := enrich.NewScope(uptimeAlert(t, dimensions), enrich.Sources{Uptime: reader, UptimeNode: reader, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Enrich(context.Background(), scope, rules.UptimeModelCode, dimensions); err != nil {
			t.Fatal(err)
		}
	}
	if reader.taskCalls != 1 || reader.nodeCalls != 1 || reader.modelCalls != 1 {
		t.Fatalf("calls task=%d node=%d model=%d", reader.taskCalls, reader.nodeCalls, reader.modelCalls)
	}
}

func TestResolverKeepsAlertUnchanged(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{
		rules.FieldTaskID: domain.NewStringScalar("7"), rules.FieldNodeID: domain.NewStringScalar("0:10.0.0.8"),
	}
	alert := uptimeAlert(t, dimensions)
	original := alert.Clone()
	reader := &uptimeReader{
		task: models.UptimeTask{ID: 42, TaskID: 7, Name: "拨测任务", Protocol: models.UptimeProtocolICMP, BKBizID: 2, BKTenantID: "tenant-a"}, taskFound: true,
	}
	scope, err := enrich.NewScope(alert, enrich.Sources{Uptime: reader, UptimeNode: reader, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Enrich(context.Background(), scope, rules.UptimeModelCode, dimensions); err != nil {
		t.Fatal(err)
	}
	if !equalAlert(alert, original) {
		t.Fatalf("uptime module changed Alert: before=%#v after=%#v", original, alert)
	}
}

type uptimeReader struct {
	business        enrich.Instance
	businessFound   bool
	businessErr     error
	cloudHost       enrich.Instance
	cloudFound      bool
	cloudErr        error
	instanceQueries []enrich.InstanceQuery
	task            models.UptimeTask
	taskFound       bool
	taskErr         error
	node            models.UptimeNode
	nodeFound       bool
	nodeErr         error
}

type countingUptimeReader struct {
	uptimeReader
	taskCalls  int
	nodeCalls  int
	modelCalls int
}

func (r *countingUptimeReader) GetUptimeTask(ctx context.Context, tenantID, taskID string) (models.UptimeTask, bool, error) {
	r.taskCalls++
	return r.uptimeReader.GetUptimeTask(ctx, tenantID, taskID)
}

func (r *countingUptimeReader) GetUptimeNode(ctx context.Context, tenantID, nodeID string) (models.UptimeNode, bool, error) {
	r.nodeCalls++
	return r.uptimeReader.GetUptimeNode(ctx, tenantID, nodeID)
}

func (r *countingUptimeReader) GetModelByCode(ctx context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	r.modelCalls++
	return r.uptimeReader.GetModelByCode(ctx, tenantID, modelCode)
}

func (r *uptimeReader) GetUptimeTask(context.Context, string, string) (models.UptimeTask, bool, error) {
	return r.task, r.taskFound, r.taskErr
}

func (r *uptimeReader) GetUptimeNode(context.Context, string, string) (models.UptimeNode, bool, error) {
	return r.node, r.nodeFound, r.nodeErr
}

func (r *uptimeReader) GetModelByCode(_ context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	return enrich.Model{
		TenantID: tenantID, ModelID: "55", ModelCode: modelCode,
		Fields: map[string]any{rules.FieldObjectModelName: "Website Service"},
	}, true, nil
}

func (r *uptimeReader) FindInstance(_ context.Context, _ string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.instanceQueries = append(r.instanceQueries, query)
	if query.ModelCode == "cw-biz" {
		return r.business, r.businessFound, r.businessErr
	}
	if query.ModelCode == rules.HostModelCode {
		return r.cloudHost, r.cloudFound, r.cloudErr
	}
	return enrich.Instance{}, false, nil
}

func uptimeAlert(t *testing.T, dimensions domain.DimensionMap) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{
		EventSourceVersion: 1, AlertID: "alert-uptime-module", BKTenantID: "tenant-a",
		EventSourceID: "built_in_bk", Fingerprint: "uptime-module", Title: "source title",
		Content: "source content", Severity: "warning", Dimensions: dimensions,
		Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive,
		LatestEventID: "event-1", TriggerEventID: "event-1", LastOccurredAt: now, UpdateAt: now,
		BeginAt: now, CreateAt: now, EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func equalAlert(left, right domain.Alert) bool { return reflect.DeepEqual(left, right) }

func status(diagnostics []enrich.Diagnostic) domain.EnrichStatus {
	if len(diagnostics) == 0 {
		return domain.EnrichStatusSucceeded
	}
	return domain.EnrichStatusPartial
}

func dependency(diagnostics []enrich.Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Dependency != "" {
			return diagnostic.Dependency
		}
	}
	return ""
}
