package downlink

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
)

const PeerPushPath = "/internal/cluster/push"

type PeerDispatcher interface {
	Push(ctx context.Context, target cluster.RouteEntry, req PeerPushRequest) (*PeerPushResponse, error)
}

type HTTPPeerDispatcherConfig struct {
	Token               string
	Timeout             time.Duration
	MaxResponseBodySize int64
	Client              *http.Client
}

type HTTPPeerDispatcher struct {
	token               string
	client              *http.Client
	maxResponseBodySize int64
}

type PeerPushHTTPError struct {
	StatusCode int
	Code       string
	Reason     string
}

func (e *PeerPushHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason != "" {
		return fmt.Sprintf("peer push failed: status=%d code=%s reason=%s", e.StatusCode, e.Code, e.Reason)
	}
	return fmt.Sprintf("peer push failed: status=%d code=%s", e.StatusCode, e.Code)
}

func NewHTTPPeerDispatcher(config HTTPPeerDispatcherConfig) *HTTPPeerDispatcher {
	client := config.Client
	if client == nil {
		client = &http.Client{
			Timeout: config.Timeout,
		}
	} else if config.Timeout > 0 && client.Timeout == 0 {
		cloned := *client
		cloned.Timeout = config.Timeout
		client = &cloned
	}

	maxResponseBodySize := config.MaxResponseBodySize
	if maxResponseBodySize <= 0 {
		maxResponseBodySize = 1 << 20
	}

	return &HTTPPeerDispatcher{
		token:               config.Token,
		client:              client,
		maxResponseBodySize: maxResponseBodySize,
	}
}

func (d *HTTPPeerDispatcher) Push(ctx context.Context, target cluster.RouteEntry, req PeerPushRequest) (*PeerPushResponse, error) {
	if d == nil || d.client == nil {
		return nil, fmt.Errorf("peer dispatcher is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if target.InternalAddr == "" {
		return nil, fmt.Errorf("peer target internal_addr is required")
	}
	if req.ClientID == "" {
		req.ClientID = target.ClientID
	}
	if req.DeviceID == "" {
		req.DeviceID = target.DeviceID
	}
	if req.SessionID == "" {
		req.SessionID = target.SessionID
	}

	body, err := sonic.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, peerPushURL(target.InternalAddr), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if d.token != "" {
		httpReq.Header.Set(InternalTokenHeader, d.token)
	}

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, d.maxResponseBodySize))
	if err != nil {
		return nil, err
	}

	var peerResp PeerPushResponse
	if len(respBody) > 0 {
		if err := sonic.Unmarshal(respBody, &peerResp); err != nil {
			return nil, err
		}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &peerResp, &PeerPushHTTPError{
			StatusCode: resp.StatusCode,
			Code:       peerResp.Code,
			Reason:     peerResp.Reason,
		}
	}

	return &peerResp, nil
}

func peerPushURL(internalAddr string) string {
	return strings.TrimRight(internalAddr, "/") + PeerPushPath
}
