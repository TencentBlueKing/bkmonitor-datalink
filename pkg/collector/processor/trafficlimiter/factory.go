// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trafficlimiter

import (
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	resultAccepted = "accepted"
	resultRejected = "rejected"

	backendRedis = "redis"
	backendLocal = "local"

	reasonNone     = "none"
	reasonNoClient = "no_client"
	reasonTimeout  = "timeout"
	reasonClosed   = "closed"
	reasonError    = "error"

	modeDisabled  = "disabled"
	modeLocalOnly = "local_only"
	modeShared    = "shared"
)

var limiterModes = [...]string{modeDisabled, modeLocalOnly, modeShared}

var errNoRedisClient = errors.New("traffic limiter has no redis client")

var trafficLimiterBytesTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "traffic_limiter_bytes_total",
		Help:      "Traffic limiter logical bytes total",
	},
	[]string{"token", "service_name", "record_type", "result"},
)

var (
	metricRecordTypes = [...]define.RecordType{define.RecordTraces, define.RecordMetrics, define.RecordLogs}
	metricResults     = [...]string{resultAccepted, resultRejected}
)

// trafficLimiterDecisionsTotal 回答「额度判断走的是共享 Redis 还是本地降级」。
// 降级是逐次判断发生的事件而不是进程级开关：连接池打满时完全可能一部分服务走通 Redis、
// 一部分退本地，用 0/1 表达不了这个比例；而且 Prometheus 按间隔抓取，短于抓取间隔的
// 降级窗口在 Gauge 上会整段丢失。Counter 单调累加，任何一次降级都会体现在差值里。
var trafficLimiterDecisionsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "traffic_limiter_decisions_total",
		Help:      "Traffic limiter quota decisions by backend and fallback reason",
	},
	[]string{"backend", "reason"},
)

// trafficLimiterMode 是配置态而非运行态，只在 Reload 时变化，不会抖动。
// mode="shared" 只表示配置合法，不保证运行时 Redis 健康——运行时的事由上面那个 Counter 回答。
var trafficLimiterMode = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "traffic_limiter_mode",
		Help:      "Traffic limiter configured mode, exactly one label is set to 1",
	},
	[]string{"mode"},
)

// 标签集固定，装载时预解析成 child。判断发生在每请求每服务的热路径上，
// 不应反复付 WithLabelValues 的标签哈希与 map 查找开销。
var (
	decisionRedis         = trafficLimiterDecisionsTotal.WithLabelValues(backendRedis, reasonNone)
	decisionLocalNoClient = trafficLimiterDecisionsTotal.WithLabelValues(backendLocal, reasonNoClient)
	decisionLocalTimeout  = trafficLimiterDecisionsTotal.WithLabelValues(backendLocal, reasonTimeout)
	decisionLocalClosed   = trafficLimiterDecisionsTotal.WithLabelValues(backendLocal, reasonClosed)
	decisionLocalError    = trafficLimiterDecisionsTotal.WithLabelValues(backendLocal, reasonError)
)

// setLimiterMode 让三个 mode 中恰好一个为 1，其余为 0，便于直接用 == 1 做告警。
func setLimiterMode(enabled, redisReady bool) {
	current := modeDisabled
	switch {
	case !enabled:
		current = modeDisabled
	case redisReady:
		current = modeShared
	default:
		current = modeLocalOnly
	}
	for _, mode := range limiterModes {
		value := float64(0)
		if mode == current {
			value = 1
		}
		trafficLimiterMode.WithLabelValues(mode).Set(value)
	}
}

// Weights 是各信号类型的计费单价：计费字节 = 逻辑字节 × 权重。
// 权重大于 1 表示同样的字节数消耗更多额度，等价于限得更严。
//
// 它必须是可比较类型（不能用 map），因为本地降级用 Config 的相等性判断策略是否变化。
type Weights struct {
	Traces  float64 `config:"traces" mapstructure:"traces"`
	Metrics float64 `config:"metrics" mapstructure:"metrics"`
	Logs    float64 `config:"logs" mapstructure:"logs"`
}

