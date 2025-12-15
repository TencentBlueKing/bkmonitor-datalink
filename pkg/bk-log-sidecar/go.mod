module github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar

go 1.16

require (
	github.com/Microsoft/go-winio v0.5.0
	github.com/containerd/containerd v1.7.27
	github.com/containerd/typeurl v1.0.2
	github.com/docker/docker v27.1.1+incompatible
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/go-logr/logr v0.4.0
	github.com/google/go-cmp v0.5.6 // indirect
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/moby/term v0.0.0-20210619224110-3f7ff695adc6 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/prometheus/client_golang v1.11.0
	github.com/stretchr/testify v1.7.0
	golang.org/x/term v0.0.0-20210615171337-6886f2dfbf5b // indirect
	google.golang.org/grpc v1.33.2
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v0.21.2
	k8s.io/code-generator v0.21.2
	k8s.io/cri-api v0.20.6
	sigs.k8s.io/controller-runtime v0.9.2
)
