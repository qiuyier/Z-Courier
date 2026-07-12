package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const (
	defaultTimeout             = 10 * time.Second
	defaultMaxResponseBodySize = 1 << 20

	pushPath      = "/internal/push"
	batchPushPath = "/internal/push/batch"
	statusPath    = "/internal/message/status"
	listPath      = "/internal/messages"
	requeuePath   = "/internal/message/requeue"
	discardPath   = "/internal/message/discard"
)

// Config controls the gateway backend client.
type Config struct {
	BaseURL             string
	InternalToken       string
	HMAC                *HMACConfig
	HTTPClient          *http.Client
	Timeout             time.Duration
	MaxResponseBodySize int64
}

// HMACConfig enables replay-resistant internal request signing.
type HMACConfig struct {
	KeyID  string
	Secret []byte
}

// Client calls one gateway node's internal HTTP API.
type Client struct {
	baseURL             string
	internalToken       string
	signer              *signing.Signer
	httpClient          *http.Client
	timeout             time.Duration
	maxResponseBodySize int64
}

// NewClient validates configuration and creates a reusable client.
// Redirects are always rejected so the internal token cannot be forwarded to
// a different address.
func NewClient(config Config) (*Client, error) {
	baseURL, err := normalizeBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}

	timeout := config.Timeout
	if timeout < 0 {
		return nil, fmt.Errorf("%w: timeout must not be negative", ErrInvalidConfig)
	}
	if timeout == 0 {
		timeout = defaultTimeout
	}

	maxResponseBodySize := config.MaxResponseBodySize
	if maxResponseBodySize < 0 || maxResponseBodySize == math.MaxInt64 {
		return nil, fmt.Errorf("%w: max response body size is invalid", ErrInvalidConfig)
	}
	if maxResponseBodySize == 0 {
		maxResponseBodySize = defaultMaxResponseBodySize
	}

	var signer *signing.Signer
	if config.HMAC != nil {
		if config.InternalToken != "" {
			return nil, fmt.Errorf("%w: internal token and HMAC cannot both be configured", ErrInvalidConfig)
		}
		signer, err = signing.NewSigner(signing.SignerConfig{
			KeyID:  config.HMAC.KeyID,
			Secret: config.HMAC.Secret,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: HMAC: %v", ErrInvalidConfig, err)
		}
	}

	httpClient := &http.Client{}
	if config.HTTPClient != nil {
		clone := *config.HTTPClient
		httpClient = &clone
	}
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return ErrRedirect
	}

	return &Client{
		baseURL:             baseURL,
		internalToken:       config.InternalToken,
		signer:              signer,
		httpClient:          httpClient,
		timeout:             timeout,
		maxResponseBodySize: maxResponseBodySize,
	}, nil
}

