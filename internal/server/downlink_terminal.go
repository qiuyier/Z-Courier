package server

import (
	"context"
	"fmt"
	"io"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/nsqforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/webhookpublisher"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

type rawTerminalPublisher interface {
	Publish(context.Context, []byte) error
}

type terminalEnvelopePublisher struct {
	next rawTerminalPublisher
}

func (p *terminalEnvelopePublisher) Publish(ctx context.Context, event downlink.TerminalEvent) error {
	body, err := sonic.Marshal(event)
	if err != nil {
		return fmt.Errorf("terminal publisher: encode event: %w", err)
	}
	if err := p.next.Publish(ctx, body); err != nil {
		return fmt.Errorf("terminal publisher: %w", err)
	}
	return nil
}

func newTerminalPublisher(config Config) (downlink.TerminalPublisher, io.Closer, error) {
	switch config.DownlinkTerminal.PublisherType {
	case "", downlink.TerminalPublisherNone:
		return nil, nil, nil
	case downlink.TerminalPublisherNSQ:
		nsqConfig := config.DownlinkTerminal.NSQ
		forwarder, err := nsqforwarder.New(nsqforwarder.Config{
			Address:       nsqConfig.Address,
			Addresses:     nsqConfig.Addresses,
			Topic:         nsqConfig.Topic,
			AuthSecret:    nsqConfig.AuthSecret,
			DialTimeout:   nsqConfig.DialTimeout,
			ReadTimeout:   nsqConfig.ReadTimeout,
			WriteTimeout:  nsqConfig.WriteTimeout,
			PublishMode:   nsqConfig.PublishMode,
			RetryAttempts: nsqConfig.RetryAttempts,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("downlink terminal NSQ publisher: %w", err)
		}
		return &terminalEnvelopePublisher{next: forwarder}, forwarder, nil
	case downlink.TerminalPublisherHTTP:
		httpConfig := config.DownlinkTerminal.HTTP
		signer, err := signing.NewSigner(signing.SignerConfig{
			KeyID:  httpConfig.HMACKeyID,
			Secret: httpConfig.HMACSecret,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("downlink terminal HTTP publisher signer: %w", err)
		}
		publisher, err := webhookpublisher.New(webhookpublisher.Config{
			URL:     httpConfig.URL,
			Timeout: httpConfig.Timeout,
			Signer:  signer,
			TLS:     httpConfig.TLS,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("downlink terminal HTTP publisher: %w", err)
		}
		return &terminalEnvelopePublisher{next: publisher}, publisher, nil
	default:
		return nil, nil, fmt.Errorf("unsupported downlink terminal publisher type %q", config.DownlinkTerminal.PublisherType)
	}
}