func (w Weights) of(recordType define.RecordType) float64 {
	switch recordType {
	case define.RecordTraces:
		return w.Traces
	case define.RecordMetrics:
		return w.Metrics
	case define.RecordLogs:
		return w.Logs
	}
	return 1
}

// normalized 把未设置的权重补成 1。
//
// 解码之后无法区分「没填」和「填了 0」，因此 0 一律按未设置处理而不是「免费」：
// 服务级覆盖不做字段级合并，一份没写 weights 的覆盖配置若被当成免费，
// 会让该服务的限流直接失效。要豁免某个信号应当使用 bytes_per_second: 0。
func (w Weights) normalized() Weights {
	if w.Traces <= 0 {
		w.Traces = 1
	}
	if w.Metrics <= 0 {
		w.Metrics = 1
	}
	if w.Logs <= 0 {
		w.Logs = 1
	}
	return w
}

func (w Weights) Validate() error {
	for name, weight := range map[string]float64{
		"traces": w.Traces, "metrics": w.Metrics, "logs": w.Logs,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return errors.Errorf("weight %s must be a non-negative finite number: %v", name, weight)
		}
	}
	return nil
}

type Config struct {
	BytesPerSecond int64   `config:"bytes_per_second" mapstructure:"bytes_per_second"`
	BurstBytes     int64   `config:"burst_bytes" mapstructure:"burst_bytes"`
	Weights        Weights `config:"weights" mapstructure:"weights"`
}

func (c Config) Validate() error {
	if c.BytesPerSecond < 0 {
		return errors.Errorf("bytes_per_second must not be negative: %d", c.BytesPerSecond)
	}
	if c.BurstBytes < 0 {
		return errors.Errorf("burst_bytes must not be negative: %d", c.BurstBytes)
	}
	if c.BytesPerSecond > 0 && c.BurstBytes == 0 {
		return errors.New("burst_bytes must be positive when bytes_per_second is positive")
	}
	return c.Weights.Validate()
}

// chargedBytes 把逻辑字节折算成计费字节。向上取整并保证非空请求至少计 1 字节，
// 否则一个极小的权重会把所有请求算成 0，等于不限流。
func (c Config) chargedBytes(size int, recordType define.RecordType) int64 {
	weight := c.Weights.of(recordType)
	if weight == 1 {
		return int64(size)
	}

	charged := math.Ceil(float64(size) * weight)
	if charged >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	cost := int64(charged)
	if size > 0 && cost < 1 {
		cost = 1
	}
	return cost
}

func (c Config) limited() bool {
	return c.BytesPerSecond > 0
}

// limitIndex 回答「某个 Token 下是否存在任何启用了额度的服务」。
// 未启用额度的 Token 在 Process 入口直接返回，因此灰度期间既不计算逻辑字节也不访问 Redis，
// 开销只落在已经开启限流的应用上。
type limitIndex struct {
	global bool
	tokens map[string]bool
}

func (i *limitIndex) limited(token string) bool {
	if limited, ok := i.tokens[token]; ok {
		return limited
	}
	return i.global
}

func (i *limitIndex) anyLimited() bool {
	if i.global {
		return true
	}
	for _, limited := range i.tokens {
		if limited {
			return true
		}
	}
	return false
}

// newLimitIndex 把「额度是否启用」的判断在配置装载时折叠成一次 Map 查找。
// 判定顺序与 TierConfig 的查找顺序一致：服务或实例级覆盖优先，其次是 Token 默认配置，
// 最后才回落到主配置。Token 显式声明了为 0 的默认额度时可以短路，即使主配置开启了限流。
func newLimitIndex(global Config, decoded []tokenConfig) *limitIndex {
	type tokenState struct {
		hasDefault     bool
		defaultLimited bool
		scopedLimited  bool
	}

	states := make(map[string]*tokenState, len(decoded))
	for _, item := range decoded {
		state, ok := states[item.token]
		if !ok {
			state = &tokenState{}
			states[item.token] = state
		}
		if item.typ == define.SubConfigFieldDefault {
			state.hasDefault = true
			state.defaultLimited = item.config.limited()
			continue
		}
		state.scopedLimited = state.scopedLimited || item.config.limited()
	}

	// 只记录能确定答案的 Token，其余的由 limited 回落到主配置。
	index := &limitIndex{global: global.limited(), tokens: make(map[string]bool, len(states))}
	for token, state := range states {
		switch {
		case state.scopedLimited:
			index.tokens[token] = true
		case state.hasDefault:
			index.tokens[token] = state.defaultLimited
		}
	}
	return index
}

