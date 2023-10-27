# bk-collector 压测报告

* 机器配置: 16Core 32GB
* Golang 版本: 1.18.2

## TokenChecker(aes256)

单核心 aes256 算法的解析速度可以达到 100w/s，在带缓存的情况下每秒可达 1000w 级别。

```shell
$ go test -bench='Aes256Decoder*' -cpu 1,2,4,8 -benchmem -run=none -benchtime=1s
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tokenchecker
cpu: AMD EPYC 7K62 48-Core Processor
BenchmarkAes256Decoder                   1000000              1086 ns/op             992 B/op         11 allocs/op
BenchmarkAes256Decoder-2                  993141              1019 ns/op             992 B/op         11 allocs/op
BenchmarkAes256Decoder-4                 1000000              1017 ns/op             992 B/op         11 allocs/op
BenchmarkAes256Decoder-8                 1000000              1053 ns/op             992 B/op         11 allocs/op
BenchmarkAes256DecoderWithCached        47216628                25.28 ns/op            0 B/op          0 allocs/op
BenchmarkAes256DecoderWithCached-2      47792042                25.08 ns/op            0 B/op          0 allocs/op
BenchmarkAes256DecoderWithCached-4      47342809                25.30 ns/op            0 B/op          0 allocs/op
BenchmarkAes256DecoderWithCached-8      47822095                25.24 ns/op            0 B/op          0 allocs/op
```

## Traces

### Decode bytes to OT Traces model

```shell
$ go test -bench='Traces*' -cpu 1,2,4,8 -benchmem -run=none
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/otlp
cpu: AMD EPYC 7K62 48-Core Processor

# 每条 traces 携带 10 spans
BenchmarkTracesUnmarshal_10_Spans                  76666             15954 ns/op            8536 B/op        211 allocs/op
BenchmarkTracesUnmarshal_10_Spans-2                76544             15578 ns/op            8536 B/op        211 allocs/op
BenchmarkTracesUnmarshal_10_Spans-4                78627             15356 ns/op            8536 B/op        211 allocs/op
BenchmarkTracesUnmarshal_10_Spans-8                78080             15159 ns/op            8536 B/op        211 allocs/op

# 每条 traces 携带 100 spans
BenchmarkTracesUnmarshal_100_Spans                  8097            150145 ns/op           80178 B/op       1924 allocs/op
BenchmarkTracesUnmarshal_100_Spans-2                7774            141590 ns/op           80178 B/op       1924 allocs/op
BenchmarkTracesUnmarshal_100_Spans-4                8949            133181 ns/op           80177 B/op       1924 allocs/op
BenchmarkTracesUnmarshal_100_Spans-8                8006            140745 ns/op           80178 B/op       1924 allocs/op

# 每条 traces 携带 10000 spans
BenchmarkTracesUnmarshal_1000_Spans                  771           1612824 ns/op          802754 B/op      19035 allocs/op
BenchmarkTracesUnmarshal_1000_Spans-2                698           1528379 ns/op          802861 B/op      19036 allocs/op
BenchmarkTracesUnmarshal_1000_Spans-4                806           1464847 ns/op          802713 B/op      19035 allocs/op
BenchmarkTracesUnmarshal_1000_Spans-8                766           1563260 ns/op          802766 B/op      19035 allocs/op

# 每条 traces 携带 10000 spans
BenchmarkTracesUnmarshal_10000_Spans                  67          17496423 ns/op         8235794 B/op     190931 allocs/op
BenchmarkTracesUnmarshal_10000_Spans-2                75          15645410 ns/op         8223246 B/op     190835 allocs/op
BenchmarkTracesUnmarshal_10000_Spans-4                79          15200804 ns/op         8217928 B/op     190795 allocs/op
BenchmarkTracesUnmarshal_10000_Spans-8                74          15369820 ns/op         8224688 B/op     190846 allocs/op
```

### Decode to bkevent

解编码序列化开销，分别测试了单请求不同 spans 数量的解析性能。每次 traces 携带 10spans 的话每秒钟能解析 3w+，随着数据量的增加解析性能逐渐下降，10000spans 的时候已经只有 ~20 次。

