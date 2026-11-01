package resolve

import (
	"net/http"
	"net/url"
)

// filterRemoteEntries keeps the entries of a network-fetched playlist that
// lead to an http or https URL, rewriting each kept entry's path to that URL.
//
// The entry strings in a remote M3U or PLS are chosen by whoever serves it,
// not by the user, and they become track paths that playback dispatches on.
// An ssh:// entry reaches exec.Command("ssh", ...) against a server-named
// host, and a path with no scheme would be opened as a local file. Fetching a
// playlist should never let its author pick which program cliamp runs or
// which local file it reads.
//
// Every entry is parsed as a URL reference. One with no scheme is resolved
// against base, the URL the playlist was finally fetched from (after
// redirects), as a browser would; base must itself be an http or https URL
// with a host. An entry is kept only when the result is a hierarchical http or
// https URL with a host, and the kept path is the parsed URL in normalized
// form, so the scheme is lowercase and playback recognizes it as a stream.
// Everything else is dropped: other schemes, opaque forms such as
// "http:a.mp3", hostless ones such as "http:/a.mp3", malformed references,
// and a Windows drive path such as "C:\music\a.mp3", which names a file on
// the author's machine.
func filterRemoteEntries[E any](base *url.URL, entries []E, path func(*E) *string) []E {
	filtered := make([]E, 0, len(entries))
	for _, e := range entries {
		p := path(&e)
		if u, ok := remoteEntryURL(base, *p); ok {
			*p = u
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// remoteEntryURL returns the normalized http or https URL a remote playlist
// entry leads to, and false when it leads anywhere else.
func remoteEntryURL(base *url.URL, entry string) (string, bool) {
	ref, err := url.Parse(entry)
	if err != nil {
		return "", false
	}
	u := ref
	if ref.Scheme == "" {
		if !isWebURL(base) {
			return "", false
		}
		u = base.ResolveReference(ref)
	}
	if !isWebURL(u) {
		return "", false
	}
	return u.String(), true
}

// isWebURL reports whether u is a hierarchical http or https URL with a host.
func isWebURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") &&
		u.Opaque == "" && u.Hostname() != ""
}

// responseURL is the URL a response was finally served from, after any
// redirects, or nil when the response does not record its request.
func responseURL(resp *http.Response) *url.URL {
	if resp.Request == nil {
		return nil
	}
	return resp.Request.URL
}
