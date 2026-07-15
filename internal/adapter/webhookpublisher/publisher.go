// Package webhookpublisher sends signed terminal-event envelopes to HTTP receivers.
package webhookpublisher

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const contentTypeJSON = "application/json"

type Config struct {
	URL     string
	Timeout time.Duration
	Signer  *signing.Signer
	Client  *http.Client
}

type Publisher struct {
	url    string
	signer *signing.Signer
	client *http.Client
}

func New(config Config) (*Publisher, error) {
	rawURL := strings.TrimSpace(config.URL)
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("webhook publisher: URL requires an absolute http or https URL")
	}
	if config.Signer == nil {
		return nil, fmt.Errorf("webhook publisher: signer is required")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else {
		copy := *client
		if copy.Timeout <= 0 {
			copy.Timeout = timeout
		}
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Publisher{url: rawURL, signer: config.Signer, client: client}, nil
}

func (p *Publisher) Publish(ctx context.Context, body []byte) error {
	if p == nil || p.client == nil || p.signer == nil || p.url == "" {
		return fmt.Errorf("webhook publisher: not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook publisher: create request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)
	if err := p.signer.Sign(req, body); err != nil {
		return fmt.Errorf("webhook publisher: sign request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook publisher: send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook publisher: unexpected HTTP status %d", resp.StatusCode)
	}
	return nil
}
