// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/rollout"
)

// rolloutCluster is one Deployment whose image writes land and whose
// rollout the test never completes.
type rolloutCluster struct {
	mu  sync.Mutex
	dep rollout.Deployment
}

func (c *rolloutCluster) GetDeployment(context.Context, string, string) (rollout.Deployment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dep := c.dep
	dep.Containers = append([]rollout.Container(nil), c.dep.Containers...)
	return dep, nil
}

func (c *rolloutCluster) SetImage(_ context.Context, _, _, uid string, index int, container, expected, image string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if uid != c.dep.UID || c.dep.Containers[index].Name != container || c.dep.Containers[index].Image != expected {
		return 0, rollout.ErrImageChanged
	}
	c.dep.Containers[index].Image = image
	c.dep.Generation++
	c.dep.Updated, c.dep.StatusReplicas = 0, 2
	return c.dep.Generation, nil
}

func rolloutFixture(t *testing.T) (*Channel, *[]SREAction) {
	t.Helper()
	cluster := &rolloutCluster{dep: rollout.Deployment{UID: "deploy-uid", Generation: 1, ObservedGeneration: 1, Replicas: 1, StatusReplicas: 1,
		Updated: 1, Available: 1, ProgressDeadlineSeconds: 600, Strategy: "RollingUpdate", MaxSurge: 1,
		Containers: []rollout.Container{{Name: "web", Image: "web:v1"}}}}
	store := rollout.NewStore(redistest.Start(t).Client(), "test.sre:{env}:")
	driver := rollout.NewDriver(rollout.DriverOptions{Store: store, Cluster: cluster, Owner: "replica-1/1", Poll: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { driver.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	var mu sync.Mutex
	actions := []SREAction{}
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Rollouts: driver,
		Audit: func(action SREAction) {
			mu.Lock()
			defer mu.Unlock()
			actions = append(actions, action)
		}})})
	if err != nil {
		t.Fatal(err)
	}
	return channel, &actions
}

// An image change answers its receipt at once with the image it replaces,
// is audited with the receipt and both images, and points to the read that
// follows it; that read names the image now running and who watches it. A
// change on a Deployment replaced since discovery is refused and audited as
// target_changed.
func TestAnImageChangeIsAuditedAndReadByItsReceipt(t *testing.T) {
	channel, actions := rolloutFixture(t)
	status, response := call(t, channel, envelope(channel, "invoke", OpSetImage, Params{"namespace": "fixture", "name": "web", "uid": "deploy-uid", "container": "web", "image": "web:v2"}))
	if status != 200 || response.Error != nil {
		t.Fatalf("set_image: HTTP %d %+v", status, response)
	}
	var receipt rollout.Receipt
	encoded, _ := json.Marshal(response.Result)
	if err := json.Unmarshal(encoded, &receipt); err != nil || receipt.ID == "" || receipt.Previous != "web:v1" || receipt.State != rollout.StateRollingOut || !receipt.Applied {
		t.Fatalf("receipt %s %v", encoded, err)
	}
	if len(response.Next) != 1 || response.Next[0].Operation != OpRollout || response.Next[0].Params["receipt_id"] != receipt.ID {
		t.Fatalf("next %+v, want the receipt read", response.Next)
	}
	if len(*actions) != 1 {
		t.Fatalf("actions %+v", *actions)
	}
	action := (*actions)[0]
	if action.Op != OpSetImage || action.Result != SREResultDone || action.UID != "deploy-uid" || action.SessionID != "post-observation-session" ||
		!strings.Contains(action.Detail, receipt.ID) || !strings.Contains(action.Detail, "web:v1 -> web:v2") {
		t.Fatalf("audit %+v", action)
	}
	status, response = call(t, channel, envelope(channel, "invoke", OpRollout, Params{"receipt_id": receipt.ID}))
	var view rollout.View
	encoded, _ = json.Marshal(response.Result)
	if err := json.Unmarshal(encoded, &view); status != 200 || err != nil || view.Live == nil || view.Live.CurrentImage != "web:v2" || view.DrivenBy != "replica-1/1" || view.LeaseExpired {
		t.Fatalf("rollout read: HTTP %d %s", status, encoded)
	}
	if _, response = call(t, channel, envelope(channel, "invoke", OpRollout, Params{"namespace": "fixture", "name": "web"})); response.Error != nil {
		t.Fatalf("read by name: %+v", response)
	}
	_, response = call(t, channel, envelope(channel, "invoke", OpSetImage, Params{"namespace": "fixture", "name": "web", "uid": "old-uid", "container": "web", "image": "web:v3"}))
	if response.Error == nil || response.Error.Code != "rollout_"+rollout.CodeTargetChanged || len(*actions) != 2 || (*actions)[1].Result != SREResultTargetChanged {
		t.Fatalf("a replaced deployment: %+v, audited %+v", response.Error, *actions)
	}
}

// Without a driver both operations say why they are unavailable.
func TestImageChangesAreUnavailableWithoutADriver(t *testing.T) {
	for _, op := range SREOperations(SREOptions{RolloutReason: "rollout_unavailable: fixture"}) {
		if op.ID != OpSetImage && op.ID != OpRollout {
			continue
		}
		if availability := op.Availability(); availability.Available || availability.Reason != "rollout_unavailable: fixture" {
			t.Fatalf("%s availability %+v", op.ID, availability)
		}
	}
}

// An image change the apiserver refused the service account is counted as
// forbidden, as every other SRE write is.
func TestARefusedImageChangeIsForbidden(t *testing.T) {
	if result, code := sreResult(&rollout.Error{Code: rollout.CodeForbidden}); result != SREResultForbidden || code != rollout.CodeForbidden {
		t.Fatalf("forbidden reads as %s/%s", result, code)
	}
}
