module github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/example/otmetrics

go 1.19

require (
	go.opentelemetry.io/contrib/instrumentation/runtime v0.33.0
	go.opentelemetry.io/otel v1.8.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric v0.31.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v0.31.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v0.31.0
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v0.31.0
	go.opentelemetry.io/otel/metric v0.31.0
	go.opentelemetry.io/otel/sdk v1.8.0
	go.opentelemetry.io/otel/sdk/metric v0.31.0
	golang.org/x/sys v0.0.0-20220209214540-3681064d5158 // indirect
)
