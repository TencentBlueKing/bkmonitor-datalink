package reflectx

import (
	"encoding/json"
	"log"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
)

// 模拟 MetricOptions 结构（避免循环导入）
type MetricOptions struct {
	enabledProfile bool
	profileAddress string
	profileToken   string
	profileAppIdx  string
	reportInterval time.Duration
}

// 辅助函数来访问不可导出的字段
func getFieldValue(v interface{}, fieldName string) interface{} {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	// 如果当前值不可寻址，复制到一个可寻址的临时变量
	if !val.CanAddr() {
		tmp := reflect.New(val.Type()).Elem()
		tmp.Set(val)
		val = tmp
	}
	field := val.FieldByName(fieldName)
	if !field.IsValid() {
		return nil
	}

	// 检查字段是否可导出
	if field.CanInterface() {
		return field.Interface()
	}

	// 对于不可导出的字段，使用 unsafe 访问
	fieldPtr := unsafe.Pointer(field.UnsafeAddr())
	fieldValue := reflect.NewAt(field.Type(), fieldPtr).Elem()
	return fieldValue.Interface()
}

// 直接 JSON 反序列化为 map[string]interface{}
func jsonToMap(t *testing.T, s string) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("json decode error: %v", err)
	}
	return m
}

func TestSimpleBuilderCopyFromMap(t *testing.T) {
	t.Run("测试 window.ProcessorOptions 映射", func(t *testing.T) {
		// 模拟 builder.go 中的逻辑
		j := `{
    	  "enabled_info_cache": true,
    	  "trace_es_query_rate": 50,
    	  "metric_report_enabled": true,
    	  "info_report_enabled": true,
    	  "metric_layer4_report_enabled": false
    	}`
		processorOptions := jsonToMap(t, j)

		options := &window.ProcessorOptions{}
		CopyFromMap(options, processorOptions)
		log.Printf("options: %+v", options)
		// 验证字段值
		enabledInfoCacheVal := getFieldValue(options, "enabledInfoCache")
		if enabledInfoCacheVal == nil {
			t.Error("enabledInfoCache field not found")
			return
		}
		enabledInfoCache := enabledInfoCacheVal.(bool)

		traceEsQueryRateVal := getFieldValue(options, "traceEsQueryRate")
		if traceEsQueryRateVal == nil {
			t.Error("traceEsQueryRate field not found")
			return
		}
		traceEsQueryRate := traceEsQueryRateVal.(int)

		metricReportEnabledVal := getFieldValue(options, "metricReportEnabled")
		if metricReportEnabledVal == nil {
			t.Error("metricReportEnabled field not found")
			return
		}
		metricReportEnabled := metricReportEnabledVal.(bool)

		infoReportEnabledVal := getFieldValue(options, "infoReportEnabled")
		if infoReportEnabledVal == nil {
			t.Error("infoReportEnabled field not found")
			return
		}
		infoReportEnabled := infoReportEnabledVal.(bool)

		metricLayer4ReportEnabledVal := getFieldValue(options, "metricLayer4ReportEnabled")
		if metricLayer4ReportEnabledVal == nil {
			t.Error("metricLayer4ReportEnabled field not found")
			return
		}
		metricLayer4ReportEnabled := metricLayer4ReportEnabledVal.(bool)

		if !enabledInfoCache {
			t.Error("enabledInfoCache should be true")
		}
		if traceEsQueryRate != 50 {
			t.Errorf("traceEsQueryRate should be 50, got %d", traceEsQueryRate)
		}
		if !metricReportEnabled {
			t.Error("metricReportEnabled should be true")
		}
		if !infoReportEnabled {
			t.Error("infoReportEnabled should be true")
		}
		if metricLayer4ReportEnabled {
			t.Error("metricLayer4ReportEnabled should be false")
		}
	})

	t.Run("测试 window.DistributiveWindowOptions 映射", func(t *testing.T) {
		j := `{
    	  "sub_window_size": 10,
    	  "watch_expired_interval": 5000000000,
    	  "concurrent_process_count": 20,
    	  "concurrent_expiration_maximum": 5,
    	  "mapping_max_span_count": 1000
    	}`
		distributiveWindowOptions := jsonToMap(t, j)

		options := &window.DistributiveWindowOptions{}
		CopyFromMap(options, distributiveWindowOptions)
		log.Printf("options: %+v", options)
		// 验证字段值
		subWindowSizeVal := getFieldValue(options, "subWindowSize")
		if subWindowSizeVal == nil {
			t.Error("subWindowSize field not found")
			return
		}
		subWindowSize := subWindowSizeVal.(int)

		watchExpiredIntervalVal := getFieldValue(options, "watchExpiredInterval")
		if watchExpiredIntervalVal == nil {
			t.Error("watchExpiredInterval field not found")
			return
		}
		watchExpiredInterval := watchExpiredIntervalVal.(time.Duration)

		concurrentProcessCountVal := getFieldValue(options, "concurrentProcessCount")
		if concurrentProcessCountVal == nil {
			t.Error("concurrentProcessCount field not found")
			return
		}
		concurrentProcessCount := concurrentProcessCountVal.(int)

		concurrentExpirationMaximumVal := getFieldValue(options, "concurrentExpirationMaximum")
		if concurrentExpirationMaximumVal == nil {
			t.Error("concurrentExpirationMaximum field not found")
			return
		}
		concurrentExpirationMaximum := concurrentExpirationMaximumVal.(int)

		mappingMaxSpanCountVal := getFieldValue(options, "mappingMaxSpanCount")
		if mappingMaxSpanCountVal == nil {
			t.Error("mappingMaxSpanCount field not found")
			return
		}
		mappingMaxSpanCount := mappingMaxSpanCountVal.(int)

		if subWindowSize != 10 {
			t.Errorf("subWindowSize should be 10, got %d", subWindowSize)
		}
		if watchExpiredInterval != 5*time.Second {
			t.Errorf("watchExpiredInterval should be 5s, got %v", watchExpiredInterval)
		}
		if concurrentProcessCount != 20 {
			t.Errorf("concurrentProcessCount should be 20, got %d", concurrentProcessCount)
		}
		if concurrentExpirationMaximum != 5 {
			t.Errorf("concurrentExpirationMaximum should be 5, got %d", concurrentExpirationMaximum)
		}
		if mappingMaxSpanCount != 1000 {
			t.Errorf("mappingMaxSpanCount should be 1000, got %d", mappingMaxSpanCount)
		}
	})

	t.Run("测试混合字段名格式", func(t *testing.T) {
		j := `{
    	  "enabledInfoCache": true,
    	  "trace_es_query_rate": 30,
    	  "metricReportEnabled": true,
    	  "info_report_enabled": true,
    	  "metricLayer4ReportEnabled": false
    	}`
		processorOptions := jsonToMap(t, j)

		options := &window.ProcessorOptions{}
		CopyFromMap(options, processorOptions)
		log.Printf("options: %+v", options)
		// 验证字段值
		enabledInfoCacheVal := getFieldValue(options, "enabledInfoCache")
		if enabledInfoCacheVal == nil {
			t.Error("enabledInfoCache field not found")
			return
		}
		enabledInfoCache := enabledInfoCacheVal.(bool)

		traceEsQueryRateVal := getFieldValue(options, "traceEsQueryRate")
		if traceEsQueryRateVal == nil {
			t.Error("traceEsQueryRate field not found")
			return
		}
		traceEsQueryRate := traceEsQueryRateVal.(int)

		metricReportEnabledVal := getFieldValue(options, "metricReportEnabled")
		if metricReportEnabledVal == nil {
			t.Error("metricReportEnabled field not found")
			return
		}
		metricReportEnabled := metricReportEnabledVal.(bool)

		infoReportEnabledVal := getFieldValue(options, "infoReportEnabled")
		if infoReportEnabledVal == nil {
			t.Error("infoReportEnabled field not found")
			return
		}
		infoReportEnabled := infoReportEnabledVal.(bool)

		metricLayer4ReportEnabledVal := getFieldValue(options, "metricLayer4ReportEnabled")
		if metricLayer4ReportEnabledVal == nil {
			t.Error("metricLayer4ReportEnabled field not found")
			return
		}
		metricLayer4ReportEnabled := metricLayer4ReportEnabledVal.(bool)

		if !enabledInfoCache {
			t.Error("enabledInfoCache should be true")
		}
		if traceEsQueryRate != 30 {
			t.Errorf("traceEsQueryRate should be 30, got %d", traceEsQueryRate)
		}
		if !metricReportEnabled {
			t.Error("metricReportEnabled should be true")
		}
		if !infoReportEnabled {
			t.Error("infoReportEnabled should be true")
		}
		if metricLayer4ReportEnabled {
			t.Error("metricLayer4ReportEnabled should be false")
		}
	})

	t.Run("测试 metrics.MetricOptions 映射", func(t *testing.T) {
		j := `{
    	  "enabled_profile": true,
    	  "profile_address": "http://localhost:8080",
    	  "profile_token": "test_token",
    	  "profile_app_idx": "test_app",
    	  "report_interval": 2000000000
    	}`
		metricOptions := jsonToMap(t, j)

		options := &MetricOptions{}
		CopyFromMap(options, metricOptions)
		log.Printf("options: %+v", options)
		// 验证字段值
		enabledProfileVal := getFieldValue(options, "enabledProfile")
		if enabledProfileVal == nil {
			t.Error("enabledProfile field not found")
			return
		}
		enabledProfile := enabledProfileVal.(bool)

		profileAddressVal := getFieldValue(options, "profileAddress")
		if profileAddressVal == nil {
			t.Error("profileAddress field not found")
			return
		}
		profileAddress := profileAddressVal.(string)

		profileTokenVal := getFieldValue(options, "profileToken")
		if profileTokenVal == nil {
			t.Error("profileToken field not found")
			return
		}
		profileToken := profileTokenVal.(string)

		profileAppIdxVal := getFieldValue(options, "profileAppIdx")
		if profileAppIdxVal == nil {
			t.Error("profileAppIdx field not found")
			return
		}
		profileAppIdx := profileAppIdxVal.(string)

		reportIntervalVal := getFieldValue(options, "reportInterval")
		if reportIntervalVal == nil {
			t.Error("reportInterval field not found")
			return
		}
		reportInterval := reportIntervalVal.(time.Duration)

		if !enabledProfile {
			t.Error("enabledProfile should be true")
		}
		if profileAddress != "http://localhost:8080" {
			t.Errorf("profileAddress should be http://localhost:8080, got %s", profileAddress)
		}
		if profileToken != "test_token" {
			t.Errorf("profileToken should be test_token, got %s", profileToken)
		}
		if profileAppIdx != "test_app" {
			t.Errorf("profileAppIdx should be test_app, got %s", profileAppIdx)
		}
		if reportInterval != 2*time.Second {
			t.Errorf("reportInterval should be 2s, got %v", reportInterval)
		}
	})

	t.Run("测试 notifier.Options 映射（基本字段）", func(t *testing.T) {
		j := `{
    	  "chan_buffer_size": 1000,
    	  "qps": 10,
    	  "kafka_config": {
    	    "kafka_topic": "topic_a",
    	    "kafka_group_id": "group_a",
    	    "kafka_host": "127.0.0.1:9092",
    	    "kafka_username": "user",
    	    "kafka_password": "pass"
    	  }
    	}`
		notifierOptions := jsonToMap(t, j)

		options := &notifier.Options{}
		CopyFromMap(options, notifierOptions)
		log.Printf("options: %+v", options)
		// 验证字段值
		chanBufferSizeVal := getFieldValue(options, "chanBufferSize")
		if chanBufferSizeVal == nil {
			t.Error("chanBufferSize field not found")
			return
		}
		chanBufferSize := chanBufferSizeVal.(int)

		qpsVal := getFieldValue(options, "qps")
		if qpsVal == nil {
			t.Error("qps field not found")
			return
		}
		qps := qpsVal.(int)

		if chanBufferSize != 1000 {
			t.Errorf("chanBufferSize should be 1000, got %d", chanBufferSize)
		}
		if qps != 10 {
			t.Errorf("qps should be 10, got %d", qps)
		}

		t.Logf("notifier.Options 基本字段映射成功: chanBufferSize=%d, qps=%d", chanBufferSize, qps)

		// 校验 kafka 嵌入字段（来自嵌套的 kafka_config）
		if got := getFieldValue(options, "KafkaTopic").(string); got != "topic_a" {
			t.Fatalf("KafkaTopic want topic_a got %s", got)
		}
		if got := getFieldValue(options, "KafkaGroupId").(string); got != "group_a" {
			t.Fatalf("KafkaGroupId want group_a got %s", got)
		}
		if got := getFieldValue(options, "KafkaHost").(string); got != "127.0.0.1:9092" {
			t.Fatalf("KafkaHost want 127.0.0.1:9092 got %s", got)
		}
		if got := getFieldValue(options, "KafkaUsername").(string); got != "user" {
			t.Fatalf("KafkaUsername want user got %s", got)
		}
		if got := getFieldValue(options, "KafkaPassword").(string); got != "pass" {
			t.Fatalf("KafkaPassword want pass got %s", got)
		}
		// ctx 不从 JSON 设置，此处不校验
	})

	t.Run("测试 storage.ProxyOptions 映射（基本字段）", func(t *testing.T) {
		j := `{
		  "worker_count": 5,
		  "save_hold_duration": 1000000000,
		  "save_hold_max_count": 100,
		  "save_req_buffer_size": 2000,
		  "cache_backend": "memory",
		  "trace_es_config": {"index_name": "trace_index_min", "host": "http://localhost:9200", "username": "u", "password": "p"},
		  "save_es_config": {"index_name": "save_index_min", "host": "http://localhost:9200", "username": "u", "password": "p"},
		  "prometheus_writer_config": {"url": "http://localhost:9090/api/v1/write", "headers": {"X-BK-TOKEN": "t"}},
		  "metrics_config": {"relation_metric_mem_duration": 60000000000, "flow_metric_mem_duration": 120000000000, "flow_metric_buckets": [0.1, 1.5]}
		}`
		storageOptions := jsonToMap(t, j)

		options := &storage.ProxyOptions{}
		CopyFromMap(options, storageOptions)
		log.Printf("options: %+v", options)
		// 验证字段值
		workerCountVal := getFieldValue(options, "workerCount")
		if workerCountVal == nil {
			t.Error("workerCount field not found")
			return
		}
		workerCount := workerCountVal.(int)

		saveHoldDurationVal := getFieldValue(options, "saveHoldDuration")
		if saveHoldDurationVal == nil {
			t.Error("saveHoldDuration field not found")
			return
		}
		saveHoldDuration := saveHoldDurationVal.(time.Duration)

		saveHoldMaxCountVal := getFieldValue(options, "saveHoldMaxCount")
		if saveHoldMaxCountVal == nil {
			t.Error("saveHoldMaxCount field not found")
			return
		}
		saveHoldMaxCount := saveHoldMaxCountVal.(int)

		saveReqBufferSizeVal := getFieldValue(options, "saveReqBufferSize")
		if saveReqBufferSizeVal == nil {
			t.Error("saveReqBufferSize field not found")
			return
		}
		saveReqBufferSize := saveReqBufferSizeVal.(int)

		if workerCount != 5 {
			t.Errorf("workerCount should be 5, got %d", workerCount)
		}
		if saveHoldDuration != 1*time.Second {
			t.Errorf("saveHoldDuration should be 1s, got %v", saveHoldDuration)
		}
		if saveHoldMaxCount != 100 {
			t.Errorf("saveHoldMaxCount should be 100, got %d", saveHoldMaxCount)
		}
		if saveReqBufferSize != 2000 {
			t.Errorf("saveReqBufferSize should be 2000, got %d", saveReqBufferSize)
		}

		t.Logf("storage.ProxyOptions 基本字段映射成功")

		// 新增校验：cache_backend
		if got := getFieldValue(options, "cacheBackend").(storage.CacheType); string(got) != "memory" {
			t.Fatalf("cacheBackend want memory got %s", got)
		}

		// 最小集：ES/Prom/Metric
		traceEs := getFieldValue(options, "traceEsConfig").(storage.EsOptions)
		if got := getFieldValue(traceEs, "indexName").(string); got != "trace_index_min" {
			t.Fatalf("traceEs.indexName want trace_index_min got %s", got)
		}
		if got := getFieldValue(traceEs, "host").(string); got != "http://localhost:9200" {
			t.Fatalf("traceEs.host want http://localhost:9200 got %s", got)
		}
		if got := getFieldValue(traceEs, "username").(string); got != "u" {
			t.Fatalf("traceEs.username want u got %s", got)
		}
		if got := getFieldValue(traceEs, "password").(string); got != "p" {
			t.Fatalf("traceEs.password want p got %s", got)
		}
		
		saveEs := getFieldValue(options, "saveEsConfig").(storage.EsOptions)
		if got := getFieldValue(saveEs, "indexName").(string); got != "save_index_min" {
			t.Fatalf("saveEs.indexName want save_index_min got %s", got)
		}
		if got := getFieldValue(saveEs, "host").(string); got != "http://localhost:9200" {
			t.Fatalf("saveEs.host want http://localhost:9200 got %s", got)
		}
		if got := getFieldValue(saveEs, "username").(string); got != "u" {
			t.Fatalf("saveEs.username want u got %s", got)
		}
		if got := getFieldValue(saveEs, "password").(string); got != "p" {
			t.Fatalf("saveEs.password want p got %s", got)
		}

		writer := getFieldValue(options, "prometheusWriterConfig")
		if got := getFieldValue(writer, "Url").(string); got != "http://localhost:9090/api/v1/write" {
			t.Fatalf("prometheusWriterConfig.Url want http://localhost:9090/api/v1/write got %s", got)
		}
		
		headers := getFieldValue(writer, "Headers").(map[string]string)
		if len(headers) != 1 || headers["X-BK-TOKEN"] != "t" {
			t.Fatalf("prometheusWriterConfig.Headers want {X-BK-TOKEN: t} got %+v", headers)
		}
		metricsCfg := getFieldValue(options, "metricsConfig").(storage.MetricConfigOptions)
		if got := getFieldValue(metricsCfg, "relationMetricMemDuration").(time.Duration); got != time.Minute {
			t.Fatalf("metrics.relationMetricMemDuration want 1m got %v", got)
		}
		if got := getFieldValue(metricsCfg, "flowMetricMemDuration").(time.Duration); got != 2*time.Minute {
			t.Fatalf("metrics.flowMetricMemDuration want 2m got %v", got)
		}
		b := getFieldValue(metricsCfg, "flowMetricBuckets").([]float64)
		if len(b) != 2 || b[0] != 0.1 || b[1] != 1.5 {
			t.Fatalf("metrics.flowMetricBuckets want [0.1 1.5] got %+v", b)
		}
	})

	// 全字段覆盖
	t.Run("测试 storage.ProxyOptions 映射（全部字段）", func(t *testing.T) {
		j := `{
		  "worker_count": 7,
		  "save_hold_duration": 2000000000,
		  "save_hold_max_count": 321,
		  "save_req_buffer_size": 4096,
		  "cache_backend": "redis",
		  "redis_cache_config": {
		    "mode": "single",
		    "host": "127.0.0.1",
		    "port": 6380,
		    "sentinel_address": ["127.0.0.1:26379", "127.0.0.1:26380"],
		    "master_name": "mymaster",
		    "sentinel_password": "sentinel_pass",
		    "password": "redis_pass",
		    "db": 1,
		    "dial_timeout": 15000000000,
		    "read_timeout": 7000000000
		  },
		  "bloom_config": {
		    "fp_rate": 0.02,
		    "normal_memory_bloom_options": {"auto_clean": 3600000000000},
		    "normal_memory_quotient_options": {"magnitude_per_min": 2000},
		    "normal_overlap_bloom_options": {"reset_duration": 7200000000000},
		    "layers_bloom_options": {"layers": 4},
		    "layers_cap_decrease_bloom_options": {"cap": 200, "layers": 3, "divisor": 2}
		  },
		  "trace_es_config": {"index_name": "trace_index", "host": "http://localhost:9200", "username": "elastic", "password": "password"},
		  "save_es_config": {"index_name": "save_index", "host": "http://localhost:9200", "username": "elastic", "password": "password"},
		  "prometheus_writer_config": {"url": "http://localhost:9090/api/v1/write", "headers": {"X-BK-TOKEN": "abc"}},
		  "metrics_config": {"relation_metric_mem_duration": 300000000000, "flow_metric_mem_duration": 600000000000, "flow_metric_buckets": [0.1, 0.5, 1.0, 2.0, 5.0]}
		}`
		storageOptions := jsonToMap(t, j)

		options := &storage.ProxyOptions{}
		CopyFromMap(options, storageOptions)
		log.Printf("options: %+v", options)
		// 基本字段
		if got := getFieldValue(options, "workerCount").(int); got != 7 {
			t.Fatalf("workerCount want %d got %d", 7, got)
		}
		if got := getFieldValue(options, "saveHoldDuration").(time.Duration); got != 2*time.Second {
			t.Fatalf("saveHoldDuration want %v got %v", 2*time.Second, got)
		}
		if got := getFieldValue(options, "saveHoldMaxCount").(int); got != 321 {
			t.Fatalf("saveHoldMaxCount want %d got %d", 321, got)
		}
		if got := getFieldValue(options, "saveReqBufferSize").(int); got != 4096 {
			t.Fatalf("saveReqBufferSize want %d got %d", 4096, got)
		}
		if got := getFieldValue(options, "cacheBackend").(storage.CacheType); string(got) != "redis" {
			t.Fatalf("cacheBackend want redis got %s", got)
		}

		// Redis
		redisCfg := getFieldValue(options, "redisCacheConfig").(storage.RedisCacheOptions)
		if got := getFieldValue(redisCfg, "mode").(string); got != "single" {
			t.Fatalf("redis.mode want single got %s", got)
		}
		if got := getFieldValue(redisCfg, "host").(string); got != "127.0.0.1" {
			t.Fatalf("redis.host want 127.0.0.1 got %s", got)
		}
		if got := getFieldValue(redisCfg, "port").(int); got != 6380 {
			t.Fatalf("redis.port want 6380 got %d", got)
		}
		// sentinelAddress 列表
		if addrs := getFieldValue(redisCfg, "sentinelAddress").([]string); len(addrs) != 2 || addrs[0] != "127.0.0.1:26379" || addrs[1] != "127.0.0.1:26380" {
			t.Fatalf("redis.sentinelAddress want [127.0.0.1:26379 127.0.0.1:26380] got %+v", addrs)
		}
		if got := getFieldValue(redisCfg, "masterName").(string); got != "mymaster" {
			t.Fatalf("redis.masterName want mymaster got %s", got)
		}
		if got := getFieldValue(redisCfg, "sentinelPassword").(string); got != "sentinel_pass" {
			t.Fatalf("redis.sentinelPassword want sentinel_pass got %s", got)
		}
		if got := getFieldValue(redisCfg, "password").(string); got != "redis_pass" {
			t.Fatalf("redis.password want redis_pass got %s", got)
		}
		if got := getFieldValue(redisCfg, "db").(int); got != 1 {
			t.Fatalf("redis.db want 1 got %d", got)
		}
		if got := getFieldValue(redisCfg, "dialTimeout").(time.Duration); got != 15*time.Second {
			t.Fatalf("redis.dialTimeout want 15s got %v", got)
		}
		if got := getFieldValue(redisCfg, "readTimeout").(time.Duration); got != 7*time.Second {
			t.Fatalf("redis.readTimeout want 7s got %v", got)
		}

		// Bloom
		bloomCfg := getFieldValue(options, "bloomConfig").(storage.BloomOptions)
		if got := getFieldValue(bloomCfg, "fpRate").(float64); got != 0.02 {
			t.Fatalf("bloom.fpRate want 0.02 got %f", got)
		}
		memBloom := getFieldValue(bloomCfg, "normalMemoryBloomOptions").(storage.MemoryBloomOptions)
		if got := getFieldValue(memBloom, "autoClean").(time.Duration); got != time.Hour {
			t.Fatalf("bloom.normalMemoryBloomOptions.autoClean want 1h got %v", got)
		}
		quot := getFieldValue(bloomCfg, "normalMemoryQuotientOptions").(storage.QuotientFilterOptions)
		if got := getFieldValue(quot, "magnitudePerMin").(int); got != 2000 {
			t.Fatalf("bloom.normalMemoryQuotientOptions.magnitudePerMin want 2000 got %d", got)
		}
		overlap := getFieldValue(bloomCfg, "normalOverlapBloomOptions").(storage.OverlapBloomOptions)
		if got := getFieldValue(overlap, "resetDuration").(time.Duration); got != 2*time.Hour {
			t.Fatalf("bloom.normalOverlapBloomOptions.resetDuration want 2h got %v", got)
		}
		layers := getFieldValue(bloomCfg, "layersBloomOptions").(storage.LayersBloomOptions)
		if got := getFieldValue(layers, "layers").(int); got != 4 {
			t.Fatalf("bloom.layersBloomOptions.layers want 4 got %d", got)
		}
		layersDec := getFieldValue(bloomCfg, "layersCapDecreaseBloomOptions").(storage.LayersCapDecreaseBloomOptions)
		if got := getFieldValue(layersDec, "cap").(int); got != 200 {
			t.Fatalf("bloom.layersCapDecreaseBloomOptions.cap want 200 got %d", got)
		}
		if got := getFieldValue(layersDec, "layers").(int); got != 3 {
			t.Fatalf("bloom.layersCapDecreaseBloomOptions.layers want 3 got %d", got)
		}
		if got := getFieldValue(layersDec, "divisor").(int); got != 2 {
			t.Fatalf("bloom.layersCapDecreaseBloomOptions.divisor want 2 got %d", got)
		}

		// ES
		traceEs := getFieldValue(options, "traceEsConfig").(storage.EsOptions)
		if got := getFieldValue(traceEs, "indexName").(string); got != "trace_index" {
			t.Fatalf("traceEs.indexName want trace_index got %s", got)
		}
		if got := getFieldValue(traceEs, "host").(string); got != "http://localhost:9200" {
			t.Fatalf("traceEs.host want http://localhost:9200 got %s", got)
		}
		if got := getFieldValue(traceEs, "username").(string); got != "elastic" {
			t.Fatalf("traceEs.username want elastic got %s", got)
		}
		if got := getFieldValue(traceEs, "password").(string); got != "password" {
			t.Fatalf("traceEs.password want password got %s", got)
		}

		saveEs := getFieldValue(options, "saveEsConfig").(storage.EsOptions)
		if got := getFieldValue(saveEs, "indexName").(string); got != "save_index" {
			t.Fatalf("saveEs.indexName want save_index got %s", got)
		}
		if got := getFieldValue(saveEs, "host").(string); got != "http://localhost:9200" {
			t.Fatalf("saveEs.host want http://localhost:9200 got %s", got)
		}
		if got := getFieldValue(saveEs, "username").(string); got != "elastic" {
			t.Fatalf("saveEs.username want elastic got %s", got)
		}
		if got := getFieldValue(saveEs, "password").(string); got != "password" {
			t.Fatalf("saveEs.password want password got %s", got)
		}

		// Prometheus Writer
		writer := getFieldValue(options, "prometheusWriterConfig")
		if got := getFieldValue(writer, "Url").(string); got != "http://localhost:9090/api/v1/write" {
			t.Fatalf("prometheusWriterConfig.Url want http://localhost:9090/api/v1/write got %s", got)
		}
		headers := getFieldValue(writer, "Headers").(map[string]string)
		if len(headers) != 1 || headers["X-BK-TOKEN"] != "abc" {
			t.Fatalf("prometheusWriterConfig.Headers want {X-BK-TOKEN: abc} got %+v", headers)
		}

		// Metrics Config
		metricsCfg := getFieldValue(options, "metricsConfig").(storage.MetricConfigOptions)
		if got := getFieldValue(metricsCfg, "relationMetricMemDuration").(time.Duration); got != 5*time.Minute {
			t.Fatalf("metrics.relationMetricMemDuration want 5m got %v", got)
		}
		if got := getFieldValue(metricsCfg, "flowMetricMemDuration").(time.Duration); got != 10*time.Minute {
			t.Fatalf("metrics.flowMetricMemDuration want 10m got %v", got)
		}
		buckets := getFieldValue(metricsCfg, "flowMetricBuckets").([]float64)
		expectedBuckets := []float64{0.1, 0.5, 1.0, 2.0, 5.0}
		if len(buckets) != len(expectedBuckets) {
			t.Fatalf("metrics.flowMetricBuckets len want %d got %d", len(expectedBuckets), len(buckets))
		}
		for i, v := range expectedBuckets {
			if buckets[i] != v {
				t.Fatalf("metrics.flowMetricBuckets[%d] want %v got %v", i, v, buckets[i])
			}
		}
	})

	t.Run("测试混合字段名格式 - 所有类型", func(t *testing.T) {
		// 测试 metrics.MetricOptions
		metricOptions := map[string]interface{}{
			"enabledProfile":  true,                    // camelCase
			"profile_address": "http://localhost:8080", // snake_case
			"profileToken":    "test_token",            // camelCase
			"profile_app_idx": "test_app",              // snake_case
			"reportInterval":  int64(3000000000),       // camelCase, 3 seconds
		}

		options := &MetricOptions{}
		CopyFromMap(options, metricOptions)
		log.Printf("options: %+v", options)
		enabledProfile := getFieldValue(options, "enabledProfile").(bool)
		profileAddress := getFieldValue(options, "profileAddress").(string)
		profileToken := getFieldValue(options, "profileToken").(string)
		profileAppIdx := getFieldValue(options, "profileAppIdx").(string)
		reportInterval := getFieldValue(options, "reportInterval").(time.Duration)

		if !enabledProfile {
			t.Error("enabledProfile should be true")
		}
		if profileAddress != "http://localhost:8080" {
			t.Errorf("profileAddress should be http://localhost:8080, got %s", profileAddress)
		}
		if profileToken != "test_token" {
			t.Errorf("profileToken should be test_token, got %s", profileToken)
		}
		if profileAppIdx != "test_app" {
			t.Errorf("profileAppIdx should be test_app, got %s", profileAppIdx)
		}
		if reportInterval != 3*time.Second {
			t.Errorf("reportInterval should be 3s, got %v", reportInterval)
		}
	})
}
