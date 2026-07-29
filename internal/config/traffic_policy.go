package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/server"
)

const (
	defaultTrafficPolicyMaxKeys               = 100000
	defaultTrafficPolicyIdleTTL               = 10 * time.Minute
	defaultTrafficPolicyRedisKeyPrefix        = "zcourier:traffic-policy"
	defaultTrafficPolicyRedisDialTimeout      = time.Second
	defaultTrafficPolicyRedisReadTimeout      = 500 * time.Millisecond
	defaultTrafficPolicyRedisWriteTimeout     = 500 * time.Millisecond
	defaultTrafficPolicyRedisOperationTimeout = 250 * time.Millisecond
)

type trafficPolicyMsgIDInterval struct {
	min uint32
	max uint32
}

func toTrafficPoliciesConfig(config TrafficPoliciesConfig, routes []server.UpstreamRouteConfig) (pipeline.TrafficPoliciesConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode == "" {
		mode = pipeline.TrafficPolicyModeLocal
	}
	switch mode {
	case pipeline.TrafficPolicyModeLocal, pipeline.TrafficPolicyModeRedis:
	default:
		return pipeline.TrafficPoliciesConfig{}, fmt.Errorf(
			"config: pipeline traffic_policies mode %q is not supported; use %q or %q",
			mode,
			pipeline.TrafficPolicyModeLocal,
			pipeline.TrafficPolicyModeRedis,
		)
	}

	maxKeys := config.MaxKeys
	if maxKeys < 0 {
		return pipeline.TrafficPoliciesConfig{}, fmt.Errorf("config: pipeline traffic_policies max_keys must not be negative")
	}
	if maxKeys == 0 {
		maxKeys = defaultTrafficPolicyMaxKeys
	}

	idleTTL := defaultTrafficPolicyIdleTTL
	if rawIdleTTL := strings.TrimSpace(config.IdleTTL); rawIdleTTL != "" {
		parsed, err := parseOptionalPositiveDuration(rawIdleTTL)
		if err != nil {
			return pipeline.TrafficPoliciesConfig{}, fmt.Errorf("config: pipeline traffic_policies idle_ttl: %w", err)
		}
		idleTTL = parsed
	}

	var redisConfig pipeline.RedisQuotaStoreConfig
	switch mode {
	case pipeline.TrafficPolicyModeLocal:
		if trafficPolicyRedisConfigSet(config.Redis) {
			return pipeline.TrafficPoliciesConfig{}, fmt.Errorf(
				"config: pipeline traffic_policies redis settings require mode %q",
				pipeline.TrafficPolicyModeRedis,
			)
		}
	case pipeline.TrafficPolicyModeRedis:
		parsed, err := toTrafficPolicyRedisConfig(config.Redis, idleTTL)
		if err != nil {
			return pipeline.TrafficPoliciesConfig{}, err
		}
		redisConfig = parsed
	}

	defaultPolicy := strings.TrimSpace(config.DefaultPolicy)
	if config.DefaultPolicy != "" && defaultPolicy == "" {
		return pipeline.TrafficPoliciesConfig{}, fmt.Errorf("config: pipeline traffic_policies default_policy must not be blank")
	}

	routeByName := make(map[string]pipeline.TrafficPolicyRoute, len(routes))
	policyRoutes := make([]pipeline.TrafficPolicyRoute, 0, len(routes))
	for _, route := range routes {
		maxMsgID := route.MsgIDMax
		if maxMsgID == 0 {
			maxMsgID = route.MsgIDMin
		}
		policyRoute := pipeline.TrafficPolicyRoute{
			Name:     strings.TrimSpace(route.Name),
			MsgIDMin: route.MsgIDMin,
			MsgIDMax: maxMsgID,
		}
		policyRoutes = append(policyRoutes, policyRoute)
		if policyRoute.Name != "" {
			routeByName[policyRoute.Name] = policyRoute
		}
	}

	policies := make([]pipeline.TrafficPolicy, 0, len(config.Policies))
	policyNames := make(map[string]struct{}, len(config.Policies))
	enabledPolicyNames := make(map[string]struct{}, len(config.Policies))
	for index, rawPolicy := range config.Policies {
		policy, enabled, err := toTrafficPolicy(rawPolicy, index, routeByName)
		if err != nil {
			return pipeline.TrafficPoliciesConfig{}, err
		}
		if _, exists := policyNames[policy.Name]; exists {
			return pipeline.TrafficPoliciesConfig{}, fmt.Errorf(
				"config: pipeline traffic_policies policy %q is duplicated",
				policy.Name,
			)
		}
		policyNames[policy.Name] = struct{}{}
		if !enabled {
			continue
		}

		policies = append(policies, policy)
		enabledPolicyNames[policy.Name] = struct{}{}
	}

	if config.Enabled && len(policies) == 0 {
		return pipeline.TrafficPoliciesConfig{}, fmt.Errorf("config: pipeline traffic_policies requires at least one enabled policy")
	}
	if defaultPolicy != "" {
		if _, exists := enabledPolicyNames[defaultPolicy]; !exists {
			return pipeline.TrafficPoliciesConfig{}, fmt.Errorf(
				"config: pipeline traffic_policies default_policy %q must name an enabled policy",
				defaultPolicy,
			)
		}
	}
	if err := validateTrafficPolicyAmbiguity(policies, routeByName); err != nil {
		return pipeline.TrafficPoliciesConfig{}, err
	}
	if config.Enabled && mode == pipeline.TrafficPolicyModeRedis {
		return pipeline.TrafficPoliciesConfig{}, fmt.Errorf(
			"config: pipeline traffic_policies mode %q is not operational yet; gateway lifecycle wiring is pending",
			pipeline.TrafficPolicyModeRedis,
		)
	}

	return pipeline.TrafficPoliciesConfig{
		Enabled:       config.Enabled,
		Mode:          mode,
		MaxKeys:       maxKeys,
		IdleTTL:       idleTTL,
		Redis:         redisConfig,
		DefaultPolicy: defaultPolicy,
		Policies:      policies,
		Routes:        policyRoutes,
	}, nil
}

