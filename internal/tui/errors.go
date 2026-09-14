package tui

import (
	"errors"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// retryHint is the tail every error hint ends with. The poll retries on its own,
// but R retries now, and a hint that did not say so would leave a user waiting.
const retryHint = "press R to retry"

// errorShortWidth bounds the status bar's text for an error that is not one of
// the sentinels: a server's raw error text can be a paragraph.
const errorShortWidth = 24

// errorText is the one presentation of a backend error, in the three sizes the
// UI draws it at: short for the status bar, long for a sentence in a pane or a
// toast, and a hint saying what to do about it.
type errorText struct {
	short, long, hint string
}

// classify decides how an error is presented. The sentinels each get their own
// wording, because each asks something different of the user — fix credentials,
// upgrade the server, or wait. Anything else is shown sanitized, since a
// server's error text is untrusted.
func classify(err error) errorText {
	switch {
	case err == nil:
		return errorText{}
	case errors.Is(err, transmission.ErrAuth):
		return errorText{
			short: "auth failed",
			long:  "authentication failed: check the server's username and password",
			hint:  "check credentials with `snatcher server add`, then " + retryHint,
		}
	case errors.Is(err, transmission.ErrUnsupported):
		return errorText{
			short: "unsupported server",
			long:  "unsupported server: snatcher needs Transmission 4.0 or newer",
			hint:  "point snatcher at Transmission 4.0 or newer, then " + retryHint,
		}
	case errors.Is(err, transmission.ErrUnreachable):
		return errorText{
			short: "unreachable",
			long:  "server unreachable: check the URL and that Transmission is running",
			hint:  retryHint,
		}
	case errors.Is(err, transmission.ErrServer):
		return errorText{
			short: "server error",
			long:  "server error: " + output.SanitizeTerminal(err.Error()),
			hint:  retryHint,
		}
	}

	text := output.SanitizeTerminal(err.Error())

	return errorText{short: truncate(text, errorShortWidth), long: text, hint: retryHint}
}

// errorLine is a backend error as one sentence a user can act on.
func errorLine(err error) string { return classify(err).long }

// errorShort is a backend error in the few cells the status bar can spare.
func errorShort(err error) string { return classify(err).short }

// errorHint is what to do about a backend error, drawn under it in a pane.
func errorHint(err error) string { return classify(err).hint }
