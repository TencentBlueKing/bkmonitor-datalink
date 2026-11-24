package taskdispatch

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestRedisRepairHoldAndResumeIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit real Redis integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	sources := eventsource.New(emptySources{}, config.SeverityConfig{})
	namespace := "repair-" + uuid.NewString()
	controller, err := NewController(ctx, client, sources, namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Del(context.Background(), controller.key, controller.leader)
	state, release, now := fixture()
	Reconcile(&state, []eventsource.Release{release}, now)
	if err := controller.update(ctx, func(s *State) error { *s = state; return nil }); err != nil {
		t.Fatal(err)
	}
	id := taskID(release.ID, "lifecycle", 0)
	failed := state.Tasks[id]
	worker := state.Workers[failed.Worker]
	worker.Seq = 1
	if _, err := controller.Beat(ctx, Heartbeat{Worker: worker, Reports: []Report{{ID: id, Epoch: failed.Epoch, Phase: "stopping", Error: "task failed; inspect worker logs", Blocked: true}}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Tasks[id].Blocked || snapshot.Tasks[id].Phase != "stopping" {
		t.Fatal("repair report not persisted")
	}
	worker.Seq++
	if _, err := controller.Beat(ctx, Heartbeat{Worker: worker, Reports: []Report{{ID: id, Epoch: failed.Epoch, Phase: "stopped"}}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = controller.Snapshot(ctx)
	if err != nil || !snapshot.Tasks[id].Blocked || snapshot.Tasks[id].Phase != "stopped" {
		t.Fatal("stop acknowledgment cleared repair requirement", err)
	}
	// 模拟中心重启，恢复必须依赖持久状态，不能依赖原控制器内存。
	if err := client.Del(ctx, controller.leader).Err(); err != nil {
		t.Fatal(err)
	}
	controller, err = NewController(ctx, client, sources, namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.update(ctx, func(s *State) error {
		for key, task := range s.Tasks {
			if task.Role == "lifecycle" {
				task.Phase = "stopped"
				s.Tasks[key] = task
			}
		}
		for key, w := range s.Workers {
			w.Seen = now.Add(time.Minute)
			s.Workers[key] = w
		}
		Reconcile(s, []eventsource.Release{release}, now.Add(time.Minute))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = controller.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count(snapshot, "lifecycle") != 0 {
		t.Fatal("restarted coordinator retried permanent failure")
	}
	if err := controller.ResumeTask(ctx, release.ID, id, failed.Version, failed.Epoch+1); !errors.Is(err, eventsource.ErrConflict) {
		t.Fatal("stale resume accepted")
	}
	if err := controller.ResumeTask(ctx, release.ID, id, failed.Version, failed.Epoch); err != nil {
		t.Fatal(err)
	}
	if err := controller.update(ctx, func(s *State) error { Reconcile(s, []eventsource.Release{release}, now.Add(time.Minute)); return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, err = controller.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Tasks[id].Blocked || snapshot.Tasks[id].Epoch <= failed.Epoch || len(snapshot.Tasks[id].Retired) == 0 {
		t.Fatal("resume failed to authorize new PEL owner")
	}
}