func init() {
	processor.Register(define.ProcessorTrafficLimiter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized, time.Now)
}

func newFactory(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
	now func() time.Time,
) (*trafficLimiter, error) {
	return newFactoryWithRedisFactory(conf, customized, now, newRedisGCRA)
}

type redisGCRAFactory func(RedisConfig) distributedGCRA

func newFactoryWithRedisFactory(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
	now func() time.Time,
	newRedis redisGCRAFactory,
) (*trafficLimiter, error) {
	loaded, err := newTierConfigs(conf, customized)
	if err != nil {
		// 主配置结构损坏时空转，绝不返回错误——那会让整条 Pipeline 建不起来。
		logger.Errorf("failed to load traffic limiter config, traffic limiting is disabled: %v", err)
		loaded = disabledConfigs()
	}
	if now == nil {
		now = time.Now
	}

	limiter := &trafficLimiter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		redisConfig:     loaded.redisConfig,
		newRedis:        newRedis,
		fallback:        newLocalGCRAManager(now),
		activity:        newServiceActivityTracker(now, deleteTrafficMetrics),
		sizer:           newServiceSizer(),
		now:             now,
	}
	view := &limiterView{
		configs:    loaded.configs,
		index:      loaded.index,
		enabled:    loaded.enabled,
		redisReady: loaded.redisReady,
	}
	if view.redisReady {
		view.redis = newRedis(loaded.redisConfig)
	}
	limiter.view.Store(view)
	setLimiterMode(view.enabled, view.redisReady)
	return limiter, nil
}

// tokenConfig 保存一条已解码的子配置，供额度索引复用，避免二次解码。
type tokenConfig struct {
	token  string
	typ    string
	config Config
}

// loadedConfigs 是一次配置装载的结果。
//
// 装载只在主配置结构本身无法解码时才返回错误，其余问题一律降级：Pipeline 中任何一个
// Processor 构建失败都会让整条流水线构建失败（parsePipelines 的 len(instances) 校验），
// 该信号的全部请求随后被 validatePreCheckProcessors 判为 400。限流是保护性功能，
// 它的配置问题绝不能升级成数据通路中断。
type loadedConfigs struct {
	configs     *confengine.TierConfig // type: Config
	index       *limitIndex
	redisConfig RedisConfig

	// enabled 表示主配置里出现了 redis 段，额度需要执行；
	// redisReady 进一步表示该段合法、可以建立客户端。两者都为真才是跨实例共享额度，
	// enabled 为真而 redisReady 为假时退化成每实例本地 GCRA。
	enabled    bool
	redisReady bool
}

// disabledConfigs 用于主配置无法解码时兜底，让处理器空转而不是让 Pipeline 建不起来。
func disabledConfigs() *loadedConfigs {
	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{})
	return &loadedConfigs{configs: configs, index: &limitIndex{}}
}

