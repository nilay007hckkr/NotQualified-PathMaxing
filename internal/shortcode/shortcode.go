// Package shortcode generates random short codes and checks custom aliases.
//
// Codes are compared without regard to case (a unique index on lower(code)
// enforces it), so "Sale" and "sale" are one link. Generated codes are
// lowercase, so they read the same however someone types them back in.
package shortcode

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// Length is the length of a generated code. 36^8 is about 2.8 trillion
// codes, so random collisions are rare, and the store retries on the
// unique index when one does happen.
const Length = 8

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// Generate returns a random code of Length lowercase letters and digits.
// Generated codes can never be reserved words: the longest reserved word is
// shorter than Length.
func Generate() string {
	var out [Length]byte
	var buf [32]byte
	n := 0
	for n < Length {
		if _, err := rand.Read(buf[:]); err != nil {
			panic(fmt.Sprintf("shortcode: crypto/rand failed: %v", err))
		}
		for _, b := range buf {
			// 252 is the largest multiple of 36 below 256; rejecting bytes
			// above it keeps every character equally likely.
			if b >= 252 {
				continue
			}
			out[n] = alphabet[b%36]
			n++
			if n == Length {
				break
			}
		}
	}
	return string(out[:])
}

// Alias limits. An alias may use letters (either case), digits, "-" and
// "_", and must start and end with a letter or digit.
const (
	MinAliasLen = 3
	MaxAliasLen = 50
)

// Error codes from API.md.
const (
	CodeAliasInvalid  = "ALIAS_INVALID"
	CodeAliasReserved = "ALIAS_RESERVED"
)

// reserved are the first path segments the server uses itself. A link with
// one of these codes could never be reached, or would hide a real route.
var reserved = map[string]bool{
	"api": true, "app": true, "p": true, "bio": true, "admin": true,
	"login": true, "logout": true, "signup": true, "static": true,
	"assets": true, "health": true, "healthz": true,
}

// Reserved returns the reserved words in alphabetical order, for API docs
// and error messages.
func Reserved() []string {
	return []string{"admin", "api", "app", "assets", "bio", "health", "healthz", "login", "logout", "p", "signup", "static"}
}

// IsReserved reports whether code is reserved, ignoring case.
func IsReserved(code string) bool { return reserved[strings.ToLower(code)] }

// CheckAlias returns an API error code and message when alias can't be used,
// or two empty strings when it can. Whether the alias is already taken is the
// store's question, answered by the unique index at insert time.
func CheckAlias(alias string) (code, message string) {
	if len(alias) < MinAliasLen || len(alias) > MaxAliasLen {
		return CodeAliasInvalid, fmt.Sprintf("alias must be %d to %d characters long", MinAliasLen, MaxAliasLen)
	}
	for i := 0; i < len(alias); i++ {
		c := alias[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		edge := i == 0 || i == len(alias)-1
		switch {
		case alnum:
		case (c == '-' || c == '_') && !edge:
		case c == '-' || c == '_':
			return CodeAliasInvalid, "alias must start and end with a letter or digit"
		default:
			return CodeAliasInvalid, "alias may only use letters, digits, - and _"
		}
	}
	if IsReserved(alias) {
		return CodeAliasReserved, fmt.Sprintf("%q is reserved by PathMaxing; choose another alias", strings.ToLower(alias))
	}
	return "", ""
}