```shell
$ go test -bench='Traces*' -cpu 1,2,4,8 -benchmem -run=none                     
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/converter
cpu: AMD EPYC 7K62 48-Core Processor

# 每条 traces 携带 10 spans
BenchmarkTracesConvert_10_Span             35800             32758 ns/op           32320 B/op        281 allocs/op
BenchmarkTracesConvert_10_Span-2           38348             31188 ns/op           32322 B/op        281 allocs/op
BenchmarkTracesConvert_10_Span-4           38014             32179 ns/op           32322 B/op        281 allocs/op
BenchmarkTracesConvert_10_Span-8           34137             34805 ns/op           32319 B/op        281 allocs/op

# 每条 traces 携带 100 spans
BenchmarkTracesConvert_100_Span             3706            323753 ns/op          319044 B/op       2740 allocs/op
BenchmarkTracesConvert_100_Span-2           3582            305872 ns/op          319046 B/op       2740 allocs/op
BenchmarkTracesConvert_100_Span-4           3464            336001 ns/op          319051 B/op       2740 allocs/op
BenchmarkTracesConvert_100_Span-8           3651            330371 ns/op          319036 B/op       2740 allocs/op

# 每条 traces 携带 1000 spans
BenchmarkTracesConvert_1000_Span             228           5088665 ns/op         3199066 B/op      27326 allocs/op
BenchmarkTracesConvert_1000_Span-2           304           4654120 ns/op         3198458 B/op      27319 allocs/op
BenchmarkTracesConvert_1000_Span-4           273           4409974 ns/op         3198624 B/op      27321 allocs/op
BenchmarkTracesConvert_1000_Span-8           271           4340106 ns/op         3198357 B/op      27320 allocs/op  # -> 3M

# 每条 traces 携带 10000 spans
BenchmarkTracesConvert_10000_Span             19          64821744 ns/op        32457694 B/op     276010 allocs/op
BenchmarkTracesConvert_10000_Span-2           24          45544275 ns/op        32394000 B/op     275361 allocs/op
BenchmarkTracesConvert_10000_Span-4           26          41802290 ns/op        32372053 B/op     275159 allocs/op
BenchmarkTracesConvert_10000_Span-8           28          39929184 ns/op        32358841 B/op     275006 allocs/op  # <- 30M
```

## Metrics

### Decode bytes to OT Metrics model

```shell
$ go test -bench='Metrics*' -cpu 1,2,4,8 -benchmem -run=none
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/otlp
cpu: AMD EPYC 7K62 48-Core Processor

# 每次请求携带 100 个数据点
BenchmarkMetricsUnmarshal_100_DataPoints            2450            475944 ns/op          247210 B/op       7127 allocs/op
BenchmarkMetricsUnmarshal_100_DataPoints-2          2647            479262 ns/op          247203 B/op       7127 allocs/op
BenchmarkMetricsUnmarshal_100_DataPoints-4          2542            451071 ns/op          247208 B/op       7127 allocs/op
BenchmarkMetricsUnmarshal_100_DataPoints-8          2536            459764 ns/op          247208 B/op       7127 allocs/op

# 每次请求携带 1000 个数据点
BenchmarkMetricsUnmarshal_1000_DataPoints            210           5686503 ns/op         2482910 B/op      71183 allocs/op
BenchmarkMetricsUnmarshal_1000_DataPoints-2          195           5283222 ns/op         2483767 B/op      71195 allocs/op
BenchmarkMetricsUnmarshal_1000_DataPoints-4          253           4759858 ns/op         2481031 B/op      71157 allocs/op
BenchmarkMetricsUnmarshal_1000_DataPoints-8          230           5600985 ns/op         2481965 B/op      71170 allocs/op

# 每次请求携带 10000 个数据点
BenchmarkMetricsUnmarshal_10000_DataPoints            18          64420212 ns/op        26307140 B/op     727820 allocs/op
BenchmarkMetricsUnmarshal_10000_DataPoints-2          22          50656280 ns/op        26069686 B/op     724587 allocs/op
BenchmarkMetricsUnmarshal_10000_DataPoints-4          21          51973117 ns/op        26120557 B/op     725280 allocs/op
BenchmarkMetricsUnmarshal_10000_DataPoints-8          22          49818006 ns/op        26069710 B/op     724588 allocs/op
```

### Decode to bkevent

测试了 Gauge、Counter、Histogram 三种不同数据类型的指标在不同数据量级的性能表现。