func newTierConfigs(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
) (*loadedConfigs, error) {
	global, redisConfig, err := decodeMainConfig(conf)
	if err != nil {
		return nil, err
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(global)

	decoded := make([]tokenConfig, 0, len(customized))
	for _, custom := range customized {
		config, err := decodeConfig(custom.Config.Config)
		if err != nil {
			logger.Errorf(
				"failed to decode traffic limiter config, token=%s, type=%s, id=%s, error=%v",
				custom.Token, custom.Type, custom.ID, err,
			)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, config)
		decoded = append(decoded, tokenConfig{token: custom.Token, typ: custom.Type, config: config})
	}

	loaded := &loadedConfigs{
		configs:     configs,
		index:       newLimitIndex(global, decoded),
		redisConfig: redisConfig,
		enabled:     !redisConfig.empty(),
	}
	switch {
	case !loaded.enabled:
		if loaded.index.anyLimited() {
			logger.Warnf("traffic limiter redis is not configured, all configured quotas are ignored")
		}
	default:
		// 配置非法等同于 Redis 不可用：保留额度语义，退化成每实例本地 GCRA，
		// 而不是让 Processor 构建失败连累整条 Pipeline。
		if err := redisConfig.Validate(); err != nil {
			logger.Errorf(
				"invalid traffic limiter redis config, degraded to per-instance local GCRA: %v", err,
			)
		} else {
			loaded.redisReady = true
		}
	}
	return loaded, nil
}

type mainConfig struct {
	BytesPerSecond int64       `config:"bytes_per_second" mapstructure:"bytes_per_second"`
	BurstBytes     int64       `config:"burst_bytes" mapstructure:"burst_bytes"`
	Weights        Weights     `config:"weights" mapstructure:"weights"`
	Redis          RedisConfig `config:"redis" mapstructure:"redis"`
}

func decodeMainConfig(conf map[string]any) (Config, RedisConfig, error) {
	var main mainConfig
	if err := mapstructure.Decode(conf, &main); err != nil {
		return Config{}, RedisConfig{}, err
	}
	config := Config{
		BytesPerSecond: main.BytesPerSecond,
		BurstBytes:     main.BurstBytes,
		Weights:        main.Weights,
	}
	if err := config.Validate(); err != nil {
		// 非法额度按「不限流」处理，同样不能让 Processor 构建失败。
		logger.Errorf("invalid traffic limiter global quota, it is ignored: %v", err)
		config = Config{}
	}
	config.Weights = config.Weights.normalized()
	return config, main.Redis.normalized(), nil
}

func decodeConfig(conf map[string]any) (Config, error) {
	var config Config
	if err := mapstructure.Decode(conf, &config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	config.Weights = config.Weights.normalized()
	return config, nil
}

type trafficLimiter struct {
	processor.CommonProcessor

	// mu 只串行化 Reload、Clean 和客户端重建这些写者，请求路径不参与竞争。
	mu          sync.Mutex
	view        atomic.Pointer[limiterView]
	redisConfig RedisConfig
	newRedis    redisGCRAFactory
	fallback    *localGCRAManager
	activity    *serviceActivityTracker
	sizer       *serviceSizer
	now         func() time.Time
}

// limiterView 是一次请求使用的配置代与 Redis 客户端快照，整体替换而不原地修改。
// 请求路径只读一次指针，随后的计量和 Redis 往返都在快照上进行，不持锁做网络 I/O：
// 否则 Reload 的写者要等全部在途 Redis 调用返回，而排在写者后面的新请求也一并停住，
// 一次 Redis 抖动就会被这条常规的周期性操作放大成所有 Receiver goroutine 的停顿。
//
// 代价是取快照之后发生的 Reload 对本次请求不可见（Reload 本来就不是原子切换点），
// 且快照到的客户端可能已被 Reload 关闭，那次判断会退化成本地 GCRA，好过阻塞所有请求。
type limiterView struct {
	configs *confengine.TierConfig // type: Config
	index   *limitIndex
	redis   distributedGCRA

	// enabled 为假表示主配置没有 redis 段，整个处理器空转。
	// redisReady 区分「配置合法、客户端只是被 Clean 掉了」和「配置本身非法」：
	// 前者必须由 ensureRedis 自愈，后者不能反复拿一份非法配置去建客户端，直接走本地降级。
	enabled    bool
	redisReady bool
}

func (p *trafficLimiter) Name() string {
	return define.ProcessorTrafficLimiter
}

func (p *trafficLimiter) IsDerived() bool {
	return false
}

func (p *trafficLimiter) IsPreCheck() bool {
	return true
}

func (p *trafficLimiter) snapshot() *limiterView {
	return p.view.Load()
}

// ensureRedis 在客户端缺失时按保留下来的 Redis 配置重建，只有确实要访问 Redis 的请求才会走到这里。
// Pipeline Manager 的 Reload 会先 Clean 全部新建的 Processor，再把上一代配置中不存在的那些
// 直接装配进来且不再调用它们的 Reload，框架因此隐含要求 Clean 之后 Process 仍然可用。
// 新增的限流器拿到的正是这样一个已经关闭客户端的实例，它必须能自愈，
// 否则该 Token 的全部 OTLP 流量都会被判为超限。
func (p *trafficLimiter) ensureRedis() distributedGCRA {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.view.Load()
	if current.redis != nil || !current.redisReady {
		return current.redis
	}
	redis := p.newRedis(p.redisConfig)
	p.view.Store(&limiterView{
		configs: current.configs, index: current.index, redis: redis,
		enabled: current.enabled, redisReady: true,
	})
	return redis
}

// Reload 在服务配置变化时只替换额度索引；Redis 连接参数变化时才替换客户端。
// GCRA 的 Redis Key 包含额度摘要，因此服务额度变化后会自然使用一个新的满桶，
// 不需要扫描或迁移旧配置对应的状态。
func (p *trafficLimiter) Reload(conf map[string]any, customized []processor.SubConfigProcessor) {
	loaded, err := newTierConfigs(conf, customized)
	if err != nil {
		logger.Errorf("failed to reload traffic limiter, keep the previous configuration: %v", err)
		return
	}

	p.mu.Lock()
	redis := p.view.Load().redis
	var oldRedis distributedGCRA
	switch {
	case !loaded.redisReady:
		// Redis 段被移除或变得非法，释放客户端；额度若仍启用则由本地 GCRA 承担。
		oldRedis, redis = redis, nil
		p.fallback.Clean()
	case redis == nil || !reflect.DeepEqual(p.redisConfig, loaded.redisConfig):
		oldRedis = redis
		redis = p.newRedis(loaded.redisConfig)
		p.fallback.Clean()
	}
	p.redisConfig = loaded.redisConfig
	p.CommonProcessor = processor.NewCommonProcessor(conf, customized)
	p.view.Store(&limiterView{
		configs: loaded.configs, index: loaded.index, redis: redis,
		enabled: loaded.enabled, redisReady: loaded.redisReady,
	})
	setLimiterMode(loaded.enabled, loaded.redisReady)
	p.mu.Unlock()

	if oldRedis != nil {
		_ = oldRedis.Close()
	}
}

// Clean 释放 Redis 连接池，避免被丢弃的实例泄漏连接和后台 goroutine：
// Manager.Reload 每次都会完整构造一套新 Processor，不 Close 就是每次热加载泄漏一个连接池和一个 goroutine。
// 但客户端置空只表示「当前没有可用客户端」，不代表限流器永久失效，ensureRedis 会在下次请求时重建。
func (p *trafficLimiter) Clean() {
	p.mu.Lock()
	current := p.view.Load()
	p.view.Store(&limiterView{
		configs: current.configs, index: current.index,
		enabled: current.enabled, redisReady: current.redisReady,
	})
	p.fallback.Clean()
	p.activity.Clean()
	p.mu.Unlock()

	if current.redis != nil {
		_ = current.redis.Close()
	}
}

// Process 先计算服务逻辑字节数并解析各服务配置，再分别判断各服务的流量额度。
// 部分服务超限时只移除对应 Resource Group，其余服务继续进入 Pipeline；全部超限才拒绝请求。
//
// 未配置 Redis 或未配置任何额度的 Token 在计算逻辑字节之前就返回，因此不承担 Sizer 遍历开销，
// 代价是这些 Token 不产生流量指标；灰度阶段的观测数据只覆盖已开启限流的应用。
func (p *trafficLimiter) Process(record *define.Record) (*define.Record, error) {
	view := p.snapshot()
	if !view.enabled {
		return nil, nil
	}
	token := record.Token.Original
	if !view.index.limited(token) {
		return nil, nil
	} // 未开限流的 Token 直接短路：不算逻辑字节、不碰 Redis

	sizes, err := p.sizer.Sizes(record)
	if err != nil {
		return nil, err
	}
	if len(sizes) == 0 {
		return nil, nil
	}

	redis := view.redis
	if redis == nil && view.redisReady {
		redis = p.ensureRedis()
	}
	decision, err := p.limit(view.configs, redis, token, record.RecordType, sizes)
	if err != nil {
		return nil, err
	}

	rejected := make(map[string]struct{}, len(decision.rejected))
	for _, service := range decision.rejected {
		rejected[service] = struct{}{}
	}
	observeTrafficBytes(token, record.RecordType, sizes, rejected)
	if len(rejected) == 0 {
		return nil, nil
	}

	if decision.accepted {
		remaining, err := p.sizer.DropRejected(record, rejected)
		if err != nil {
			return nil, err
		}
		if remaining {
			return nil, nil
		}
	}

	return nil, errors.Errorf(
		"traffic limiter rejected the request, token [%s], record_type [%s], services [%s]",
		token, record.RecordType.S(), strings.Join(decision.rejected, ","),
	)
}

// limit 使用一份配置快照判断各服务的额度，Redis 往返不持有 p.mu。
func (p *trafficLimiter) limit(
	configs *confengine.TierConfig,
	redis distributedGCRA,
	token string,
	recordType define.RecordType,
	sizes map[string]int,
) (limitDecision, error) {
	now := p.now()
	accepted := false
	rejected := make([]string, 0)
	for service, size := range sizes {
		config := configs.Get(token, service, "")
		if config == nil {
			return limitDecision{}, errors.Errorf(
				"traffic limiter config not found, token [%s], service [%s]", token, service,
			)
		}
		p.activity.Touch(token, service, now)
		if size <= 0 {
			accepted = true
			continue
		}

		serviceConfig := config.(Config)
		if !serviceConfig.limited() {
			p.fallback.Delete(token, service)
			accepted = true
			continue
		}

		// 没有可用客户端时按 Redis 故障处理。限流器自身状态异常不应该放大成整个 Token 的流量损失：
		// Redis 运行时故障都已经降级到本地 GCRA，客户端缺失更没有理由 fail-closed。
		// 额度按计费字节扣减：不同信号的单位价值不同，用权重折算后再进同一个桶。
		// 指标仍然记录原始逻辑字节，两者的换算关系由权重配置给出。
		cost := serviceConfig.chargedBytes(size, recordType)
		allowed, redisErr := false, error(errNoRedisClient)
		if redis != nil {
			allowed, redisErr = redis.Allow(token, service, cost, serviceConfig)
		}
		if redisErr != nil {
			warnRedisFailure(redisErr)
			fallbackDecision(redisErr).Inc()
			allowed = p.fallback.Allow(token, service, cost, serviceConfig, now)
		} else {
			decisionRedis.Inc()
			// Redis 一旦恢复成功判断，就丢弃该服务的本地降级状态；
			// 下一次独立故障会重新从满桶开始。
			p.fallback.Delete(token, service)
		}
		if allowed {
			accepted = true
			continue
		}
		rejected = append(rejected, service)
	}
	sort.Strings(rejected)
	return limitDecision{accepted: accepted, rejected: rejected}, nil
}

func observeTrafficBytes(
	token string,
	recordType define.RecordType,
	sizes map[string]int,
	rejected map[string]struct{},
) {
	for service, size := range sizes {
		if size <= 0 {
			continue
		}
		result := resultAccepted
		if _, ok := rejected[service]; ok {
			result = resultRejected
		}
		trafficLimiterBytesTotal.WithLabelValues(token, service, recordType.S(), result).Add(float64(size))
	}
}

func deleteTrafficMetrics(token, service string) {
	for _, recordType := range metricRecordTypes {
		for _, result := range metricResults {
			trafficLimiterBytesTotal.DeleteLabelValues(token, service, recordType.S(), result)
		}
	}
}
