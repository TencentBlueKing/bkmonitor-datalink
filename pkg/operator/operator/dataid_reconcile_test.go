// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/prometheus/config"
	promdiscovery "github.com/prometheus/prometheus/discovery"
	promk8ssd "github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/dataidwatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/objectsref"
)

type fakeMonitorResourceLister struct {
	objects []any
	listed  chan struct{}
	release chan struct{}
}

func (l *fakeMonitorResourceLister) ListAll(_ labels.Selector, appendFn cache.AppendFunc) error {
	for _, obj := range l.objects {
		if l.listed != nil {
			close(l.listed)
			<-l.release
		}
		appendFn(obj)
	}
	return nil
}

type fakeDiscover struct {
	name        string
	dataID      *bkv1beta1.DataID
	startErr    error
	startCount  int
	stopCount   int
	reloadCount int
}

type fakeDataIDWatcher struct {
	metricDataID *bkv1beta1.DataID
	metricErr    error
}

var _ dataidwatcher.Watcher = (*fakeDataIDWatcher)(nil)

func (w *fakeDataIDWatcher) Start() error { return nil }

func (w *fakeDataIDWatcher) Stop() {}

func (w *fakeDataIDWatcher) DataIDs() []*bkv1beta1.DataID {
	if w.metricDataID == nil {
		return nil
	}
	return []*bkv1beta1.DataID{w.metricDataID}
}

func (w *fakeDataIDWatcher) MatchMetricDataID(
	_ define.MonitorMeta,
	_ bool,
) (*bkv1beta1.DataID, error) {
	return w.metricDataID, w.metricErr
}

func (w *fakeDataIDWatcher) MatchEventDataID(
	_ define.MonitorMeta,
	_ bool,
) (*bkv1beta1.DataID, error) {
	return nil, errors.New("event dataid not configured")
}

func (w *fakeDataIDWatcher) GetClusterInfo() (*define.ClusterInfo, error) {
	return nil, errors.New("cluster info not configured")
}

func (d *fakeDiscover) Name() string { return d.name }

func (d *fakeDiscover) UK() string { return d.name }

func (d *fakeDiscover) Type() string { return monitorKindServiceMonitor }

func (d *fakeDiscover) IsSystem() bool { return true }

func (d *fakeDiscover) Start() error {
	d.startCount++
	return d.startErr
}

func (d *fakeDiscover) Stop() { d.stopCount++ }

func (d *fakeDiscover) Reload() error {
	d.reloadCount++
	return nil
}

func (d *fakeDiscover) MonitorMeta() define.MonitorMeta { return define.MonitorMeta{} }

func (d *fakeDiscover) DataID() *bkv1beta1.DataID { return d.dataID }

func (d *fakeDiscover) SetDataID(dataID *bkv1beta1.DataID) { d.dataID = dataID }

func (d *fakeDiscover) DaemonSetChildConfigs() []*discover.ChildConfig { return nil }

func (d *fakeDiscover) StatefulSetChildConfigs() []*discover.ChildConfig { return nil }

func TestAddDiscoverIfAbsent(t *testing.T) {
	t.Run("starts and registers a missing discover", func(t *testing.T) {
		op := &Operator{discovers: make(map[string]discover.Discover)}
		candidate := &fakeDiscover{name: "ServiceMonitor:default/example:0"}

		added, err := op.addDiscoverIfAbsent(candidate)

		require.NoError(t, err)
		assert.True(t, added)
		assert.Equal(t, 1, candidate.startCount)
		assert.Same(t, candidate, op.discovers[candidate.Name()])
	})

	t.Run("keeps an existing discover running", func(t *testing.T) {
		existing := &fakeDiscover{name: "ServiceMonitor:default/example:0"}
		candidate := &fakeDiscover{name: existing.Name()}
		op := &Operator{discovers: map[string]discover.Discover{existing.Name(): existing}}

		added, err := op.addDiscoverIfAbsent(candidate)

		require.NoError(t, err)
		assert.False(t, added)
		assert.Zero(t, candidate.startCount)
		assert.Zero(t, candidate.stopCount)
		assert.Zero(t, existing.startCount)
		assert.Zero(t, existing.stopCount)
		assert.Same(t, existing, op.discovers[existing.Name()])
	})

	t.Run("does not register a discover that fails to start", func(t *testing.T) {
		op := &Operator{discovers: make(map[string]discover.Discover)}
		candidate := &fakeDiscover{
			name:     "ServiceMonitor:default/example:0",
			startErr: errors.New("start failed"),
		}

		added, err := op.addDiscoverIfAbsent(candidate)

		assert.EqualError(t, err, "start failed")
		assert.False(t, added)
		assert.Equal(t, 1, candidate.startCount)
		assert.Empty(t, op.discovers)
	})
}

