module github.com/rocker-zhang/gpufleet-cli

go 1.26.0

require (
	github.com/rocker-zhang/gpufleet-proto/gen/go v0.1.0
	github.com/spf13/cobra v1.8.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.1 // indirect
)

// Poly-repo: in CI the proto gen module is consumed at its pinned tag
// (proto/v0.1.0). For local workspace builds this replace resolves it offline
// against the vendored REAL gen types — NOT a hand-rolled mirror. cli reads the
// agent over HTTP (TASK-0020 / TASK-0016), so it links NO sibling Go module
// other than the read-only proto contract: no agent, no rca, no semantics.
