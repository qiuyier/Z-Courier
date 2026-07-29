package server

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/qiuyier/Z-Courier/internal/pipeline"
)

func newTrafficPolicyHandler(
	config pipeline.TrafficPoliciesConfig,
) (*pipeline.TrafficPolicyHandler, io.Closer, error) {
	if !config.Enabled {
		return nil, nil, nil
	}

	switch strings.ToLower(strings.TrimSpace(config.Mode)) {
	case "", pipeline.TrafficPolicyModeLocal:
		config.Mode = pipeline.TrafficPolicyModeLocal
		return pipeline.NewTrafficPolicyHandler(config), nil, nil
	case pipeline.TrafficPolicyModeRedis:
		store, err := pipeline.NewRedisQuotaStore(config.Redis)
		if err != nil {
			return nil, nil, fmt.Errorf("traffic policy redis quota store: %w", err)
		}
		if err := store.Ping(context.Background()); err != nil {
			_ = store.Close()
			return nil, nil, fmt.Errorf("traffic policy redis quota ping: %w", err)
		}
		return pipeline.NewTrafficPolicyHandlerWithStore(config, store), store, nil
	default:
		return nil, nil, fmt.Errorf("unsupported traffic policy mode %q", config.Mode)
	}
}
