go 1.23.0

module github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector

require (
	connectrpc.com/connect v1.16.2
	github.com/TarsCloud/TarsGo v1.4.5
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse v1.7.0
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator v1.0.0
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils v0.3.0
	github.com/apache/thrift v0.16.0
	github.com/bytedance/sonic v1.13.3
	github.com/cespare/xxhash/v2 v2.2.0
	github.com/elastic/beats v7.1.1+incompatible
	github.com/elastic/go-ucfg v0.7.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang/protobuf v1.5.4
	github.com/golang/snappy v0.0.4
	github.com/google/pprof v0.0.0-20230912144702-c363fe2c2ed8
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.0
	github.com/grafana/jfr-parser v0.11.0
	github.com/jaegertracing/jaeger v1.34.1
	github.com/klauspost/pgzip v1.2.6
	github.com/liuwenping/go-fastping v0.0.0-20201022032547-03cfc2cff8ee
	github.com/matttproud/golang_protobuf_extensions v1.0.4
	github.com/mitchellh/mapstructure v1.5.0
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/jaeger v0.52.0
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/zipkin v0.52.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/planetscale/vtprotobuf v0.6.0
	github.com/prometheus-operator/prometheus-operator v0.59.1
	github.com/prometheus/client_golang v1.16.0
	github.com/prometheus/client_model v0.4.0
	github.com/prometheus/common v0.44.0
	github.com/prometheus/prometheus v1.99.0
	github.com/rs/cors v1.8.2
	github.com/spf13/cast v1.5.0
	github.com/stretchr/testify v1.9.0
	github.com/tinylib/msgp v1.1.6
	go.opentelemetry.io/collector/pdata v0.52.0
	go.opentelemetry.io/collector/semconv v0.52.0
	go.uber.org/atomic v1.11.0
	go.uber.org/automaxprocs v1.5.2
	golang.org/x/exp v0.0.0-20231226003508-02704c960a9b
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240227224415-6ceb2ff114de
	google.golang.org/grpc v1.63.1
	google.golang.org/protobuf v1.33.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.27.3
	k8s.io/apimachinery v0.27.3
	k8s.io/client-go v0.27.3
	lukechampine.com/frand v1.4.2
	skywalking.apache.org/repo/goapi v0.0.0-20230314034821-0c5a44bb767a
)

require (
	github.com/Shopify/sarama v1.32.0 // indirect
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da // indirect
	github.com/alecthomas/units v0.0.0-20211218093645-b94a6e3cc137 // indirect
	github.com/armon/go-metrics v0.3.10 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/bytedance/sonic/loader v0.2.4 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/cenkalti/backoff/v4 v4.2.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cloudwego/base64x v0.1.5 // indirect
	github.com/containerd/cgroups v1.0.3 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/docker/distribution v2.8.2+incompatible // indirect
	github.com/docker/go-units v0.5.0 // indirect
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
	github.com/emicklei/go-restful/v3 v3.10.2 // indirect
	github.com/fatih/color v1.15.0 // indirect
	github.com/garyburd/redigo v1.6.2 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.20.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.4 // indirect
	github.com/godbus/dbus/v5 v5.0.6 // indirect
	github.com/gofrs/uuid v4.3.1+incompatible // indirect
	github.com/gogo/googleapis v1.4.1 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/grafana/regexp v0.0.0-20221122212121-6b5c0a4cb7fd // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.16.0 // indirect
	github.com/hashicorp/consul/api v1.14.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.2.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/hashicorp/golang-lru v0.6.0 // indirect
	github.com/hashicorp/serf v0.9.7 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.0.0 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.2 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/joeshaw/multierror v0.0.0-20140124173710-69b34d4ec901 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/hashstructure v1.1.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/natefinch/npipe v0.0.0-20160621034901-c1b8fa8bdcce // indirect
	github.com/nightlyone/lockfile v1.0.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal v0.52.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/opencensus v0.52.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/runtime-spec v1.0.3-0.20210326190908-1c3f411f0417 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/openzipkin/zipkin-go v0.4.0 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.59.1 // indirect
	github.com/prometheus-operator/prometheus-operator/pkg/client v0.59.1 // indirect
	github.com/prometheus/procfs v0.11.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/spf13/afero v1.9.2 // indirect
	github.com/spf13/cobra v1.4.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/uber/jaeger-client-go v2.30.0+incompatible // indirect
	github.com/uber/jaeger-lib v2.4.1+incompatible // indirect
	go.opencensus.io v0.23.0 // indirect
	go.opentelemetry.io/otel v1.16.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/internal/retry v1.16.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.16.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.16.0 // indirect
	go.opentelemetry.io/otel/metric v1.16.0 // indirect
	go.opentelemetry.io/otel/sdk v1.16.0 // indirect
	go.opentelemetry.io/otel/trace v1.16.0 // indirect
	go.opentelemetry.io/proto/otlp v1.0.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.24.0 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/oauth2 v0.27.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/term v0.30.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240227224415-6ceb2ff114de // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v0.0.0-20181124034731-591f970eefbb // indirect
	k8s.io/apiextensions-apiserver v0.25.0 // indirect
	k8s.io/component-base v0.25.0 // indirect
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/kube-openapi v0.0.0-20230525220651-2546d827e515 // indirect
	k8s.io/utils v0.0.0-20230711102312-30195339c3c7 // indirect
	sigs.k8s.io/controller-runtime v0.12.3 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.3.0 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

replace (
	github.com/elastic/beats v7.1.1+incompatible => github.com/TencentBlueKing/beats v7.1.15-bk+incompatible
	// A replace directive is needed for github.com/prometheus/prometheus to ensure running against the latest version of prometheus.
	github.com/prometheus/prometheus => github.com/prometheus/prometheus v0.38.0
)
