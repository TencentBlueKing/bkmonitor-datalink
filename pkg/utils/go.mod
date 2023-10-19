module github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils

go 1.18

require (
	github.com/go-redis/redis/v8 v8.11.5
	github.com/hashicorp/consul/api v1.8.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/common v0.26.0
	github.com/spf13/afero v1.9.2
	github.com/stretchr/testify v1.8.3
	github.com/uptrace/opentelemetry-go-extra/otelzap v0.1.18
	github.com/xeipuuv/gojsonschema v1.2.0
	go.uber.org/zap v1.24.0
	golang.org/x/net v0.0.0-20210428140749-89ef3d95e781
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)

require (
	github.com/armon/go-metrics v0.0.0-20180917152333-f0300d1749da // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fatih/color v1.9.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.1 // indirect
	github.com/hashicorp/go-hclog v0.12.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.0.0 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/golang-lru v0.5.1 // indirect
	github.com/hashicorp/serf v0.9.5 // indirect
	github.com/mattn/go-colorable v0.1.6 // indirect
	github.com/mattn/go-isatty v0.0.12 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.1.2 // indirect
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/tinylib/msgp v1.1.6 // indirect
	github.com/uptrace/opentelemetry-go-extra/otelutil v0.1.18 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20180127040702-4e3ac2762d5f // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	go.opentelemetry.io/otel v1.11.2 // indirect
	go.opentelemetry.io/otel/trace v1.11.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/mod v0.4.1 // indirect
	golang.org/x/sys v0.7.0 // indirect
	golang.org/x/text v0.3.6 // indirect
	golang.org/x/tools v0.1.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils => ../utils
