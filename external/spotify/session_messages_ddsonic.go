package spotify

// ddsonic: what the log and the footer say when a stored sign-in comes back
// without a Web API token. A refresh that failed for now (the network, an
// outage) is retried: the stored sign-in is kept, and `spotify reset`, which
// deletes it, would only make things worse. Only a sign-in with no refresh
// token to try needs signing in again.

import "github.com/bjarneo/cliamp/internal/appmeta"

// storedTokenMessage is the message for a stored sign-in that has no Web API
// token after its silent refresh, with refreshToken the token it holds.
func storedTokenMessage(refreshToken string) string {
	if refreshToken == "" {
		return "spotify: the stored sign-in cannot be renewed; sign in to Spotify again"
	}
	return "spotify: could not renew the Spotify sign-in just now (network or Spotify trouble); your sign-in is kept and " +
		appmeta.ClientName() + " tries again when it next needs Spotify, so try again in a moment"
}
