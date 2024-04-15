module github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer

go 1.19

require (
	github.com/MauriceGit/skiplist v0.0.0-20181208093031-38aa714e3f14
	github.com/Shopify/sarama v1.27.0
	github.com/alicebob/miniredis/v2 v2.14.1
	github.com/asaskevich/EventBus v0.0.0-20180315140547-d46933a94f05
	github.com/bytedance/sonic v1.8.8
	github.com/cenkalti/backoff v2.0.0+incompatible
	github.com/cespare/xxhash v1.1.0
	github.com/cespare/xxhash/v2 v2.1.1
	github.com/cstockton/go-conv v0.0.0-20161128013909-4f5d7d0741da
	github.com/dghubble/sling v1.2.0
	github.com/elastic/go-elasticsearch/v5 v5.6.1
	github.com/elastic/go-elasticsearch/v6 v6.8.2
	github.com/elastic/go-elasticsearch/v7 v7.3.0
	github.com/emirpasic/gods v1.12.0
	github.com/go-redis/redis v6.15.1+incompatible
	github.com/go-redis/redis/v8 v8.8.3
	github.com/golang/mock v1.3.1
	github.com/google/go-cmp v0.5.5
	github.com/hashicorp/consul v1.4.1
	github.com/hashicorp/go-rootcerts v1.0.0
	github.com/hashicorp/go-version v1.2.0
	github.com/influxdata/influxdb v1.7.3
	github.com/jinzhu/copier v0.0.0-20190625015134-976e0346caa8
	github.com/jmespath/go-jmespath v0.0.0-20180206201540-c2b33e8439af
	github.com/mitchellh/mapstructure v1.1.2
	github.com/olekukonko/tablewriter v0.0.1
	github.com/oliveagle/jsonpath v0.0.0-20180606110733-2e52cf6e6852
	github.com/pkg/errors v0.8.1
	github.com/prashantv/gostub v1.1.0
	github.com/prometheus/client_golang v0.9.2
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475
	github.com/spf13/afero v1.2.0
	github.com/spf13/cobra v0.0.3
	github.com/spf13/pflag v1.0.3
	github.com/spf13/viper v1.3.1
	github.com/stretchr/testify v1.8.1
	go.etcd.io/bbolt v1.3.2
	go.uber.org/automaxprocs v1.5.1
	go.uber.org/zap v1.9.1
	golang.org/x/net v0.0.0-20220927171203-f486391704dc
	golang.org/x/sync v0.0.0-20220923202941-7f9b1623fab7
	golang.org/x/text v0.3.7
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
	gopkg.in/yaml.v2 v2.3.0
)

require (
	github.com/StackExchange/wmi v0.0.0-20190523213315-cbe66965904d // indirect
	github.com/alicebob/gopher-json v0.0.0-20200520072559-a9ecdc9d1d3a // indirect
	github.com/armon/circbuf v0.0.0-20190214190532-5111143e8da2 // indirect
	github.com/armon/go-metrics v0.0.0-20180917152333-f0300d1749da // indirect
	github.com/beorn7/perks v0.0.0-20180321164747-3a771d992973 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20221115062448-fe3a3abad311 // indirect
	github.com/coredns/coredns v1.5.0 // indirect
	github.com/cstockton/go-iter v0.0.0-20161124213939-353ca660c5db // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/eapache/go-resiliency v1.3.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20180814174437-776d5712da21 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/envoyproxy/go-control-plane v0.8.0 // indirect
	github.com/fsnotify/fsnotify v1.4.9 // indirect
	github.com/go-ole/go-ole v1.2.4 // indirect
	github.com/gogo/googleapis v1.2.0 // indirect
	github.com/golang/protobuf v1.4.2 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/google/gofuzz v1.0.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.1 // indirect
	github.com/hashicorp/go-discover v0.0.0-20190522154730-8aba54d36e17 // indirect
	github.com/hashicorp/go-immutable-radix v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v0.5.1 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/hashicorp/hil v0.0.0-20190212132231-97b3a9cdfa93 // indirect
	github.com/hashicorp/net-rpc-msgpackrpc v0.0.0-20151116020338-a14192a58a69 // indirect
	github.com/hashicorp/raft-boltdb v0.0.0-20171010151810-6e5ba93211ea // indirect
	github.com/hashicorp/serf v0.9.0 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/influxdata/platform v0.0.0-20181219193417-0f79e4ea3248 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jmespath/go-jmespath/internal/testify v1.5.1 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.15.11 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/magiconair/properties v1.8.0 // indirect
	github.com/mattn/go-runewidth v0.0.9 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/mitchellh/go-homedir v1.0.0 // indirect
	github.com/mitchellh/hashstructure v1.0.0 // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml v1.2.0 // indirect
	github.com/pierrec/lz4 v2.5.2+incompatible // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.0.0-20190129233127-fd36f4220a90 // indirect
	github.com/prometheus/common v0.2.0 // indirect
	github.com/prometheus/procfs v0.0.0-20190203183350-488faf799f86 // indirect
	github.com/shirou/gopsutil v2.18.12+incompatible // indirect
	github.com/shirou/w32 v0.0.0-20160930032740-bb4de0191aa4 // indirect
	github.com/spf13/cast v1.3.0 // indirect
	github.com/spf13/jwalterweatherman v1.0.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/yuin/gopher-lua v0.0.0-20200816102855-ee81675732da // indirect
	go.opentelemetry.io/otel v0.20.0 // indirect
	go.opentelemetry.io/otel/metric v0.20.0 // indirect
	go.opentelemetry.io/otel/trace v0.20.0 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.1.0 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/crypto v0.0.0-20220722155217-630584e8d5aa // indirect
	golang.org/x/sys v0.0.0-20220728004956-3c1f35247d10 // indirect
	google.golang.org/grpc v1.19.1 // indirect
	google.golang.org/protobuf v1.23.0 // indirect
	gopkg.in/jcmturner/aescts.v1 v1.0.1 // indirect
	gopkg.in/jcmturner/dnsutils.v1 v1.0.1 // indirect
	gopkg.in/jcmturner/gokrb5.v7 v7.5.0 // indirect
	gopkg.in/jcmturner/rpc.v1 v1.1.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// 背景：官方的 JMESPath SDK 不支持扩展自定义函数
// 有开发者对此提了PR，但官方并未同意支持该功能 (详见: https://github.com/jmespath/go-jmespath/issues/29)
// 因此基于 fork 了上述 Issue 提及的版本，以支持配置自定义函数
// 但由于之前项目已经有非常多代码引用了这个库，为了减少代码改动，此处直接做了 replace
// 引入时，还是写 import "github.com/jmespath/go-jmespath" , 但实际上是使用 "github.com/jayjiahua/go-jmespath" 这个版本
replace github.com/jmespath/go-jmespath => github.com/jayjiahua/go-jmespath v0.0.0-20211202132552-7e3a56e7a162