// Push submits one downlink message. HTTP 202 is a successful queued result.
func (c *Client) Push(ctx context.Context, request PushRequest) (*PushResponse, error) {
	if err := validatePushRequest(request); err != nil {
		return nil, err
	}

	var response PushResponse
	if err := c.doJSON(ctx, http.MethodPost, pushPath, nil, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// PushBatch submits multiple downlink messages. HTTP 207 is decoded as a
// normal BatchPushResponse because individual results carry the failures.
func (c *Client) PushBatch(ctx context.Context, request BatchPushRequest) (*BatchPushResponse, error) {
	if len(request.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages is required", ErrInvalidArgument)
	}
	for index, message := range request.Messages {
		if err := validatePushRequest(message); err != nil {
			return nil, fmt.Errorf("%w: messages[%d]: %v", ErrInvalidArgument, index, err)
		}
	}

	var response BatchPushResponse
	if err := c.doJSON(ctx, http.MethodPost, batchPushPath, nil, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetMessage retrieves delivery and retry state for one message.
func (c *Client) GetMessage(ctx context.Context, messageID string) (*MessageStatusResponse, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, fmt.Errorf("%w: message_id is required", ErrInvalidArgument)
	}

	query := url.Values{"message_id": []string{messageID}}
	var response MessageStatusResponse
	if err := c.doJSON(ctx, http.MethodGet, statusPath, query, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ListMessages retrieves persisted messages by delivery status.
func (c *Client) ListMessages(ctx context.Context, request ListMessagesRequest) (*ListMessagesResponse, error) {
	if request.Status != "" && !request.Status.Valid() {
		return nil, fmt.Errorf("%w: unknown message status %q", ErrInvalidArgument, request.Status)
	}
	if request.Limit < 0 {
		return nil, fmt.Errorf("%w: limit must not be negative", ErrInvalidArgument)
	}

	query := make(url.Values)
	if request.Status != "" {
		query.Set("status", string(request.Status))
	}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}
	if cursor := strings.TrimSpace(request.Cursor); cursor != "" {
		query.Set("cursor", cursor)
	}

	var response ListMessagesResponse
	if err := c.doJSON(ctx, http.MethodGet, listPath, query, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// Requeue moves a retryable message back to pending state.
func (c *Client) Requeue(ctx context.Context, messageID string) (*MessageStatusResponse, error) {
	return c.messageAction(ctx, requeuePath, messageID, "")
}

// Discard stops future delivery attempts for a message.
func (c *Client) Discard(ctx context.Context, messageID, reason string) (*MessageStatusResponse, error) {
	return c.messageAction(ctx, discardPath, messageID, strings.TrimSpace(reason))
}

func (c *Client) messageAction(ctx context.Context, path, messageID, reason string) (*MessageStatusResponse, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, fmt.Errorf("%w: message_id is required", ErrInvalidArgument)
	}

	request := MessageActionRequest{MessageID: messageID, Reason: reason}
	var response MessageStatusResponse
	if err := c.doJSON(ctx, http.MethodPost, path, nil, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, input, output any) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidArgument)
	}

	requestURL := c.baseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}

	var body io.Reader
	var requestBody []byte
	if input != nil {
		data, err := sonic.Marshal(input)
		if err != nil {
			return fmt.Errorf("%w: encode request: %v", ErrInvalidArgument, err)
		}
		requestBody = data
		body = bytes.NewReader(requestBody)
	}

	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestContext, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("%w: create request: %v", ErrInvalidArgument, err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.internalToken != "" {
		request.Header.Set(InternalTokenHeader, c.internalToken)
	}
	if c.signer != nil {
		if err := c.signer.Sign(request, requestBody); err != nil {
			return fmt.Errorf("%w: %v", ErrSigning, err)
		}
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return &RequestError{Method: method, URL: requestURL, Err: err}
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBodySize+1))
	if err != nil {
		return &RequestError{Method: method, URL: requestURL, Err: err}
	}
	if int64(len(responseBody)) > c.maxResponseBodySize {
		return fmt.Errorf("%w: limit is %d bytes", ErrResponseTooLarge, c.maxResponseBodySize)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return apiErrorFromResponse(response.StatusCode, responseBody)
	}
	if len(responseBody) == 0 {
		return fmt.Errorf("%w: empty body", ErrInvalidResponse)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := sonic.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if strings.TrimSpace(envelope.Code) == "" {
		return fmt.Errorf("%w: missing code", ErrInvalidResponse)
	}
	if err := sonic.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	return nil
}

func apiErrorFromResponse(statusCode int, body []byte) error {
	var response struct {
		Code            string `json:"code"`
		Reason          string `json:"reason"`
		CapacityScope   string `json:"capacity_scope"`
		CapacityLimit   int    `json:"capacity_limit"`
		CapacityPending int    `json:"capacity_pending"`
	}
	_ = sonic.Unmarshal(body, &response)
	return &APIError{
		StatusCode:      statusCode,
		Code:            response.Code,
		Reason:          response.Reason,
		CapacityScope:   response.CapacityScope,
		CapacityLimit:   response.CapacityLimit,
		CapacityPending: response.CapacityPending,
	}
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: base URL is required", ErrInvalidConfig)
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%w: base URL must be absolute", ErrInvalidConfig)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: base URL scheme must be http or https", ErrInvalidConfig)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: base URL must not contain user info, query, or fragment", ErrInvalidConfig)
	}

	return strings.TrimRight(parsed.String(), "/"), nil
}

func validatePushRequest(request PushRequest) error {
	if strings.TrimSpace(request.ClientID) == "" {
		return fmt.Errorf("%w: client_id is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(request.DeviceID) == "" {
		return fmt.Errorf("%w: device_id is required", ErrInvalidArgument)
	}
	if request.MsgID == 0 {
		return fmt.Errorf("%w: msg_id must not be zero", ErrInvalidArgument)
	}
	return nil
}