func assertMonitorDiscoverRecovery(
	t *testing.T,
	resource any,
	expectedType any,
) {
	t.Helper()

	op := &Operator{discovers: make(map[string]discover.Discover)}
	lister := &fakeMonitorResourceLister{objects: []any{resource}}
	candidate := &fakeDiscover{name: "monitor:default/example:0"}
	dataIDAvailable := false
	create := func(obj any) []discover.Discover {
		assert.IsType(t, expectedType, obj)
		if !dataIDAvailable {
			return nil
		}
		return []discover.Discover{candidate}
	}

	require.NoError(t, op.recoverMonitorDiscovers(lister, create))
	assert.Empty(t, op.discovers)

	dataIDAvailable = true
	require.NoError(t, op.recoverMonitorDiscovers(lister, create))
	assert.Equal(t, 1, candidate.startCount)
	assert.Same(t, candidate, op.discovers[candidate.Name()])

	require.NoError(t, op.recoverMonitorDiscovers(lister, create))
	assert.Equal(t, 1, candidate.startCount)
}

func TestRecoverServiceMonitorDiscovers(t *testing.T) {
	assertMonitorDiscoverRecovery(
		t,
		&promv1.ServiceMonitor{},
		&promv1.ServiceMonitor{},
	)
}

func TestRecoverPodMonitorDiscovers(t *testing.T) {
	assertMonitorDiscoverRecovery(
		t,
		&promv1.PodMonitor{},
		&promv1.PodMonitor{},
	)
}

func TestMonitorDeleteCannotBeOverwrittenByStaleRecovery(t *testing.T) {
	tests := []struct {
		name     string
		resource any
		builder  monitorDiscoverBuilder
		remove   func(*Operator, any)
	}{
		{
			name: "ServiceMonitor",
			resource: &promv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "default"},
				Spec:       promv1.ServiceMonitorSpec{Endpoints: []promv1.Endpoint{{}}},
			},
			builder: func(any) []discover.Discover {
				return []discover.Discover{&fakeDiscover{name: define.MonitorMeta{
					Kind: monitorKindServiceMonitor, Namespace: "default", Name: "example",
				}.ID()}}
			},
			remove: func(op *Operator, obj any) { op.handleServiceMonitorDelete(obj) },
		},
		{
			name: "PodMonitor",
			resource: &promv1.PodMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "default"},
				Spec: promv1.PodMonitorSpec{
					PodMetricsEndpoints: []promv1.PodMetricsEndpoint{{}},
				},
			},
			builder: func(any) []discover.Discover {
				return []discover.Discover{&fakeDiscover{name: define.MonitorMeta{
					Kind: monitorKindPodMonitor, Namespace: "default", Name: "example",
				}.ID()}}
			},
			remove: func(op *Operator, obj any) { op.handlePodMonitorDelete(obj) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listed := make(chan struct{})
			release := make(chan struct{})
			lister := &fakeMonitorResourceLister{
				objects: []any{tt.resource},
				listed:  listed,
				release: release,
			}
			op := &Operator{discovers: make(map[string]discover.Discover)}

			recovered := make(chan error, 1)
			go func() {
				recovered <- op.recoverMonitorDiscovers(lister, tt.builder)
			}()
			<-listed

			deleted := make(chan struct{})
			go func() {
				tt.remove(op, tt.resource)
				close(deleted)
			}()

			select {
			case <-deleted:
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			require.NoError(t, <-recovered)
			<-deleted

			assert.Empty(t, op.discovers)
		})
	}
}

func newDataIDRecoveryTestOperator(
	watcher dataidwatcher.Watcher,
) *Operator {
	return &Operator{
		ctx:               context.Background(),
		client:            k8sfake.NewSimpleClientset(),
		dw:                watcher,
		objectsController: &objectsref.ObjectsController{},
	}
}

func TestServiceMonitorDiscoversFromObjectRetriesAfterDataIDArrives(t *testing.T) {
	watcher := &fakeDataIDWatcher{metricErr: errors.New("system dataid not found")}
	op := newDataIDRecoveryTestOperator(watcher)
	serviceMonitor := &promv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example",
			Namespace: "default",
		},
		Spec: promv1.ServiceMonitorSpec{
			Endpoints: []promv1.Endpoint{{}},
		},
	}

	assert.Empty(t, op.serviceMonitorDiscoversFromObject(serviceMonitor))

	watcher.metricDataID = newTestMetricDataID()
	watcher.metricErr = nil
	discovers := op.serviceMonitorDiscoversFromObject(serviceMonitor)
	require.Len(t, discovers, 1)
	assert.Equal(t, watcher.metricDataID, discovers[0].DataID())
}

func TestPodMonitorDiscoversFromObjectRetriesAfterDataIDArrives(t *testing.T) {
	watcher := &fakeDataIDWatcher{metricErr: errors.New("system dataid not found")}
	op := newDataIDRecoveryTestOperator(watcher)
	podMonitor := &promv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example",
			Namespace: "default",
		},
		Spec: promv1.PodMonitorSpec{
			PodMetricsEndpoints: []promv1.PodMetricsEndpoint{{}},
		},
	}

	assert.Empty(t, op.podMonitorDiscoversFromObject(podMonitor))

	watcher.metricDataID = newTestMetricDataID()
	watcher.metricErr = nil
	discovers := op.podMonitorDiscoversFromObject(podMonitor)
	require.Len(t, discovers, 1)
	assert.Equal(t, watcher.metricDataID, discovers[0].DataID())
}

