// Package fetchx provides an HTTP client that blocks private/reserved IPs at dial time
// (post-DNS) and retries transient failures with jittered backoff.
package fetchx

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 15 * time.Second

// Client fetches URLs with SSRF protections.
type Client struct {
	http        *http.Client
	maxAttempts int
	allowHosts  map[string]struct{}
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.http.Timeout = d
	}
}

// WithMaxAttempts sets retry count (minimum 1).
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxAttempts = n
		}
	}
}

// WithAllowHosts permits fetching these hostnames even when they resolve to private IPs
// (e.g. deliberate internal service calls).
func WithAllowHosts(hosts ...string) Option {
	return func(c *Client) {
		for _, h := range hosts {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" {
				c.allowHosts[h] = struct{}{}
			}
		}
	}
}

// NewClient builds a fetch client. Defaults: 15s timeout, 3 attempts.
func NewClient(opts ...Option) *Client {
	c := &Client{
		http: &http.Client{
			Timeout: defaultTimeout,
			Transport: safeTransport(map[string]struct{}{}),
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("fetchx: too many redirects")
				}
				return nil
			},
		},
		maxAttempts: 3,
		allowHosts:  make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.http.Transport = safeTransport(c.allowHosts)
	return c
}

func safeTransport(allowHosts map[string]struct{}) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if _, ok := allowHosts[strings.ToLower(host)]; !ok {
				if err := checkResolvedIPs(ctx, host); err != nil {
					return nil, err
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
		MaxIdleConns:        8,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

func checkResolvedIPs(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("fetchx: blocked IP %s", ip)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("fetchx: dns lookup %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("fetchx: no addresses for %s", host)
	}
	for _, ipa := range ips {
		if blockedIP(ipa.IP) {
			return fmt.Errorf("fetchx: blocked IP %s for host %s", ipa.IP, host)
		}
	}
	return nil
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	// Unique local (fc00::/7) and metadata-ish ranges.
	if ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT 100.64.0.0/10
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
	}
	return false
}

// Get performs an HTTP GET with retries. Only http/https schemes are allowed.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("fetchx: unsupported scheme %q", u.Scheme)
	}
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		if attempt > 0 {
			jitter := time.Duration(rand.Int63n(int64(500*time.Millisecond))) + time.Duration(attempt)*500*time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(jitter):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
			continue
		}
		if readErr != nil {
			lastErr = readErr
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("fetchx: after %d attempts: %w", c.maxAttempts, lastErr)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
