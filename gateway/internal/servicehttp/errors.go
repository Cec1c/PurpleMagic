package servicehttp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"dnf90.local/gateway/internal/diagnostics"
)

// PeerError retains the underlying cause for errors.Is/As without exposing a
// URL, response body or driver message in logs or browser errors.
type PeerError struct {
	Kind            string
	Status          int
	RequestID, Code string
	Cause           error
}

func (e *PeerError) Error() string {
	return fmt.Sprintf("private GM peer %s (status %d)", e.Kind, e.Status)
}
func (e *PeerError) Unwrap() error { return e.Cause }
func (e *PeerError) DiagnosticFields() []any {
	return []any{"peer_kind", e.Kind, "peer_status", e.Status, "peer_request_id", e.RequestID, "peer_error_code", e.Code}
}

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func responseID(res *http.Response) string {
	id := res.Header.Get("X-Request-ID")
	b, e := hex.DecodeString(id)
	if e == nil && len(b) == 16 && len(id) == 32 {
		return id
	}
	return ""
}
func (c *Client) ReadResponse(ctx context.Context, res *http.Response) (raw []byte, err error) {
	done := diagnostics.Start(ctx, "peer.response_body")
	defer func() { done(err) }()
	defer res.Body.Close()
	fail := func(kind string, cause error) error {
		return &PeerError{Kind: kind, Status: res.StatusCode, RequestID: responseID(res), Cause: cause}
	}
	// Read only the bounded envelope on status failures. Messages are discarded.
	if res.StatusCode != http.StatusOK {
		e := fail("http_status", nil).(*PeerError)
		if code := res.Header.Get("X-PurpleMagic-Error-Code"); codePattern.MatchString(code) {
			e.Code = code
		}
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&envelope) == nil && codePattern.MatchString(envelope.Error.Code) {
			e.Code = envelope.Error.Code
		}
		return nil, e
	}
	if res.Header.Get("X-PurpleMagic-Protocol") != Version || res.Header.Get("X-PVF-SHA256") != c.Hash() {
		return nil, fail("generation_mismatch", nil)
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, MaxResponse+1))
	if err != nil {
		return nil, fail("response_read", err)
	}
	if len(raw) > MaxResponse {
		return nil, fail("response_limit", nil)
	}
	return raw, nil
}
