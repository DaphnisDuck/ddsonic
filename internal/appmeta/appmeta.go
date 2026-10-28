package appmeta

import (
	"strings"

	"github.com/bjarneo/cliamp/internal/appdir"
)

var (
	clientName = appdir.Name // ddsonic: identifies this fork to servers and MPRIS
	deviceName = appdir.Name
	version    = "dev"
)

func SetVersion(v string) {
	if v != "" {
		version = v
	}
}

func ClientName() string { return clientName }

// DisplayName is the name desktop media integrations show to people (the
// MPRIS Identity): the product's name. ddsonic.
func DisplayName() string { return appdir.Name }

// DesktopEntry is the name of the launcher entry ddsonic installs, without
// ".desktop" (packaging/linux): the MPRIS DesktopEntry, by which a desktop's
// media controls find the player's icon. ddsonic.
func DesktopEntry() string { return appdir.Name }

func DeviceName() string { return deviceName }

func Version() string { return version }

// CallbackPage is one of upstream's sign-in callback pages, titled with this
// player's name instead of cliamp's: the browser tab a user sees after
// signing in to Spotify or YouTube Music. ddsonic.
func CallbackPage(html string) string {
	return strings.Replace(html, "<title>cliamp</title>", "<title>"+clientName+"</title>", 1)
}

// projectURL is where a server's operator can find out what this client is.
// It is written out, not built from the name: a GitHub address is its own
// name, renamed in its own step.
const projectURL = "https://github.com/DaphnisDuck/ddsonic"

// ProjectURL is the project's home page, where releases are published.
func ProjectURL() string { return projectURL }

// UserAgent is how ddsonic names itself in the HTTP requests it makes, as
// "ddsonic/<version> (<project URL>)". It is the one place that string is
// built: every request that names its client calls this. ddsonic.
func UserAgent() string {
	return clientName + "/" + strings.TrimPrefix(version, "v") + " (" + projectURL + ")"
}
