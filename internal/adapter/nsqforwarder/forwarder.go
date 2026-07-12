package nsqforwarder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	nsq "github.com/nsqio/go-nsq"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
)

const (
	TargetType      = "nsq"
	StatusPublished = "published"
	StatusFailed    = "publish_failed"

	PublishModeRoundRobin = "round_robin"
)

type Producer interface {
	Publish(topic string, body []byte) error
	Stop()
}

type ProducerFactory func(address string, config *nsq.Config) (Producer, error)

type Config struct {
	Address       string
	Addresses     []string
	Topic         string
	AuthSecret    string
	DialTimeout   time.Duration
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	PublishMode   string
	RetryAttempts int
	Producer      Producer
	Producers     []Producer
	Factory       ProducerFactory
}

type Forwarder struct {
	topic         string
	endpoints     []endpoint
	retryAttempts int
	next          atomic.Uint64
	stopOnce      sync.Once
}

type endpoint struct {
	address  string
	producer Producer
}

func New(config Config) (*Forwarder, error) {
	if config.Topic == "" {
		return nil, fmt.Errorf("nsq forwarder: empty topic")
	}
	if config.RetryAttempts < 0 {
		return nil, fmt.Errorf("nsq forwarder: retry_attempts must be greater than or equal to 0")
	}
	if config.PublishMode == "" {
		config.PublishMode = PublishModeRoundRobin
	}
	if config.PublishMode != PublishModeRoundRobin {
		return nil, fmt.Errorf("nsq forwarder: unsupported publish mode %q", config.PublishMode)
	}

	endpoints, err := newEndpoints(config)
	if err != nil {
		return nil, err
	}

	return &Forwarder{
		topic:         config.Topic,
		endpoints:     endpoints,
		retryAttempts: config.RetryAttempts,
	}, nil
}

func newEndpoints(config Config) ([]endpoint, error) {
	producers := append([]Producer(nil), config.Producers...)
	if config.Producer != nil {
		producers = append(producers, config.Producer)
	}
	if len(producers) > 0 {
		endpoints := make([]endpoint, 0, len(producers))
		for i, producer := range producers {
			if producer == nil {
				return nil, fmt.Errorf("nsq forwarder: producer %d is nil", i)
			}
			endpoints = append(endpoints, endpoint{
				address:  fmt.Sprintf("producer-%d", i),
				producer: producer,
			})
		}
		return endpoints, nil
	}

	addresses := normalizedAddresses(config)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("nsq forwarder: empty address")
	}

	factory := config.Factory
	if factory == nil {
		factory = func(address string, nsqConfig *nsq.Config) (Producer, error) {
			return nsq.NewProducer(address, nsqConfig)
		}
	}

	nsqConfig := newNSQConfig(config)
	endpoints := make([]endpoint, 0, len(addresses))
	for _, address := range addresses {
		producer, err := factory(address, nsqConfig)
		if err != nil {
			closeEndpoints(endpoints)
			return nil, fmt.Errorf("nsq forwarder: create producer %q: %w", address, err)
		}
		if producer == nil {
			closeEndpoints(endpoints)
			return nil, fmt.Errorf("nsq forwarder: create producer %q: nil producer", address)
		}
		endpoints = append(endpoints, endpoint{
			address:  address,
			producer: producer,
		})
	}

	return endpoints, nil
}

func normalizedAddresses(config Config) []string {
	addresses := append([]string(nil), config.Addresses...)
	if len(addresses) == 0 && config.Address != "" {
		addresses = append(addresses, config.Address)
	}

	seen := make(map[string]struct{}, len(addresses))
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		out = append(out, address)
	}
	return out
}

func newNSQConfig(config Config) *nsq.Config {
	nsqConfig := nsq.NewConfig()
	if config.AuthSecret != "" {
		nsqConfig.AuthSecret = config.AuthSecret
	}
	if config.DialTimeout > 0 {
		nsqConfig.DialTimeout = config.DialTimeout
	}
	if config.ReadTimeout > 0 {
		nsqConfig.ReadTimeout = config.ReadTimeout
		if nsqConfig.HeartbeatInterval >= config.ReadTimeout {
			nsqConfig.HeartbeatInterval = config.ReadTimeout / 2
		}
	}
	if config.WriteTimeout > 0 {
		nsqConfig.WriteTimeout = config.WriteTimeout
	}

	return nsqConfig
}

func (f *Forwarder) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if err := ctx.Err(); err != nil {
		return &router.ForwardResult{TargetType: TargetType, Status: StatusFailed}, err
	}

	body, err := sonic.Marshal(upstream.NewMessage(packet))
	if err != nil {
		return &router.ForwardResult{TargetType: TargetType, Status: StatusFailed}, err
	}

	if err := f.publish(ctx, body); err != nil {
		return &router.ForwardResult{TargetType: TargetType, Status: StatusFailed}, err
	}

	return &router.ForwardResult{
		TargetType: TargetType,
		Status:     StatusPublished,
	}, nil
}

// Publish sends an already encoded payload through the same bounded producer
// selection and retry behavior used by upstream forwarding.
func (f *Forwarder) Publish(ctx context.Context, body []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.publish(ctx, body)
}

func (f *Forwarder) publish(ctx context.Context, body []byte) error {
	if len(f.endpoints) == 0 {
		return fmt.Errorf("nsq forwarder: no producers")
	}

	attempts := min(f.retryAttempts+1, len(f.endpoints))
	start := int(f.next.Add(1)-1) % len(f.endpoints)

	var joined error
	for i := range attempts {
		if err := ctx.Err(); err != nil {
			return err
		}

		endpoint := f.endpoints[(start+i)%len(f.endpoints)]
		if err := endpoint.producer.Publish(f.topic, body); err != nil {
			joined = errors.Join(joined, fmt.Errorf("%s: %w", endpoint.address, err))
			continue
		}

		return nil
	}

	return fmt.Errorf("nsq forwarder: publish failed after %d attempt(s): %w", attempts, joined)
}

func (f *Forwarder) Close() error {
	f.stopOnce.Do(func() {
		closeEndpoints(f.endpoints)
	})
	return nil
}

func closeEndpoints(endpoints []endpoint) {
	for _, endpoint := range endpoints {
		endpoint.producer.Stop()
	}
}