```shell
$ go test -bench='Metrics*' -cpu 1,2,4,8 -benchmem -run=none
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/converter
cpu: AMD EPYC 7K62 48-Core Processor

# 每条 metrics 携带 10 数据点
BenchmarkMetricsConvert_10_Gauge_DataPoint                 41794             28708 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Gauge_DataPoint-2               38311             29707 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Gauge_DataPoint-4               42267             28818 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Gauge_DataPoint-8               38520             31027 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Counter_DataPoint               41443             29723 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Counter_DataPoint-2             42217             29544 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Counter_DataPoint-4             38998             29837 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Counter_DataPoint-8             40880             30560 ns/op           16176 B/op        275 allocs/op
BenchmarkMetricsConvert_10_Histogram_DataPoint             32671             36527 ns/op           23408 B/op        366 allocs/op
BenchmarkMetricsConvert_10_Histogram_DataPoint-2           33414             35307 ns/op           23408 B/op        366 allocs/op
BenchmarkMetricsConvert_10_Histogram_DataPoint-4           33543             35908 ns/op           23408 B/op        366 allocs/op
BenchmarkMetricsConvert_10_Histogram_DataPoint-8           33852             35964 ns/op           23408 B/op        366 allocs/op

# 每条 metrics 携带 100 数据点
BenchmarkMetricsConvert_100_Gauge_DataPoint                 4131            288893 ns/op          160904 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Gauge_DataPoint-2               3993            291638 ns/op          160905 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Gauge_DataPoint-4               4078            290397 ns/op          160905 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Gauge_DataPoint-8               3909            299560 ns/op          160906 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Counter_DataPoint               3754            290969 ns/op          160906 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Counter_DataPoint-2             4070            286429 ns/op          160905 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Counter_DataPoint-4             4112            293245 ns/op          160905 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Counter_DataPoint-8             3780            310648 ns/op          160907 B/op       2708 allocs/op
BenchmarkMetricsConvert_100_Histogram_DataPoint             3240            358986 ns/op          232208 B/op       3609 allocs/op
BenchmarkMetricsConvert_100_Histogram_DataPoint-2           3016            359071 ns/op          232210 B/op       3609 allocs/op
BenchmarkMetricsConvert_100_Histogram_DataPoint-4           2902            367037 ns/op          232213 B/op       3609 allocs/op
BenchmarkMetricsConvert_100_Histogram_DataPoint-8           3091            369360 ns/op          232211 B/op       3609 allocs/op

# 每条 metrics 携带 1000 数据点
BenchmarkMetricsConvert_1000_Gauge_DataPoint                 330           3647447 ns/op         1621388 B/op      27048 allocs/op
BenchmarkMetricsConvert_1000_Gauge_DataPoint-2               337           3610533 ns/op         1621327 B/op      27047 allocs/op
BenchmarkMetricsConvert_1000_Gauge_DataPoint-4               343           3566420 ns/op         1621283 B/op      27047 allocs/op
BenchmarkMetricsConvert_1000_Gauge_DataPoint-8               349           3476671 ns/op         1621239 B/op      27046 allocs/op
BenchmarkMetricsConvert_1000_Counter_DataPoint               319           3880802 ns/op         1621514 B/op      27049 allocs/op
BenchmarkMetricsConvert_1000_Counter_DataPoint-2             321           3783145 ns/op         1621497 B/op      27049 allocs/op
BenchmarkMetricsConvert_1000_Counter_DataPoint-4             346           3233224 ns/op         1621281 B/op      27046 allocs/op
BenchmarkMetricsConvert_1000_Counter_DataPoint-8             348           3441307 ns/op         1621272 B/op      27046 allocs/op
BenchmarkMetricsConvert_1000_Histogram_DataPoint             266           4512578 ns/op         2363918 B/op      36055 allocs/op
BenchmarkMetricsConvert_1000_Histogram_DataPoint-2           302           4240235 ns/op         2363454 B/op      36050 allocs/op
BenchmarkMetricsConvert_1000_Histogram_DataPoint-4           290           3780847 ns/op         2363600 B/op      36052 allocs/op
BenchmarkMetricsConvert_1000_Histogram_DataPoint-8           291           3973454 ns/op         2363594 B/op      36052 allocs/op

# Gauge、Counter、Histogram 每种数据类型各 1000。
BenchmarkMetricsConvert_1000_DataPoint                        76          13649774 ns/op         5674810 B/op      90477 allocs/op
BenchmarkMetricsConvert_1000_DataPoint-2                      98          11841340 ns/op         5665948 B/op      90373 allocs/op
BenchmarkMetricsConvert_1000_DataPoint-4                     103          11591840 ns/op         5664468 B/op      90356 allocs/op
BenchmarkMetricsConvert_1000_DataPoint-8                     106          11165340 ns/op         5663656 B/op      90346 allocs/op  # <- 5M
```

