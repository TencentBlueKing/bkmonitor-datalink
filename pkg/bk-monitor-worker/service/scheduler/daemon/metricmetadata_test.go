package daemon

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func TestMetricMetadataDaemonTaskRegistration(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	queue := selectMetricMetadataQueue([]string{common.DefaultQueueName})
	for range 2 {
		if err := enqueueMetricMetadataTask(context.Background(), client, queue); err != nil {
			t.Fatal(err)
		}
	}
	items, err := client.SMembers(context.Background(), common.DaemonTaskKey()).Result()
	if err != nil || len(items) != 1 {
		t.Fatalf("daemon tasks = %d, error = %v; want one task", len(items), err)
	}
	var stored task.SerializerTask
	if err := jsonx.Unmarshal([]byte(items[0]), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Kind != MetricMetadataTaskKind || stored.Options.Queue != common.DefaultQueueName || string(stored.Payload) != "{}" {
		t.Fatalf("unexpected daemon task: %+v", stored)
	}
	operator, err := taskDefine[stored.Kind].initialFunc(context.Background())
	if err != nil || operator == nil {
		t.Fatalf("initialize task operator: %v", err)
	}
	if dimension := operator.GetTaskDimension(stored.Payload); dimension != "metricmetadata/kafka" {
		t.Fatalf("task dimension = %q", dimension)
	}
}

func TestSelectMetricMetadataQueue(t *testing.T) {
	tests := []struct {
		name   string
		queues []string
		want   string
	}{
		{name: "dedicated queue takes precedence", queues: []string{common.DefaultQueueName, MetricMetadataQueueName}, want: MetricMetadataQueueName},
		{name: "fallback to default", queues: []string{common.DefaultQueueName}, want: common.DefaultQueueName},
		{name: "skip when neither queue is configured", queues: []string{"other"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectMetricMetadataQueue(tt.queues); got != tt.want {
				t.Fatalf("selectMetricMetadataQueue(%v) = %q, want %q", tt.queues, got, tt.want)
			}
		})
	}
}
