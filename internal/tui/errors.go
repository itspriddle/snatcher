package tui

import (
	"errors"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
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

// errorClass is one sentinel's presentation. appendErr puts the error's own
// text, sanitized, after long: a server error's text is the only thing that
// says what went wrong.
type errorClass struct {
	sentinel          error
	short, long, hint string
	appendErr         bool
}

// errorClasses are the sentinels with wording of their own, checked in order.
// Each asks something different of the user — fix credentials, upgrade the
// server, or wait — and Prowlarr's say which backend is asking, since the
// status bar can carry both at once.
var errorClasses = []errorClass{
	{
		sentinel: transmission.ErrAuth,
		short:    "auth failed",
		long:     "authentication failed: check the server's username and password",
		hint:     "check credentials with `snatcher server add`, then " + retryHint,
	},
	{
		sentinel: transmission.ErrUnsupported,
		short:    "unsupported server",
		long:     "unsupported server: snatcher needs Transmission 4.0 or newer",
		hint:     "point snatcher at Transmission 4.0 or newer, then " + retryHint,
	},
	{
		sentinel: transmission.ErrUnreachable,
		short:    "unreachable",
		long:     "server unreachable: check the URL and that Transmission is running",
		hint:     retryHint,
	},
	{
		sentinel:  transmission.ErrServer,
		short:     "server error",
		long:      "server error: ",
		hint:      retryHint,
		appendErr: true,
	},
	{
		sentinel: prowlarr.ErrAuth,
		short:    "auth failed",
		long:     "Prowlarr authentication failed: check the server's API key",
		hint:     "check the API key with `snatcher server add --type prowlarr`, then " + retryHint,
	},
	{
		sentinel: prowlarr.ErrUnreachable,
		short:    "unreachable",
		long:     "Prowlarr unreachable: check the URL and that Prowlarr is running",
		hint:     retryHint,
	},
	{
		sentinel:  prowlarr.ErrServer,
		short:     "server error",
		long:      "Prowlarr error: ",
		hint:      retryHint,
		appendErr: true,
	},
}

// classify decides how an error is presented: a sentinel's own wording, or
// anything else shown sanitized, since a server's error text is untrusted.
func classify(err error) errorText {
	if err == nil {
		return errorText{}
	}

	for _, c := range errorClasses {
		if !errors.Is(err, c.sentinel) {
			continue
		}

		long := c.long
		if c.appendErr {
			long += output.SanitizeTerminal(err.Error())
		}

		return errorText{short: c.short, long: long, hint: c.hint}
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
