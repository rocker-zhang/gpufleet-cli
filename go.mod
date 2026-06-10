module github.com/rocker-zhang/gpufleet-cli

go 1.23

require (
	github.com/rocker-zhang/gpufleet-agent v0.0.0
	github.com/rocker-zhang/gpufleet-rca v0.0.0-00010101000000-000000000000
	github.com/rocker-zhang/gpufleet-semantics v0.0.0
	github.com/spf13/cobra v1.8.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
)

// Poly-repo: in CI each dependency is consumed at a pinned tag. For local
// workspace builds these replace directives point at the sibling repos.
// Remove/override via a go.work when building against tagged releases.
replace github.com/rocker-zhang/gpufleet-semantics => ../semantics

replace github.com/rocker-zhang/gpufleet-agent => ../agent

replace github.com/rocker-zhang/gpufleet-rca => ../rca
