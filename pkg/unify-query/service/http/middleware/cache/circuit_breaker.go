package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
)

// CircuitBreakerState 熔断器状态
type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

// String 返回状态的字符串表示
func (s CircuitBreakerState) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "Half-Open"
	default:
		return "Unknown"
	}
}

// CircuitBreaker 熔断器实现
type CircuitBreaker struct {
	name         string
	maxFailures  int           // 最大失败次数
	resetTimeout time.Duration // 重置超时时间
	metrics      *Metrics

	state        int32 // 使用 atomic 操作
	failures     int32
	lastFailTime int64 // Unix 纳秒时间戳
	mu           sync.RWMutex
}

// NewCircuitBreaker 创建新的熔断器
func NewCircuitBreaker(name string, maxFailures int, resetTimeout time.Duration, metrics *Metrics) *CircuitBreaker {
	return &CircuitBreaker{
		name:         name,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		metrics:      metrics,
		state:        int32(StateClosed),
	}
}

// Execute 执行操作，受熔断器保护
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	if !cb.allowRequest() {
		return fmt.Errorf("circuit breaker '%s' is open", cb.name)
	}

	err := fn()
	cb.recordResult(err == nil)
	return err
}

// allowRequest 检查是否允许请求通过
func (cb *CircuitBreaker) allowRequest() bool {
	state := CircuitBreakerState(atomic.LoadInt32(&cb.state))

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		// 检查是否可以转为半开状态
		return cb.attemptReset()
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// recordResult 记录执行结果
func (cb *CircuitBreaker) recordResult(success bool) {
	state := CircuitBreakerState(atomic.LoadInt32(&cb.state))

	if success {
		cb.onSuccess()
	} else {
		cb.onFailure()
	}

	// 更新监控指标
	cb.updateMetrics(state)
}

// onSuccess 处理成功情况
func (cb *CircuitBreaker) onSuccess() {
	atomic.StoreInt32(&cb.failures, 0)

	state := CircuitBreakerState(atomic.LoadInt32(&cb.state))
	if state == StateHalfOpen {
		// 半开状态下的成功，关闭熔断器
		atomic.StoreInt32(&cb.state, int32(StateClosed))
	}
}

// onFailure 处理失败情况
func (cb *CircuitBreaker) onFailure() {
	failures := atomic.AddInt32(&cb.failures, 1)
	atomic.StoreInt64(&cb.lastFailTime, time.Now().UnixNano())

	state := CircuitBreakerState(atomic.LoadInt32(&cb.state))
	if state == StateClosed && failures >= int32(cb.maxFailures) {
		// 失败次数超过阈值，打开熔断器
		atomic.StoreInt32(&cb.state, int32(StateOpen))
	} else if state == StateHalfOpen {
		// 半开状态下的失败，重新打开熔断器
		atomic.StoreInt32(&cb.state, int32(StateOpen))
	}
}

// attemptReset 尝试重置熔断器
func (cb *CircuitBreaker) attemptReset() bool {
	lastFailTime := atomic.LoadInt64(&cb.lastFailTime)
	if time.Since(time.Unix(0, lastFailTime)) >= cb.resetTimeout {
		// 尝试转换为半开状态
		if atomic.CompareAndSwapInt32(&cb.state, int32(StateOpen), int32(StateHalfOpen)) {
			atomic.StoreInt32(&cb.failures, 0)
			return true
		}
	}
	return false
}

// updateMetrics 更新监控指标
func (cb *CircuitBreaker) updateMetrics(oldState CircuitBreakerState) {
	if cb.metrics == nil {
		return
	}

	newState := CircuitBreakerState(atomic.LoadInt32(&cb.state))
	if newState != oldState {
		cb.metrics.circuitBreakerStatus.WithLabelValues(cb.name).Set(float64(newState))
	}
}

// GetState 获取当前状态
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	return CircuitBreakerState(atomic.LoadInt32(&cb.state))
}

// Reset 手动重置熔断器
func (cb *CircuitBreaker) Reset() {
	atomic.StoreInt32(&cb.state, int32(StateClosed))
	atomic.StoreInt32(&cb.failures, 0)
	atomic.StoreInt64(&cb.lastFailTime, 0)
}

// FlowController 流控器
type FlowController struct {
	maxInflight int32 // 最大熔断器释放的并发数
	current     int32 // 当前并发数
	metrics     *Metrics
}

// NewFlowController 创建流控器
func NewFlowController(maxInflight int, metrics *Metrics) *FlowController {
	return &FlowController{
		maxInflight: int32(maxInflight),
		metrics:     metrics,
	}
}

// Acquire 获取执行许可
func (fc *FlowController) Acquire() error {
	current := atomic.AddInt32(&fc.current, 1)
	if current > fc.maxInflight {
		atomic.AddInt32(&fc.current, -1)
		return fmt.Errorf("flow controller: max inflight limit %d exceeded", fc.maxInflight)
	}

	// 更新 run 监控
	if fc.metrics != nil {
		fc.metrics.updateSidecarWatchCount(float64(current))
	}

	return nil
}

// Release 释放执行许可
func (fc *FlowController) Release() {
	current := atomic.AddInt32(&fc.current, -1)
	if current < 0 {
		atomic.StoreInt32(&fc.current, 0)
	}

	// 更新 run 监控
	if fc.metrics != nil {
		fc.metrics.updateSidecarWatchCount(float64(current))
	}
}

// GetCurrent 获取当前并发数
func (fc *FlowController) GetCurrent() int32 {
	return atomic.LoadInt32(&fc.current)
}

// ResilienceManager 容灾管理器
type ResilienceManager struct {
	redisCircuitBreaker   *CircuitBreaker
	flowController        *FlowController
	metrics               *Metrics
	circuitBreakerEnabled bool
}

// NewResilienceManager 创建容灾管理器
func NewResilienceManager(maxInflight int, maxFailures int, resetTimeout time.Duration, metrics *Metrics) *ResilienceManager {
	enabled := viper.GetBool(http.QueryCacheCircuitBreakerEnabledConfigPath)

	return &ResilienceManager{
		redisCircuitBreaker:   NewCircuitBreaker("redis", maxFailures, resetTimeout, metrics),
		flowController:        NewFlowController(maxInflight, metrics),
		metrics:               metrics,
		circuitBreakerEnabled: enabled,
	}
}

// ExecuteWithProtection 带容灾保护的执行
func (rm *ResilienceManager) ExecuteWithProtection(ctx context.Context, fn func() error) error {
	if !rm.circuitBreakerEnabled {
		return fn()
	}

	if err := rm.flowController.Acquire(); err != nil {
		rm.metrics.recordCacheError("flow_control", "max_inflight")
		return err
	}
	defer rm.flowController.Release()

	return rm.redisCircuitBreaker.Execute(ctx, fn)
}

// GetRedisCircuitBreakerState 获取 Redis 熔断器状态
func (rm *ResilienceManager) GetRedisCircuitBreakerState() CircuitBreakerState {
	return rm.redisCircuitBreaker.GetState()
}

// IsRedisAvailable 检查 Redis 是否可用
func (rm *ResilienceManager) IsRedisAvailable() bool {
	if !rm.circuitBreakerEnabled {
		return true
	}
	return rm.redisCircuitBreaker.GetState() != StateOpen
}
