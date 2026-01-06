module github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar

go 1.24.0

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/containerd/containerd v1.7.30
	github.com/containerd/typeurl v1.0.2
	github.com/docker/docker v27.1.1+incompatible
	github.com/go-logr/logr v1.4.2
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.27.4
	github.com/prometheus/client_golang v1.16.0
	github.com/stretchr/testify v1.8.4
	google.golang.org/grpc v1.59.0
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.26.2
	k8s.io/apimachinery v0.27.4
	k8s.io/client-go v0.26.2
	k8s.io/code-generator v0.21.2
	k8s.io/cri-api v0.27.1
	sigs.k8s.io/controller-runtime v0.9.2
)

require (
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/moby/term v0.0.0-20210619224110-3f7ff695adc6 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	golang.org/x/term v0.37.0 // indirect
)
