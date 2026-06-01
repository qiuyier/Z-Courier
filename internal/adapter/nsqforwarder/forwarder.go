package nsqforwarder

import (
	"context"
	"fmt"
	"sync"
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
)

type Producer interface {
	Publish(topic string, body []byte) error
	Stop()
}

type Config struct {
	Address      string
	Topic        string
	AuthSecret   string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	Producer     Producer
}

type Forwarder struct {
	topic    string
	producer Producer
	stopOnce sync.Once
}

func New(config Config) (*Forwarder, error) {
	if config.Topic == "" {
		return nil, fmt.Errorf("nsq forwarder: empty topic")
	}

	producer := config.Producer
	if producer == nil {
		if config.Address == "" {
			return nil, fmt.Errorf("nsq forwarder: empty address")
		}

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

		var err error
		producer, err = nsq.NewProducer(config.Address, nsqConfig)
		if err != nil {
			return nil, fmt.Errorf("nsq forwarder: create producer: %w", err)
		}
	}

	return &Forwarder{
		topic:    config.Topic,
		producer: producer,
	}, nil
}

func (f *Forwarder) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if err := ctx.Err(); err != nil {
		return &router.ForwardResult{TargetType: TargetType, Status: StatusFailed}, err
	}

	body, err := sonic.Marshal(upstream.NewMessage(packet))
	if err != nil {
		return &router.ForwardResult{TargetType: TargetType, Status: StatusFailed}, err
	}

	if err := f.producer.Publish(f.topic, body); err != nil {
		return &router.ForwardResult{TargetType: TargetType, Status: StatusFailed}, err
	}

	return &router.ForwardResult{
		TargetType: TargetType,
		Status:     StatusPublished,
	}, nil
}

func (f *Forwarder) Close() error {
	f.stopOnce.Do(func() {
		f.producer.Stop()
	})
	return nil
}
