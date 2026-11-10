//go:build ddsonic_testpool

package ytmusic

// ddsonic: a build for tests only (-tags ddsonic_testpool) with a built-in
// credential pool, as upstream ships one and ddsonic does not, so that the
// paths those credentials open are tested on the executable (#82). The
// values are placeholders that sign nobody in.
func init() {
	fallbackCredentials = []oauthCreds{{ClientID: "test-only-id", ClientSecret: "test-only-secret"}}
}
