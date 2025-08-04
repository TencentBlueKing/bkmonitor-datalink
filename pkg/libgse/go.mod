module github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse

go 1.23.0

require (
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils v0.3.0
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/containerd/cgroups v1.0.1
	github.com/elastic/beats v7.1.1+incompatible
	github.com/golang/protobuf v1.5.3
	github.com/natefinch/npipe v0.0.0-20160621034901-c1b8fa8bdcce
	github.com/nightlyone/lockfile v1.0.0
	github.com/opencontainers/runtime-spec v1.0.2
	github.com/peterbourgon/diskv v2.0.1+incompatible
	github.com/prometheus/client_golang v1.15.1
	github.com/prometheus/client_model v0.4.0
	github.com/prometheus/common v0.43.0
	github.com/prometheus/prometheus v0.43.0
	github.com/stretchr/testify v1.8.3
	go.opentelemetry.io/otel v1.16.0
	go.opentelemetry.io/otel/sdk v1.16.0
	go.opentelemetry.io/otel/trace v1.16.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/boltdb/bolt v1.3.1
	github.com/spf13/cast v1.3.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.16.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.16.0
	golang.org/x/time v0.3.0
)

require (
	github.com/Shopify/sarama v1.27.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v4 v4.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/eapache/go-resiliency v1.2.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20180814174437-776d5712da21 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/elastic/ecs v1.6.0 // indirect
	github.com/elastic/go-lumber v0.1.0 // indirect
	github.com/elastic/go-seccomp-bpf v1.1.0 // indirect
	github.com/elastic/go-structform v0.0.7 // indirect
	github.com/elastic/go-sysinfo v1.4.0 // indirect
	github.com/elastic/go-ucfg v0.7.0 // indirect
	github.com/elastic/go-windows v1.0.0 // indirect
	github.com/elastic/gosigar v0.11.0 // indirect
	github.com/fatih/color v1.14.1 // indirect
	github.com/garyburd/redigo v1.6.2 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/godbus/dbus/v5 v5.0.4 // indirect
	github.com/gofrs/uuid v4.3.0+incompatible // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/btree v1.0.0 // indirect
	github.com/grafana/regexp v0.0.0-20221122212121-6b5c0a4cb7fd // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.15.2 // indirect
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/jcmturner/gofork v1.0.0 // indirect
	github.com/joeshaw/multierror v0.0.0-20140124173710-69b34d4ec901 // indirect
	github.com/klauspost/compress v1.11.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/mitchellh/hashstructure v1.0.0 // indirect
	github.com/pierrec/lz4 v2.5.2+incompatible // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/procfs v0.9.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20200313005456-10cdbea86bc0 // indirect
	github.com/spf13/cobra v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	go.opentelemetry.io/otel/exporters/otlp/internal/retry v1.16.0 // indirect
	go.opentelemetry.io/otel/metric v1.16.0 // indirect
	go.opentelemetry.io/proto/otlp v0.19.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.24.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/exp v0.0.0-20230307190834-24139beb5833 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 // indirect
	google.golang.org/grpc v1.56.3 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
	gopkg.in/jcmturner/aescts.v1 v1.0.1 // indirect
	gopkg.in/jcmturner/dnsutils.v1 v1.0.1 // indirect
	gopkg.in/jcmturner/gokrb5.v7 v7.5.0 // indirect
	gopkg.in/jcmturner/rpc.v1 v1.1.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gopkg.in/natefinch/npipe.v2 v2.0.0-20160621034901-c1b8fa8bdcce // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	howett.net/plist v0.0.0-20181124034731-591f970eefbb // indirect
)

replace github.com/elastic/beats v7.1.1+incompatible => github.com/TencentBlueKing/beats v7.1.12-bk+incompatible
