go 1.19

module github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector

require (
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse v0.0.0-00010101000000-000000000000
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils v0.0.0-00010101000000-000000000000
	github.com/apache/thrift v0.16.0
	github.com/buraksezer/consistent v0.10.0
	github.com/bytedance/sonic v1.10.2
	github.com/cespare/xxhash/v2 v2.1.2
	github.com/elastic/beats v7.1.1+incompatible
	github.com/elastic/go-ucfg v0.7.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang/protobuf v1.5.2
	github.com/golang/snappy v0.0.4
	github.com/google/uuid v1.3.0
	github.com/gorilla/mux v1.8.0
	github.com/jaegertracing/jaeger v1.34.1
	github.com/liuwenping/go-fastping v0.0.0-20201022032547-03cfc2cff8ee
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369
	github.com/mitchellh/mapstructure v1.5.0
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/jaeger v0.52.0
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/zipkin v0.52.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.2
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.37.0
	github.com/rs/cors v1.8.2
	github.com/spf13/cast v1.5.0
	github.com/stretchr/testify v1.8.3
	github.com/syndtr/goleveldb v1.0.0
	github.com/tinylib/msgp v1.1.6
	go.opentelemetry.io/collector/pdata v0.52.0
	go.opentelemetry.io/collector/semconv v0.52.0
	go.uber.org/automaxprocs v1.5.1
	google.golang.org/genproto v0.0.0-20221118155620-16455021b5e6
	google.golang.org/grpc v1.52.0
	google.golang.org/protobuf v1.29.0
	k8s.io/client-go v0.24.2
	lukechampine.com/frand v1.4.2
	skywalking.apache.org/repo/goapi v0.0.0-20230314034821-0c5a44bb767a
)

require (
	github.com/Shopify/sarama v1.32.0 // indirect
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da // indirect
	github.com/armon/go-metrics v0.3.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/census-instrumentation/opencensus-proto v0.3.0 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20230717121745-296ad89f973d // indirect
	github.com/chenzhuoyu/iasm v0.9.0 // indirect
	github.com/containerd/cgroups v1.0.1 // indirect
	github.com/coreos/go-systemd/v22 v22.1.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/eapache/go-resiliency v1.2.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20180814174437-776d5712da21 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/elastic/ecs v1.6.0 // indirect
	github.com/elastic/go-lumber v0.1.0 // indirect
	github.com/elastic/go-seccomp-bpf v1.1.0 // indirect
	github.com/elastic/go-structform v0.0.7 // indirect
	github.com/elastic/go-sysinfo v1.4.0 // indirect
	github.com/elastic/go-windows v1.0.0 // indirect
	github.com/elastic/gosigar v0.11.0 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/garyburd/redigo v1.6.2 // indirect
	github.com/godbus/dbus/v5 v5.0.3 // indirect
	github.com/gofrs/uuid v4.3.0+incompatible // indirect
	github.com/gogo/googleapis v1.4.1 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/grafana/regexp v0.0.0-20220304095617-2e8d9baf4ac2 // indirect
	github.com/hashicorp/consul/api v1.13.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.2.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.2.0 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/serf v0.9.6 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.0.0 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.2 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/joeshaw/multierror v0.0.0-20140124173710-69b34d4ec901 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.14.4 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/hashstructure v1.0.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/natefinch/npipe v0.0.0-20160621034901-c1b8fa8bdcce // indirect
	github.com/nightlyone/lockfile v1.0.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal v0.52.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/opencensus v0.52.0 // indirect
	github.com/opencontainers/runtime-spec v1.0.2 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/openzipkin/zipkin-go v0.4.0 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/procfs v0.7.4-0.20211011103944-1a7a2bd3279f // indirect
	github.com/prometheus/prometheus v0.37.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/spf13/afero v1.9.2 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/uber/jaeger-client-go v2.30.0+incompatible // indirect
	github.com/uber/jaeger-lib v2.4.1+incompatible // indirect
	go.opencensus.io v0.23.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.24.0 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/crypto v0.0.0-20220511200225-c6db032c6c88 // indirect
	golang.org/x/net v0.6.0 // indirect
	golang.org/x/sys v0.7.0 // indirect
	golang.org/x/text v0.7.0 // indirect
	golang.org/x/time v0.0.0-20220609170525-579cf78fd858 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v0.0.0-20181124034731-591f970eefbb // indirect
	k8s.io/utils v0.0.0-20220210201930-3a6ce19ff2f9 // indirect
)

replace github.com/elastic/beats v7.1.1+incompatible => github.com/TencentBlueKing/beats v7.1.15-bk+incompatible

replace github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils => ../utils

replace github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse => ../libgse
