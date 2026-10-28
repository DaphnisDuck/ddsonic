// ddsonic: the SDK symbols gate, a module of its own so that its loader
// (golang.org/x/tools/go/packages) is in no graph that ddsonic builds. Only
// the sdkcontract runner runs it (docs/ddsonic/upstream.md).
module github.com/bjarneo/cliamp/tools/sdkgate

go 1.26.0

require golang.org/x/tools v0.50.0

require (
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
)
