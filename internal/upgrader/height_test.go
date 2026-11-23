package upgrader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCometRPC_LatestHeight(t *testing.T) {
	t.Run("reads latest_block_height from /status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/status", r.URL.Path)
			w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"result":{"sync_info":{"latest_block_height":"149192001","catching_up":false}}}`))
		}))
		defer server.Close()

		height, err := NewCometRPC(server.URL + "/").LatestHeight(context.Background())

		require.NoError(t, err)
		assert.Equal(t, int64(149192001), height)
	})

	t.Run("non 200 is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		_, err := NewCometRPC(server.URL).LatestHeight(context.Background())

		assert.ErrorContains(t, err, "unexpected status")
	})

	t.Run("unreachable node is an error", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()

		_, err := NewCometRPC(server.URL).LatestHeight(context.Background())

		assert.Error(t, err)
	})
}
