package torrent

import (
	"fmt"
	"strconv"
	"strings"
)

// Selector is a vendor-neutral selection of torrents for the mutation commands
// (start, stop, and later remove/verify). It captures the three selection forms
// the CLI supports: numeric per-session IDs, 40-character SHA1 info-hashes, and
// an "all torrents" flag. Build one with ParseSelector and feed RPCIDs() to the
// backend so every write command resolves positional arguments through one
// tested helper.
type Selector struct {
	// IDs are the backend's numeric per-session identifiers.
	IDs []int64

	// Hashes are 40-character SHA1 info-hash strings.
	Hashes []string

	// All targets every torrent on the server. When set, IDs and Hashes are
	// empty and RPCIDs returns nil so the RPC "ids" argument is omitted.
	All bool
}

// hashLength is the length of a SHA1 info-hash string (40 hex characters).
// Transmission's hashString. v2/hybrid 64-char hashes are out of scope.
const hashLength = 40

// ParseSelector classifies positional torrent arguments into a Selector and
// enforces the selection rules shared by every mutation command. Each token is
// an ID when it is all digits and fits an int64, a hash when it is a
// 40-character hex string, and an error otherwise (naming the offending token).
//
// One of args or all is required: a bare invocation with neither errors rather
// than silently targeting everything, and combining positional args with all is
// rejected.
func ParseSelector(args []string, all bool) (Selector, error) {
	if all {
		if len(args) > 0 {
			return Selector{}, fmt.Errorf("cannot combine torrent selectors with --all")
		}

		return Selector{All: true}, nil
	}

	if len(args) == 0 {
		return Selector{}, fmt.Errorf("specify torrent ids or hashes, or use --all")
	}

	var sel Selector
	for _, arg := range args {
		switch {
		case isAllDigits(arg):
			id, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				return Selector{}, fmt.Errorf("invalid torrent selector %q: %w", arg, err)
			}
			sel.IDs = append(sel.IDs, id)
		case isInfoHash(arg):
			sel.Hashes = append(sel.Hashes, strings.ToLower(arg))
		default:
			return Selector{}, fmt.Errorf("invalid torrent selector %q", arg)
		}
	}

	return sel, nil
}

// RPCIDs returns the value for the RPC "ids" argument: nil when All is set (so
// the param is omitted and the server targets every torrent), otherwise a slice
// mixing the int64 IDs and the hash strings, IDs first then hashes.
func (s Selector) RPCIDs() []any {
	if s.All {
		return nil
	}

	ids := make([]any, 0, len(s.IDs)+len(s.Hashes))
	for _, id := range s.IDs {
		ids = append(ids, id)
	}
	for _, hash := range s.Hashes {
		ids = append(ids, hash)
	}

	return ids
}

// isAllDigits reports whether s is a non-empty string of ASCII digits. It
// classifies a token as a numeric ID candidate; the actual int64 range check is
// left to strconv.ParseInt so overflow surfaces as an error.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// isInfoHash reports whether s is a 40-character hexadecimal SHA1 info-hash. A
// hex string of any other length is not treated as a hash.
func isInfoHash(s string) bool {
	if len(s) != hashLength {
		return false
	}

	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}

	return true
}