func newTestMetricDataID() *bkv1beta1.DataID {
	return &bkv1beta1.DataID{
		ObjectMeta: metav1.ObjectMeta{Name: "custommetricdataid"},
		Spec: bkv1beta1.DataIDSpec{
			DataID: 1001,
			Labels: map[string]string{
				"bcs_cluster_id": "test-cluster",
				"bk_biz_id":      "2",
			},
		},
	}
}

func TestCreatePromScrapeConfigDiscoversRetriesAfterDataIDArrives(t *testing.T) {
	previousKinds := configs.G().PromSDKinds
	configs.G().PromSDKinds = configs.PromSDKinds{monitorKindKubernetesSd}
	t.Cleanup(func() {
		configs.G().PromSDKinds = previousKinds
	})

	dataID := newTestMetricDataID()
	watcher := &fakeDataIDWatcher{
		metricDataID: dataID,
		metricErr:    errors.New("common dataid not found"),
	}
	op := &Operator{
		ctx:               context.Background(),
		client:            k8sfake.NewSimpleClientset(),
		dw:                watcher,
		objectsController: &objectsref.ObjectsController{},
	}
	scrapeConfigs := []resourceScrapConfig{
		{
			Namespace: "default",
			Resource:  "secret/prometheus.yaml",
			Config: config.ScrapeConfig{
				JobName: "example",
				ServiceDiscoveryConfigs: promdiscovery.Configs{
					&promk8ssd.SDConfig{Role: promk8ssd.RolePod},
				},
			},
		},
	}

	assert.Empty(t, op.createPromScrapeConfigDiscovers(scrapeConfigs))

	watcher.metricErr = nil
	discovers := op.createPromScrapeConfigDiscovers(scrapeConfigs)
	require.Len(t, discovers, 1)
	assert.Equal(t, dataID, discovers[0].DataID())
}

func TestReloadAllDiscoversKeepsExistingBehavior(t *testing.T) {
	oldDataID := &bkv1beta1.DataID{
		ObjectMeta: metav1.ObjectMeta{Name: "k8smetricdataid"},
		Spec:       bkv1beta1.DataIDSpec{DataID: 1001},
	}
	newDataID := oldDataID.DeepCopy()
	newDataID.Spec.DataID = 1002

	watcher := &fakeDataIDWatcher{metricDataID: newDataID}
	existing := &fakeDiscover{name: "ServiceMonitor:default/example:0", dataID: oldDataID}
	op := &Operator{
		dw:        watcher,
		discovers: map[string]discover.Discover{existing.Name(): existing},
	}

	op.reloadAllDiscovers()

	assert.Same(t, newDataID, existing.dataID)
	assert.Equal(t, 1, existing.reloadCount)
	assert.Zero(t, existing.stopCount)
}

func TestSnapshotPromScrapeConfigs(t *testing.T) {
	expected := resourceScrapConfig{
		Namespace: "default",
		Resource:  "secret/prometheus.yaml",
		Config:    config.ScrapeConfig{JobName: "example"},
	}
	op := &Operator{
		prevResourceScrapeConfigs: map[string]resourceScrapConfig{
			"secret/prometheus.yaml/example": expected,
		},
	}

	snapshot := op.snapshotPromScrapeConfigs()
	delete(op.prevResourceScrapeConfigs, "secret/prometheus.yaml/example")

	assert.Equal(t, []resourceScrapConfig{expected}, snapshot)
}

func TestPromSdReloadWaitsForRecoveryReconcile(t *testing.T) {
	existing := resourceScrapConfig{
		Resource: "secret/prometheus.yaml",
		Config:   config.ScrapeConfig{JobName: "example"},
	}
	op := &Operator{
		discovers: make(map[string]discover.Discover),
		prevResourceScrapeConfigs: map[string]resourceScrapConfig{
			"secret/prometheus.yaml/example": existing,
		},
	}

	// 模拟恢复流程已进入临界区且持有旧快照。
	op.promSdReconcileMut.Lock()
	reloaded := make(chan struct{})
	go func() {
		op.reconcilePromScrapeConfigs(nil)
		close(reloaded)
	}()

	select {
	case <-reloaded:
		t.Fatal("PromSD reload must wait for the recovery reconcile")
	case <-time.After(100 * time.Millisecond):
	}
	assert.Contains(t, op.prevResourceScrapeConfigs, "secret/prometheus.yaml/example")

	op.promSdReconcileMut.Unlock()
	<-reloaded
	assert.Empty(t, op.prevResourceScrapeConfigs)
}
