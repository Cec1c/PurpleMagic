// Package servicehttp authenticates the private GM service link. It carries no
// browser cookies and accepts only a configured peer and one PVF generation.
package servicehttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"dnf90.local/gateway/internal/diagnostics"
)

const Version = "purplemagic-service-v1"
const MaxResponse = 16 << 20

type identityKey struct{}

type Config struct {
	URL        string `json:"url"`
	Token      string `json:"token"`
	CAFile     string `json:"caFile,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	PVFSHA256  string `json:"pvfSha256"`
}

type Client struct {
	config Config
	http   *http.Client
}

func New(c Config) (*Client, error) {
	u, e := url.Parse(c.URL)
	hash, he := hex.DecodeString(c.PVFSHA256)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(c.Token) < 32 || he != nil || len(hash) != 32 || strings.ToLower(c.PVFSHA256) != c.PVFSHA256 {
		return nil, errors.New("invalid private GM peer configuration")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return nil, errors.New("private GM peer requires TLS or loopback tunnel")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.ServerName}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, errors.New("GM peer CA unavailable")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid GM peer CA")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	c.URL = strings.TrimRight(c.URL, "/")
	return &Client{c, &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Hash() string { return c.config.PVFSHA256 }
func (c *Client) Do(ctx context.Context, method, path string, body []byte, headers http.Header) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, errors.New("invalid service route")
	}
	r, e := http.NewRequestWithContext(ctx, method, c.config.URL+path, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	for _, k := range []string{"Content-Type", "Idempotency-Key"} {
		if v := headers.Get(k); v != "" {
			r.Header.Set(k, v)
		}
	}
	r.Header.Set("Authorization", "Bearer "+c.config.Token)
	r.Header.Set("X-PurpleMagic-Protocol", Version)
	r.Header.Set("X-PVF-SHA256", c.config.PVFSHA256)
	diagnostics.Headers(ctx, r.Header)
	done := diagnostics.Start(ctx, "peer.response_headers")
	res, err := c.http.Do(r)
	if err != nil {
		err = &PeerError{Kind: "transport", Cause: err}
	} else {
		diagnostics.Field(ctx, "peer_status", res.StatusCode)
		diagnostics.Field(ctx, "peer_request_id", responseID(res))
	}
	done(err)
	return res, err
}
func (c *Client) JSON(ctx context.Context, path string, in, out any) error {
	raw, e := json.Marshal(in)
	if e != nil {
		return e
	}
	h := http.Header{"Content-Type": []string{"application/json"}}
	res, e := c.Do(ctx, "POST", path, raw, h)
	if e != nil {
		return e
	}
	raw, e = c.ReadResponse(ctx, res)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(raw, out); e != nil {
		return &PeerError{Kind: "invalid_json", Status: res.StatusCode, RequestID: responseID(res), Cause: e}
	}
	return nil
}
func Authenticated(r *http.Request) bool { v, _ := r.Context().Value(identityKey{}).(bool); return v }
func Protect(token, hash string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if len(token) < 32 || subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 || r.Header.Get("X-PurpleMagic-Protocol") != Version || r.Header.Get("X-PVF-SHA256") != hash || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			diagnostics.Code(r.Context(), "private_auth_or_generation")
			w.Header().Set("X-PurpleMagic-Error-Code", "private_auth_or_generation")
			http.Error(w, "private service authentication or generation mismatch", http.StatusForbidden)
			return
		}
		w.Header().Set("X-PurpleMagic-Protocol", Version)
		w.Header().Set("X-PVF-SHA256", hash)
		diagnostics.TrustPeer(r.Context(), r.Header)
		diagnostics.ResponseHeaders(r.Context(), w.Header())
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, true)))
	})
}