func toTrafficPolicyRedisConfig(
	config TrafficPolicyRedisConfig,
	idleTTL time.Duration,
) (pipeline.RedisQuotaStoreConfig, error) {
	addr := strings.TrimSpace(config.Addr)
	if addr == "" {
		return pipeline.RedisQuotaStoreConfig{}, fmt.Errorf(
			"config: pipeline traffic_policies redis.addr is required",
		)
	}
	if config.DB < 0 {
		return pipeline.RedisQuotaStoreConfig{}, fmt.Errorf(
			"config: pipeline traffic_policies redis.db must be greater than or equal to 0",
		)
	}
	if idleTTL < time.Millisecond {
		return pipeline.RedisQuotaStoreConfig{}, fmt.Errorf(
			"config: pipeline traffic_policies idle_ttl must be at least 1ms in redis mode",
		)
	}

	keyPrefix := defaultTrafficPolicyRedisKeyPrefix
	if config.KeyPrefix != "" {
		keyPrefix = strings.TrimSpace(config.KeyPrefix)
		if keyPrefix == "" {
			return pipeline.RedisQuotaStoreConfig{}, fmt.Errorf(
				"config: pipeline traffic_policies redis.key_prefix must not be blank",
			)
		}
	}

	dialTimeout, err := trafficPolicyRedisDuration(
		config.DialTimeout,
		defaultTrafficPolicyRedisDialTimeout,
		"dial_timeout",
	)
	if err != nil {
		return pipeline.RedisQuotaStoreConfig{}, err
	}
	readTimeout, err := trafficPolicyRedisDuration(
		config.ReadTimeout,
		defaultTrafficPolicyRedisReadTimeout,
		"read_timeout",
	)
	if err != nil {
		return pipeline.RedisQuotaStoreConfig{}, err
	}
	writeTimeout, err := trafficPolicyRedisDuration(
		config.WriteTimeout,
		defaultTrafficPolicyRedisWriteTimeout,
		"write_timeout",
	)
	if err != nil {
		return pipeline.RedisQuotaStoreConfig{}, err
	}
	operationTimeout, err := trafficPolicyRedisDuration(
		config.OperationTimeout,
		defaultTrafficPolicyRedisOperationTimeout,
		"operation_timeout",
	)
	if err != nil {
		return pipeline.RedisQuotaStoreConfig{}, err
	}

	failureMode := strings.ToLower(strings.TrimSpace(config.FailureMode))
	if failureMode == "" {
		failureMode = pipeline.TrafficPolicyFailureModeFailClosed
	}
	if failureMode != pipeline.TrafficPolicyFailureModeFailClosed {
		return pipeline.RedisQuotaStoreConfig{}, fmt.Errorf(
			"config: pipeline traffic_policies redis.failure_mode supports only %q",
			pipeline.TrafficPolicyFailureModeFailClosed,
		)
	}

	return pipeline.RedisQuotaStoreConfig{
		Addr:             addr,
		Username:         strings.TrimSpace(config.Username),
		Password:         config.Password,
		DB:               config.DB,
		KeyPrefix:        keyPrefix,
		IdleTTL:          idleTTL,
		DialTimeout:      dialTimeout,
		ReadTimeout:      readTimeout,
		WriteTimeout:     writeTimeout,
		OperationTimeout: operationTimeout,
		FailureMode:      failureMode,
	}, nil
}

