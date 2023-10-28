module github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker

go 1.19

require (
	github.com/IBM/sarama v1.41.2
	github.com/RedisBloom/redisbloom-go v1.0.0
	github.com/agiledragon/gomonkey/v2 v2.10.1
	github.com/ahmetb/go-linq/v3 v3.2.0
	github.com/alicebob/miniredis v2.5.0+incompatible
	github.com/bytedance/sonic v1.9.1
	github.com/cespare/xxhash/v2 v2.2.0
	github.com/deckarep/golang-set v1.8.0
	github.com/deckarep/golang-set/v2 v2.3.1
	github.com/docker/docker v24.0.6+incompatible
	github.com/elastic/go-elasticsearch/v5 v5.6.1
	github.com/elastic/go-elasticsearch/v6 v6.8.10
	github.com/elastic/go-elasticsearch/v7 v7.17.10
	github.com/gin-contrib/pprof v1.4.0
	github.com/gin-gonic/gin v1.9.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/golang/protobuf v1.5.3
	github.com/gomodule/redigo v1.8.9
	github.com/google/uuid v1.2.0
	github.com/grafana/pyroscope-go v1.0.4
	github.com/hashicorp/consul/api v1.20.0
	github.com/jinzhu/gorm v1.9.16
	github.com/json-iterator/go v1.1.12
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cast v1.5.1
	github.com/spf13/cobra v1.7.0
	github.com/spf13/viper v1.16.0
	github.com/stretchr/testify v1.8.4
	github.com/tylertreat/BoomFilters v0.0.0-20210315201527-1a82519a3e43
	github.com/valyala/fastjson v1.6.4
	github.com/xdg-go/scram v1.1.2
	go.etcd.io/bbolt v1.3.7
	go.uber.org/automaxprocs v1.5.3
	go.uber.org/zap v1.24.0
	golang.org/x/exp v0.0.0-20230905200255-921286631fa9
	google.golang.org/protobuf v1.31.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/BurntSushi/toml v1.3.2 // indirect
	github.com/alicebob/gopher-json v0.0.0-20230218143504-906a9b012302 // indirect
	github.com/armon/go-metrics v0.4.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20221115062448-fe3a3abad311 // indirect
	github.com/d4l3k/messagediff v1.2.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/eapache/go-resiliency v1.4.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.2 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.14.0 // indirect
	github.com/go-sql-driver/mysql v1.5.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/grafana/pyroscope-go/godeltaprof v0.1.4 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.2.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/klauspost/cpuid/v2 v2.2.4 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.3 // indirect
	github.com/leodido/go-urn v1.2.4 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/onsi/gomega v1.25.0 // indirect
	github.com/pelletier/go-toml/v2 v2.0.8 // indirect
	github.com/pierrec/lz4/v4 v4.1.18 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.26.0 // indirect
	github.com/prometheus/procfs v0.1.3 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/spf13/afero v1.9.5 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/subosito/gotenv v1.4.2 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.11 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/yuin/gopher-lua v1.1.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/arch v0.3.0 // indirect
	golang.org/x/crypto v0.13.0 // indirect
	golang.org/x/net v0.15.0 // indirect
	golang.org/x/sys v0.12.0 // indirect
	golang.org/x/text v0.13.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gotest.tools/v3 v3.5.0 // indirect
)

require (
	github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils v0.0.0-00010101000000-000000000000
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
)

replace github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils => ../utils
