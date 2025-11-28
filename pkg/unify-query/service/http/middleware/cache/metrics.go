package cache

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics 监控指标集合
type Metrics struct {
	// 业务指标
	cacheRequestsTotal     *prometheus.CounterVec
	singleflightDedupTotal *prometheus.CounterVec
	dbRequestsTotal        *prometheus.CounterVec

	// 延迟指标
	cacheDurationSeconds *prometheus.HistogramVec

	// 资源和容量指标
	payloadSizeBytes   *prometheus.HistogramVec
	sidecarWatchCount  prometheus.Gauge
	sidecarQueueLength prometheus.Gauge
	redisPoolConns     *prometheus.GaugeVec

	// 错误和熔断指标
	cacheErrorsTotal     *prometheus.CounterVec
	circuitBreakerStatus *prometheus.GaugeVec
	singleflightTimeouts prometheus.Counter
}

var (
	// 初始化监控指标
	cacheRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unify_cache_requests_total",
			Help: "Total number of cache requests by layer and status",
		},
		[]string{"layer", "status"},
	)

	singleflightDedupTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unify_sf_dedup_total",
			Help: "Total number of requests deduplicated by singleflight",
		},
		[]string{"type"},
	)

	dbRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unify_db_requests_total",
			Help: "Total number of requests that reach the database",
		},
		[]string{"source"},
	)

	cacheDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "unify_cache_duration_seconds",
			Help:    "Time spent on cache operations by step",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"step"},
	)

	payloadSizeBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "unify_cache_payload_size_bytes",
			Help:    "Size of cached payloads in bytes",
			Buckets: []float64{1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216},
		},
		[]string{"key_prefix"},
	)

	sidecarWatchCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "unify_sidecar_watch_count",
			Help: "Current number of keys being watched by run",
		},
	)

	sidecarQueueLength = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "unify_sidecar_queue_len",
			Help: "Current length of run subscription queue",
		},
	)

	redisPoolConns = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unify_redis_pool_conns",
			Help: "Number of Redis connections by state",
		},
		[]string{"state"},
	)

	cacheErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unify_cache_errors_total",
			Help: "Total number of cache errors by operation and reason",
		},
		[]string{"op", "reason"},
	)

	circuitBreakerStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unify_circuit_breaker_status",
			Help: "Circuit breaker status (0=Closed, 1=Open, 2=Half-Open)",
		},
		[]string{"name"},
	)

	singleflightTimeouts = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "unify_sf_timeout_total",
			Help: "Total number of singleflight timeouts",
		},
	)
)

// NewMetrics 创建监控指标实例
func NewMetrics() *Metrics {
	return &Metrics{
		cacheRequestsTotal:     cacheRequestsTotal,
		singleflightDedupTotal: singleflightDedupTotal,
		dbRequestsTotal:        dbRequestsTotal,
		cacheDurationSeconds:   cacheDurationSeconds,
		payloadSizeBytes:       payloadSizeBytes,
		sidecarWatchCount:      sidecarWatchCount,
		sidecarQueueLength:     sidecarQueueLength,
		redisPoolConns:         redisPoolConns,
		cacheErrorsTotal:       cacheErrorsTotal,
		circuitBreakerStatus:   circuitBreakerStatus,
		singleflightTimeouts:   singleflightTimeouts,
	}
}

// 记录缓存请求
func (m *Metrics) recordCacheRequest(layer, status string) {
	m.cacheRequestsTotal.WithLabelValues(layer, status).Inc()
}

// 记录 singleflight 去重
func (m *Metrics) recordSingleflightDedup(dedupType string) {
	m.singleflightDedupTotal.WithLabelValues(dedupType).Inc()
}

// 记录数据库请求
func (m *Metrics) recordDBRequest(source string) {
	m.dbRequestsTotal.WithLabelValues(source).Inc()
}

// 记录缓存操作延迟
func (m *Metrics) recordCacheDuration(step string, duration time.Duration) {
	m.cacheDurationSeconds.WithLabelValues(step).Observe(duration.Seconds())
}

// 记录负载大小
func (m *Metrics) recordPayloadSize(keyPrefix string, size float64) {
	m.payloadSizeBytes.WithLabelValues(keyPrefix).Observe(size)
}

// 记录缓存错误
func (m *Metrics) recordCacheError(op, reason string) {
	m.cacheErrorsTotal.WithLabelValues(op, reason).Inc()
}

// 更新 run 监控计数
func (m *Metrics) updateSidecarWatchCount(count float64) {
	m.sidecarWatchCount.Set(count)
}

// 更新 run 队列长度
func (m *Metrics) updateSidecarQueueLength(length float64) {
	m.sidecarQueueLength.Set(length)
}

// 记录 singleflight 超时
func (m *Metrics) recordSingleflightTimeout() {
	m.singleflightTimeouts.Inc()
}
