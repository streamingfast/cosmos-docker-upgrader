package upgrader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HeightSource reports the latest block height the node has committed. It is
// used to tell a live upgrade signal from one left over by an upgrade that was
// applied out of band.
type HeightSource interface {
	LatestHeight(ctx context.Context) (int64, error)
}

// CometRPC queries the CometBFT RPC `/status` endpoint of the node.
type CometRPC struct {
	URL    string
	Client *http.Client
}

func NewCometRPC(url string) *CometRPC {
	return &CometRPC{
		URL:    strings.TrimSuffix(url, "/"),
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *CometRPC) LatestHeight(ctx context.Context) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/status", nil)
	if err != nil {
		return 0, fmt.Errorf("new request: %w", err)
	}

	response, err := c.Client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("query %s/status: %w", c.URL, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("query %s/status: unexpected status %s", c.URL, response.Status)
	}

	var status struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return 0, fmt.Errorf("decode %s/status: %w", c.URL, err)
	}

	height, err := strconv.ParseInt(status.Result.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse latest_block_height %q: %w", status.Result.SyncInfo.LatestBlockHeight, err)
	}

	return height, nil
}
