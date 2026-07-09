package fetchx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runabol/tork/internal/fetchx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	host := "127.0.0.1"
	body, err := fetchx.NewClient(fetchx.WithAllowHosts(host)).Get(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
}

func TestGetBlocksLoopback(t *testing.T) {
	_, err := fetchx.NewClient().Get(context.Background(), "http://127.0.0.1/")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "blocked IP"))
}

func TestGetBlocksMetadataIP(t *testing.T) {
	_, err := fetchx.NewClient().Get(context.Background(), "http://169.254.169.254/latest/meta-data/")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "blocked IP"))
}

func TestGetBlocksPrivateHostname(t *testing.T) {
	_, err := fetchx.NewClient().Get(context.Background(), "http://localhost/")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "blocked IP"))
}

func TestGetRetriesThenSucceeds(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	host := "127.0.0.1"
	body, err := fetchx.NewClient(fetchx.WithMaxAttempts(3), fetchx.WithAllowHosts(host)).Get(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, 2, hits)
}

func TestGetAllowHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal"))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	_, err := fetchx.NewClient(fetchx.WithAllowHosts(host)).Get(context.Background(), srv.URL)
	require.NoError(t, err)
}

func TestGetRejectsFileScheme(t *testing.T) {
	_, err := fetchx.NewClient().Get(context.Background(), "file:///etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported scheme")
}