func trafficPolicyRedisDuration(raw string, defaultValue time.Duration, field string) (time.Duration, error) {
	parsed, err := parseOptionalPositiveDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf(
			"config: pipeline traffic_policies redis.%s: %w",
			field,
			err,
		)
	}
	if parsed == 0 {
		return defaultValue, nil
	}
	return parsed, nil
}

func trafficPolicyRedisConfigSet(config TrafficPolicyRedisConfig) bool {
	return config.Addr != "" ||
		config.Username != "" ||
		config.Password != "" ||
		config.DB != 0 ||
		config.KeyPrefix != "" ||
		config.DialTimeout != "" ||
		config.ReadTimeout != "" ||
		config.WriteTimeout != "" ||
		config.OperationTimeout != "" ||
		config.FailureMode != ""
}

func toTrafficPolicy(
	config TrafficPolicyConfig,
	index int,
	routes map[string]pipeline.TrafficPolicyRoute,
) (pipeline.TrafficPolicy, bool, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy #%d name is required",
			index+1,
		)
	}
	if config.Priority <= 0 {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q priority must be greater than 0",
			name,
		)
	}

	key := strings.ToLower(strings.TrimSpace(config.Key))
	if key == "" {
		key = pipeline.TrafficPolicyKeyClientID
	}
	if key != pipeline.TrafficPolicyKeyClientID {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q key %q is unsupported; V16.1 supports only %q",
			name,
			key,
			pipeline.TrafficPolicyKeyClientID,
		)
	}

	clientIDs, err := normalizeTrafficPolicyValues(config.Match.ClientIDs, "client_id", name)
	if err != nil {
		return pipeline.TrafficPolicy{}, false, err
	}
	routeNames, err := normalizeTrafficPolicyValues(config.Match.Routes, "route", name)
	if err != nil {
		return pipeline.TrafficPolicy{}, false, err
	}
	for _, routeName := range routeNames {
		if _, exists := routes[routeName]; !exists {
			return pipeline.TrafficPolicy{}, false, fmt.Errorf(
				"config: pipeline traffic_policies policy %q references unknown enabled route %q",
				name,
				routeName,
			)
		}
	}

	if config.Match.MsgIDMin == 0 && config.Match.MsgIDMax != 0 {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q msg_id_min is required when msg_id_max is set",
			name,
		)
	}
	if config.Match.MsgIDMax != 0 && config.Match.MsgIDMax < config.Match.MsgIDMin {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q msg_id_max must be greater than or equal to msg_id_min",
			name,
		)
	}

	if config.TokenBucket.Capacity <= 0 {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q token_bucket capacity must be greater than 0",
			name,
		)
	}
	if config.TokenBucket.RefillTokens <= 0 {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q token_bucket refill_tokens must be greater than 0",
			name,
		)
	}
	refillInterval, err := parseOptionalPositiveDuration(strings.TrimSpace(config.TokenBucket.RefillInterval))
	if err != nil {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q token_bucket refill_interval: %w",
			name,
			err,
		)
	}
	if refillInterval <= 0 {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q token_bucket refill_interval must be greater than 0",
			name,
		)
	}

	policy := pipeline.TrafficPolicy{
		Name:     name,
		Priority: config.Priority,
		Match: pipeline.TrafficPolicyMatch{
			ClientIDs: clientIDs,
			MsgIDMin:  config.Match.MsgIDMin,
			MsgIDMax:  config.Match.MsgIDMax,
			Routes:    routeNames,
		},
		Key: key,
		TokenBucket: pipeline.TokenBucketConfig{
			Capacity:       config.TokenBucket.Capacity,
			RefillTokens:   config.TokenBucket.RefillTokens,
			RefillInterval: refillInterval,
		},
	}
	if len(trafficPolicyIntervals(policy, routes)) == 0 {
		return pipeline.TrafficPolicy{}, false, fmt.Errorf(
			"config: pipeline traffic_policies policy %q route and msg_id selectors cannot match the same packet",
			name,
		)
	}

	enabled := config.Enabled == nil || *config.Enabled
	return policy, enabled, nil
}

