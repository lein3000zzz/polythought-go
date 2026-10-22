module github.com/lein3000zzz/polythought-go

go 1.27.0

// 1.27.2 fixes net/http, HTTP/2, crypto/tls and net/textproto vulnerabilities (R22);
// 1.27.3, crypto/x509's trust in compromised intermediates (GO-2026-6809).
toolchain go1.27.3

require (
	github.com/anthropics/anthropic-sdk-go v1.83.0
	github.com/cloudwego/hertz v0.10.6
	github.com/coder/websocket v1.8.15
	github.com/elastic/go-seccomp-bpf v1.6.0
	github.com/ethereum/go-ethereum v1.17.8
	github.com/evanw/esbuild v0.28.2
	github.com/go-jet/jet/v2 v2.16.1
	github.com/go-telegram/bot v1.27.0
	github.com/google/jsonschema-go v0.4.3
	github.com/google/uuid v1.6.0
	github.com/hashicorp/go-hclog v1.6.3
	github.com/hashicorp/go-plugin v1.8.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/jedisct1/go-minisign v0.0.0-20260527172527-a09352b57a22
	github.com/knadh/koanf/parsers/yaml v1.1.1
	github.com/knadh/koanf/providers/confmap v1.0.1
	github.com/knadh/koanf/providers/rawbytes v1.0.1
	github.com/knadh/koanf/v2 v2.3.8
	github.com/landlock-lsm/go-landlock v0.10.1
	github.com/lein3000zzz/the-watchers v0.0.0-20260728160212-a9a1ca05a9eb
	github.com/openai/openai-go/v3 v3.82.0
	github.com/pressly/goose/v3 v3.28.0
	github.com/shopspring/decimal v1.5.0
	github.com/stretchr/testify v1.12.1
	github.com/testcontainers/testcontainers-go v0.44.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0
	github.com/tetratelabs/wazero v1.12.0
	github.com/wneessen/go-mail v0.8.1
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
	golang.org/x/crypto v0.57.0
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
	golang.org/x/time v0.16.0
	google.golang.org/genai v1.75.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.18.2 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	dario.cat/mergo v1.0.2 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.20.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/cloudwego/gopkg v0.2.0 // indirect
	github.com/cloudwego/netpoll v0.7.5 // indirect
	github.com/consensys/gnark-crypto v0.18.1 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v0.2.1 // indirect
	github.com/cpuguy83/dockercfg v0.3.2 // indirect
	github.com/crate-crypto/go-eth-kzg v1.5.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/go-connections v0.8.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/ethereum/c-kzg-4844/v2 v2.1.8 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.11 // indirect
	github.com/googleapis/gax-go/v2 v2.17.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/lib/pq v1.12.3 // indirect
	github.com/lufia/plan9stats v0.0.0-20260330125221-c963978e514e // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/go-archive v0.3.0 // indirect
	github.com/moby/moby/api v1.55.0 // indirect
	github.com/moby/moby/client v0.5.1 // indirect
	github.com/moby/patternmatcher v0.6.1 // indirect
	github.com/moby/sys/sequential v0.7.0 // indirect
	github.com/moby/sys/user v0.4.1 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/shirou/gopsutil/v4 v4.26.6 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/supranational/blst v0.3.16 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260831171406-18b4a7587f8a // indirect
	kernel.org/pub/linux/libs/security/libcap/psx v1.2.77 // indirect
)
