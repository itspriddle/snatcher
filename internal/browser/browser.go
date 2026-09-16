// Package browser opens a URL in the user's browser through the OS's own opener.
//
// It shells out to open(1) or xdg-open(1) and reads no environment of its own;
// whether to use it at all — not over SSH, where it would open a browser on the
// remote machine — is the launcher's decision. It mirrors internal/clipboard.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

// openTimeout bounds one open, so a wedged opener cannot hold the TUI's command
// forever. The openers return as soon as they have handed the URL off.
const openTimeout = 3 * time.Second

// ErrScheme is returned for a URL that is not http or https. The URL is
// server-supplied, and an opener would happily launch a file: or a custom-scheme
// handler.
var ErrScheme = errors.New("browser: only http and https URLs can be opened")

// Opener opens URLs through a native tool.
type Opener struct {
	name string
	path string
}

// candidates lists the openers to try for goos, in order of preference.
func candidates(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"open"}
	case "linux":
		return []string{"xdg-open"}
	}

	return nil
}

// Detect finds the opener for this OS on PATH, and returns nil when there is
// none.
func Detect() *Opener {
	for _, name := range candidates(runtime.GOOS) {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}

		return &Opener{name: name, path: path}
	}

	return nil
}

// checkURL refuses anything but an absolute http or https URL. The URL rides
// argv, so a value starting with "-" is refused too: a parsed http URL cannot
// start with one, which is the check.
func checkURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrScheme
	}

	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ErrScheme
	}

	return nil
}

// Open opens rawURL. The URL never appears in the returned error, since it may
// carry a credential the caller has not redacted.
//
// Stdout and Stderr are left nil, so the child gets /dev/null and never writes
// under the TUI's alt screen.
func (o *Opener) Open(ctx context.Context, rawURL string) error {
	if err := checkURL(rawURL); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()

	if err := exec.CommandContext(ctx, o.path, rawURL).Run(); err != nil {
		return fmt.Errorf("%s: %w", o.name, err)
	}

	return nil
}