func normalizeTrafficPolicyValues(values []string, kind, policyName string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf(
				"config: pipeline traffic_policies policy %q contains a blank %s selector",
				policyName,
				kind,
			)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf(
				"config: pipeline traffic_policies policy %q contains duplicate %s selector %q",
				policyName,
				kind,
				value,
			)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func validateTrafficPolicyAmbiguity(
	policies []pipeline.TrafficPolicy,
	routes map[string]pipeline.TrafficPolicyRoute,
) error {
	for leftIndex, left := range policies {
		leftIntervals := trafficPolicyIntervals(left, routes)
		for rightIndex := leftIndex + 1; rightIndex < len(policies); rightIndex++ {
			right := policies[rightIndex]
			if left.Priority != right.Priority ||
				!trafficPolicyClientSelectorsOverlap(left.Match.ClientIDs, right.Match.ClientIDs) ||
				!trafficPolicyIntervalsOverlap(leftIntervals, trafficPolicyIntervals(right, routes)) {
				continue
			}
			return fmt.Errorf(
				"config: pipeline traffic_policies policies %q and %q overlap at priority %d",
				left.Name,
				right.Name,
				left.Priority,
			)
		}
	}
	return nil
}

func trafficPolicyClientSelectorsOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return true
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	for _, value := range left {
		if _, exists := rightSet[value]; exists {
			return true
		}
	}
	return false
}

func trafficPolicyIntervals(
	policy pipeline.TrafficPolicy,
	routes map[string]pipeline.TrafficPolicyRoute,
) []trafficPolicyMsgIDInterval {
	policyInterval := trafficPolicyMsgIDInterval{min: 0, max: ^uint32(0)}
	if policy.Match.MsgIDMin != 0 {
		policyInterval.min = policy.Match.MsgIDMin
		policyInterval.max = policy.Match.MsgIDMax
		if policyInterval.max == 0 {
			policyInterval.max = policyInterval.min
		}
	}
	if len(policy.Match.Routes) == 0 {
		return []trafficPolicyMsgIDInterval{policyInterval}
	}

	intervals := make([]trafficPolicyMsgIDInterval, 0, len(policy.Match.Routes))
	for _, routeName := range policy.Match.Routes {
		route, exists := routes[routeName]
		if !exists {
			continue
		}
		routeInterval := trafficPolicyMsgIDInterval{min: route.MsgIDMin, max: route.MsgIDMax}
		if routeInterval.max == 0 {
			routeInterval.max = routeInterval.min
		}
		intersection := trafficPolicyMsgIDInterval{
			min: max(policyInterval.min, routeInterval.min),
			max: min(policyInterval.max, routeInterval.max),
		}
		if intersection.min <= intersection.max {
			intervals = append(intervals, intersection)
		}
	}
	return intervals
}

func trafficPolicyIntervalsOverlap(left, right []trafficPolicyMsgIDInterval) bool {
	for _, leftInterval := range left {
		for _, rightInterval := range right {
			if leftInterval.min <= rightInterval.max && rightInterval.min <= leftInterval.max {
				return true
			}
		}
	}
	return false
}