## Logs

### Decode bytes to OT Metrics model

```shell
$ go test -bench='Logs*' -cpu 1,2,4,8 -benchmem -run=none   
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/otlp
cpu: AMD EPYC 7K62 48-Core Processor

# 每次请求携带 100 条日志，每条 10KB
BenchmarkMetricsUnmarshal_100x10KB_Logs             2824            397584 ns/op         1098391 B/op       2124 allocs/op
BenchmarkMetricsUnmarshal_100x10KB_Logs-2           2854            411307 ns/op         1098368 B/op       2124 allocs/op
BenchmarkMetricsUnmarshal_100x10KB_Logs-4           2731            402801 ns/op         1098468 B/op       2124 allocs/op
BenchmarkMetricsUnmarshal_100x10KB_Logs-8           2816            420436 ns/op         1098400 B/op       2124 allocs/op

# 每次请求携带 100 条日志，每条 100KB
BenchmarkMetricsUnmarshal_100x100KB_Logs             453           2506251 ns/op        10858476 B/op       2126 allocs/op
BenchmarkMetricsUnmarshal_100x100KB_Logs-2           424           2719317 ns/op        10867827 B/op       2126 allocs/op
BenchmarkMetricsUnmarshal_100x100KB_Logs-4           445           2499594 ns/op        10860936 B/op       2126 allocs/op
BenchmarkMetricsUnmarshal_100x100KB_Logs-8           472           2409995 ns/op        10852978 B/op       2126 allocs/op

# 每次请求携带 100 条日志，每条 1000KB
BenchmarkMetricsUnmarshal_100x1000KB_Logs              1        2530968420 ns/op        717770960 B/op      3077 allocs/op
BenchmarkMetricsUnmarshal_100x1000KB_Logs-2            1        2497116788 ns/op        717770864 B/op      3076 allocs/op
BenchmarkMetricsUnmarshal_100x1000KB_Logs-4            1        2514134762 ns/op        717771152 B/op      3079 allocs/op
BenchmarkMetricsUnmarshal_100x1000KB_Logs-8            1        2515841616 ns/op        717771152 B/op      3079 allocs/op

# 每次请求携带 1000 条日志，每条 10KB
BenchmarkMetricsUnmarshal_1000x10KB_Logs             210           4958893 ns/op        11260675 B/op      21071 allocs/op
BenchmarkMetricsUnmarshal_1000x10KB_Logs-2           262           4309140 ns/op        11201343 B/op      21062 allocs/op
BenchmarkMetricsUnmarshal_1000x10KB_Logs-4           237           4454211 ns/op        11226622 B/op      21066 allocs/op
BenchmarkMetricsUnmarshal_1000x10KB_Logs-8           234           4497739 ns/op        11230024 B/op      21066 allocs/op

# 每次请求携带 1000 条日志，每条 100KB
BenchmarkMetricsUnmarshal_1000x100KB_Logs              1        2538823359 ns/op        726411088 B/op     30085 allocs/op
BenchmarkMetricsUnmarshal_1000x100KB_Logs-2            1        2536967746 ns/op        726410992 B/op     30084 allocs/op
BenchmarkMetricsUnmarshal_1000x100KB_Logs-4            1        2510460797 ns/op        726411376 B/op     30088 allocs/op
BenchmarkMetricsUnmarshal_1000x100KB_Logs-8            1        2510771529 ns/op        726411472 B/op     30089 allocs/op
```

### Decode to bkevent

测试了不同日志数据量级对解析开销的影响。

