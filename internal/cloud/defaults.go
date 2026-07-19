package cloud

// Default RustFS connection parameters. These are fixed at build time so that
// end users never see storage infrastructure details — the cloud drive simply
// works out of the box, like any consumer net-disk product.
const (
	DefaultEndpoint        = "http://127.0.0.1:9000"
	DefaultRegion          = "us-east-1"
	DefaultAccessKeyID     = "easyshare-dev"
	DefaultSecretAccessKey  = "replace-with-a-long-random-development-secret"
	DefaultBucket          = "easyshare"
	DefaultAllowInsecureHTTP = true
)
