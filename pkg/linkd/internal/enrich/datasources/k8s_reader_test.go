package datasources

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

func TestOneModelK8sReaderMapsModelIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model string
		dimensions  map[string]string
		want        map[string]string
	}{
		{"cluster", "cw-K8s_Cluster", map[string]string{}, map[string]string{}},
		{"namespace", "cw-K8s_Namespace", map[string]string{"namespace": "default"}, map[string]string{"namespace": "default"}},
		{"service", "cw-K8s_Service", map[string]string{"namespace": "default", "service": "web"}, map[string]string{"namespace": "default", "name": "web"}},
		{"node", "cw-K8s_Node", map[string]string{"node": "node-1", "service": "must-not-be-used"}, map[string]string{"name": "node-1"}},
		{"workload", "cw-K8s_Workload", map[string]string{"namespace": "default", "workload_kind": "Deployment", "workload_name": "web"}, map[string]string{"namespace": "default", "type": "Deployment", "name": "web"}},
		{"pod", "cw-K8s_Pod", map[string]string{"namespace": "default", "pod_name": "web-1"}, map[string]string{"namespace": "default", "name": "web-1"}},
		{"standalone pod", "cw-K8s_Pod", map[string]string{"namespace": "default", "workload_kind": "Pod", "workload_name": "standalone"}, map[string]string{"namespace": "default", "name": "standalone"}},
		{"container", "cw-K8s_Container", map[string]string{"namespace": "default", "pod_name": "web-1", "container_name": "nginx"}, map[string]string{"namespace": "default", "pod_name": "web-1", "name": "nginx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dims := domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1")}
			for k, v := range tc.dimensions {
				dims[k] = domain.NewStringScalar(v)
			}
			base := &k8sInstanceReaderProbe{}
			reader, err := NewOneModelK8sReader(base)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = reader.FindK8sInstance(t.Context(), "tenant-a", tc.model, dims)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"cluster_id": "cluster-1"}
			for k, v := range tc.want {
				want[k] = v
			}
			got := map[string]string{}
			for _, f := range base.query.AttributeFilters {
				if f.Type != enrich.InstanceAttributeKeyword {
					t.Fatalf("wrong identity filter type: %+v", f)
				}
				got[f.Field] = f.Value.(string)
			}
			if base.calls != 1 || base.tenant != "tenant-a" || base.query.ModelCode != tc.model || !reflect.DeepEqual(got, want) {
				t.Fatalf("calls=%d tenant=%s query=%+v filters=%v want=%v", base.calls, base.tenant, base.query, got, want)
			}
		})
	}
}

func TestOneModelK8sReaderRejectsMissingIdentityAndPropagatesFailure(t *testing.T) {
	t.Parallel()
	base := &k8sInstanceReaderProbe{}
	reader, err := NewOneModelK8sReader(base)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = reader.FindK8sInstance(t.Context(), "tenant-a", "cw-K8s_Node", domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "service": domain.NewStringScalar("wrong-node")})
	if err == nil || !strings.Contains(err.Error(), "node") || base.calls != 0 {
		t.Fatalf("missing node reached OneModel: calls=%d err=%v", base.calls, err)
	}
	_, _, err = reader.FindK8sInstance(t.Context(), "tenant-a", "unknown", domain.DimensionMap{})
	if err == nil || base.calls != 0 {
		t.Fatal("unknown model accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dims := domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "node": domain.NewStringScalar("node-1")}
	_, _, err = reader.FindK8sInstance(ctx, "tenant-a", "cw-K8s_Node", dims)
	if !errors.Is(err, context.Canceled) || base.calls != 0 {
		t.Fatalf("cancellation ignored: calls=%d err=%v", base.calls, err)
	}
	dependency := errors.New("backend unavailable")
	base.err = dependency
	_, _, err = reader.FindK8sInstance(t.Context(), "tenant-a", "cw-K8s_Node", dims)
	if !errors.Is(err, dependency) || base.calls != 1 {
		t.Fatalf("dependency failure lost: %v", err)
	}
}

type k8sInstanceReaderProbe struct {
	query  enrich.InstanceQuery
	tenant string
	calls  int
	err    error
}

func (r *k8sInstanceReaderProbe) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.calls++
	r.tenant = tenant
	r.query = query
	return enrich.Instance{}, false, r.err
}
