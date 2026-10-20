// Package httpx holds small HTTP helpers shared by the redirect service and
// the management API: the client's address behind proxies, and JSON in and
// out in the error format API.md defines.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the visitor's address. With trustedHops = 0 it is the
// TCP peer, and X-Forwarded-For is ignored, because anyone can send that
// header. Behind trustedHops reverse proxies (1 on Render), each proxy
// appends the address it saw, so the client is the entry trustedHops from
// the right; anything further left was written by the client and can't be
// trusted.
func ClientIP(r *http.Request, trustedHops int) netip.Addr {
	if trustedHops > 0 {
		var hops []string
		for _, h := range r.Header.Values("X-Forwarded-For") {
			for _, part := range strings.Split(h, ",") {
				if part = strings.TrimSpace(part); part != "" {
					hops = append(hops, part)
				}
			}
		}
		if len(hops) >= trustedHops {
			if ip, err := netip.ParseAddr(hops[len(hops)-trustedHops]); err == nil {
				return ip.Unmap()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}

// WriteJSON writes v as JSON with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v) // the client went away; nothing to do
	}
}

// ErrorBody is the error envelope from API.md.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the inside of the error envelope.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError writes {"error": {"code", "message"}} with status.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// MaxJSONBody bounds request bodies; link inputs are far smaller.
const MaxJSONBody = 64 << 10

// ErrBadRequest marks errors that DecodeJSON's caller should answer with 400.
var ErrBadRequest = errors.New("bad request")

// DecodeJSON reads one JSON object from r into dst. It insists on a JSON
// content type (a cross-site HTML form can't send one without a CORS
// preflight), refuses unknown fields so typos fail loudly, and bounds the
// body size.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", ErrBadRequest)
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return fmt.Errorf("%w: request body is larger than %d bytes", ErrBadRequest, MaxJSONBody)
		}
		return fmt.Errorf("%w: invalid JSON: %v", ErrBadRequest, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: request body must hold a single JSON object", ErrBadRequest)
	}
	return nil
}