```shell
$ go test -bench='Logs*' -cpu 1,2,4,8 -benchmem -run=none   
goos: linux
goarch: amd64
pkg: github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/converter
cpu: AMD EPYC 7K62 48-Core Processor

# 每条日志 1kb，每次请求携带 10 条日志
BenchmarkLogsConvert_10x1KB_LogRecords              8346            126286 ns/op           80778 B/op        779 allocs/op
BenchmarkLogsConvert_10x1KB_LogRecords-2            8527            123685 ns/op           80790 B/op        779 allocs/op
BenchmarkLogsConvert_10x1KB_LogRecords-4            8589            130864 ns/op           80812 B/op        779 allocs/op
BenchmarkLogsConvert_10x1KB_LogRecords-8            8142            131179 ns/op           80827 B/op        779 allocs/op

# 每条日志 10kb，每次请求携带 10 条日志
BenchmarkLogsConvert_10x10KB_LogRecords             4020            274331 ns/op          262667 B/op        779 allocs/op
BenchmarkLogsConvert_10x10KB_LogRecords-2           3808            268345 ns/op          262772 B/op        779 allocs/op
BenchmarkLogsConvert_10x10KB_LogRecords-4           3667            287840 ns/op          263072 B/op        779 allocs/op
BenchmarkLogsConvert_10x10KB_LogRecords-8           3968            296075 ns/op          263238 B/op        779 allocs/op

# 每条日志 100kb，每次请求携带 10 条日志
BenchmarkLogsConvert_10x100KB_LogRecords             615           1851869 ns/op         2183526 B/op        780 allocs/op
BenchmarkLogsConvert_10x100KB_LogRecords-2           519           1989507 ns/op         2199339 B/op        782 allocs/op
BenchmarkLogsConvert_10x100KB_LogRecords-4           662           1954552 ns/op         2194511 B/op        782 allocs/op
BenchmarkLogsConvert_10x100KB_LogRecords-8           568           1982716 ns/op         2207452 B/op        782 allocs/op

# 每条日志 1kb，每次请求携带 100 条日志
BenchmarkLogsConvert_100x1KB_LogRecords              934           1278967 ns/op          804116 B/op       7715 allocs/op
BenchmarkLogsConvert_100x1KB_LogRecords-2            912           1330602 ns/op          804301 B/op       7716 allocs/op
BenchmarkLogsConvert_100x1KB_LogRecords-4            914           1303465 ns/op          804498 B/op       7716 allocs/op
BenchmarkLogsConvert_100x1KB_LogRecords-8            842           1431635 ns/op          804775 B/op       7716 allocs/op

# 每条日志 10kb，每次请求携带 100 条日志
BenchmarkLogsConvert_100x10KB_LogRecords             307           3333774 ns/op         2638293 B/op       7719 allocs/op
BenchmarkLogsConvert_100x10KB_LogRecords-2           370           3218590 ns/op         2637688 B/op       7719 allocs/op
BenchmarkLogsConvert_100x10KB_LogRecords-4           325           3284460 ns/op         2641520 B/op       7721 allocs/op
BenchmarkLogsConvert_100x10KB_LogRecords-8           360           3255758 ns/op         2642762 B/op       7722 allocs/op

# 每条日志 100kb，每次请求携带 100 条日志
BenchmarkLogsConvert_100x100KB_LogRecords             42          24023292 ns/op        22977016 B/op       7739 allocs/op
BenchmarkLogsConvert_100x100KB_LogRecords-2           40          25049807 ns/op        23076379 B/op       7742 allocs/op
BenchmarkLogsConvert_100x100KB_LogRecords-4           43          24099056 ns/op        22979021 B/op       7741 allocs/op
BenchmarkLogsConvert_100x100KB_LogRecords-8           43          23765223 ns/op        22987070 B/op       7741 allocs/op
```

### Benchmark server

benchmark server 模拟 HTTP 数据上报。

| core | logCount/request | logLength/request | requests   | QPS          | elapsed       | sent batch | exporter |
| ---- |------------------|-------------------|-----|--------------|---------------|------------|----------|
| 8 | 10               | 1KB               | 10000 |     20283         | 493.66968ms   | 100            | gse-agent    |
| 8 | 10               | 10KB              | 10000 |     13495         | 741.037216ms  | 100         | gse-agent  |
| 8 | 10               | 100KB             | 10000 | 1881| 5.315255229s  | 100          | gse-agent  |
| 8 | 100              | 1KB               | 10000 | 1595| 6.266090726s  | 100          | gse-agent  |
| 8 | 100              | 10KB              | 10000 | 470| 21.247460949s | 100          | gse-agent  |
