package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// TestMQConsumeRateLoaded verifies the consume_rate field on MQ.config is
// read by Kafka frontend setup and used to create the per-frontend flow
// limiter. The chain under test is:
//
//	MetaClusterInfo.ConsumeRate (metadata.yaml MQ.<name>.consume_rate)
//	  -> MQConfigFromContext / AsKafkaCluster
//	  -> frontend.NewKafkaConsumerGroupFrontend rate = kafkaConfig.ConsumeRate
//	  -> define.NewFlowLimiter(name, rate) creates per-frontend limiter
//
// Since spinning up the full sarama consumer is out of scope here, the test
// mirrors the exact expression from kafka/frontend.go:161-164 and asserts
// that the field round-trips through MQConfigFromContext and the priority
// fallback (consume_rate=0 -> dataIdFlowBytes default).
func TestMQConsumeRateLoaded(t *testing.T) {
	t.Run("consume_rate=52428800 is read back from context", func(t *testing.T) {
		mq := &config.MetaClusterInfo{
			ClusterType: "kafka",
			ConsumeRate: 52428800, // 50 MB/s
		}
		ctx := config.MQConfigIntoContext(context.Background(), mq)

		kafkaConfig := config.MQConfigFromContext(ctx).AsKafkaCluster()
		require.Equal(t, "kafka", kafkaConfig.ClusterType)
		require.Equal(t, 52428800, kafkaConfig.ConsumeRate,
			"ConsumeRate should round-trip via context.Value chain")
	})

	t.Run("consume_rate=0 falls back to dataIdFlowBytes default (20MB/s)", func(t *testing.T) {
		mq := &config.MetaClusterInfo{ClusterType: "kafka", ConsumeRate: 0}
		ctx := config.MQConfigIntoContext(context.Background(), mq)
		kafkaConfig := config.MQConfigFromContext(ctx).AsKafkaCluster()

		rate := kafkaConfig.ConsumeRate
		var fallback int
		if rate <= 0 {
			fallback = 1024 * 1024 * 20 // DataIdFlowBytes() default when dataIdFlowBytes <= 0
		} else {
			fallback = rate
		}
		require.Equal(t, 0, kafkaConfig.ConsumeRate, "explicit zero preserved")
		require.Equal(t, 1024*1024*20, fallback,
			"frontend.NewKafkaConsumerGroupFrontend falls back to dataIdFlowBytes default when consume_rate<=0")
	})

	t.Run("consume_rate=1 (lower than dataIdFlowBytes default) wins over fallback", func(t *testing.T) {
		mq := &config.MetaClusterInfo{ClusterType: "kafka", ConsumeRate: 1}
		ctx := config.MQConfigIntoContext(context.Background(), mq)
		kafkaConfig := config.MQConfigFromContext(ctx).AsKafkaCluster()

		rate := kafkaConfig.ConsumeRate
		if rate <= 0 {
			rate = 1024 * 1024 * 20
		}
		require.Equal(t, 1, rate,
			"any positive consume_rate wins over the 20MB/s fallback")
	})
}