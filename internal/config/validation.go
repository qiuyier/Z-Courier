package config

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/server"
)

// ValidationReport contains non-fatal configuration findings.
type ValidationReport struct {
	Warnings []string
}

// ValidationError reports one or more fatal configuration problems.
type ValidationError struct {
	Problems []string
	Causes   []error
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "config: validation failed"
	}
	if len(e.Problems) == 1 {
		return "config: " + e.Problems[0]
	}
	return "config: validation failed:\n- " + strings.Join(e.Problems, "\n- ")
}

func (e *ValidationError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.Causes...)
}

// ValidateFile loads a config file and runs static validation without
// connecting to external dependencies.
func ValidateFile(path string) (ValidationReport, error) {
	fileConfig, err := Load(path)
	if err != nil {
		return ValidationReport{}, err
	}
	return fileConfig.Validate()
}

// Validate checks static config structure and cross-field relationships. It
// avoids active dependency checks such as JWKS fetches or database connects.
func (c *File) Validate() (ValidationReport, error) {
	if c == nil {
		return ValidationReport{}, fmt.Errorf("config: file is nil")
	}

	collector := validationCollector{}
	if err := c.validateShape(); err != nil {
		collector.addError(err)
	}
	c.validateUpstreamRouteConflicts(&collector)
	c.validateOperationalWarnings(&collector)

	report := ValidationReport{Warnings: collector.warnings()}
	if err := collector.err(); err != nil {
		return report, err
	}
	return report, nil
}

func (c *File) validateShape() error {
	out := server.DefaultConfig()
	if c.GatewayNode != "" {
		out.GatewayNode = c.GatewayNode
	}
	if len(c.RouteMsgIDs) > 0 {
		out.RouteMsgIDs = append([]uint32(nil), c.RouteMsgIDs...)
	}
	if err := applyInternalHTTPConfig(&out, c.InternalHTTP); err != nil {
		return err
	}
	if err := applyAdminConsoleConfig(&out, c.AdminConsole); err != nil {
		return err
	}
	if err := applyClusterConfig(&out, c.Cluster); err != nil {
		return err
	}
	if err := applyDownlinkConfig(&out, c.Downlink); err != nil {
		return err
	}
	if _, err := toPipelineConfig(c.Pipeline); err != nil {
		return err
	}
	if _, err := toUpstreamRoutes(c.Upstream.Routes); err != nil {
		return err
	}
	if err := validateAuthConfigShape(c.Auth); err != nil {
		return err
	}
	return nil
}

func validateAuthConfigShape(config AuthConfig) error {
	provider := strings.ToLower(strings.TrimSpace(config.Type))
	if provider == "" {
		hasHTTP := isHTTPAuthConfigSet(config.HTTP)
		hasJWT := isJWTAuthConfigSet(config.JWT)
		if config.StaticTokens == nil && !hasHTTP && !hasJWT {
			return nil
		}
		if config.StaticTokens == nil || hasHTTP || hasJWT {
			return fmt.Errorf("%w: auth.type is required when using a non-static provider", auth.ErrMisconfigured)
		}
		provider = auth.ProviderStatic
	}

	if err := validateAuthProviderConfig(provider, config); err != nil {
		return err
	}
	if _, _, err := toAuthCacheConfig(config.Cache); err != nil {
		return err
	}

	switch provider {
	case auth.ProviderStatic:
		if len(config.StaticTokens) == 0 {
			return fmt.Errorf("%w: static provider requires static_tokens", auth.ErrMisconfigured)
		}
		for token, principal := range config.StaticTokens {
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("%w: static provider token key must not be empty", auth.ErrMisconfigured)
			}
			if strings.TrimSpace(principal.ClientID) == "" {
				return fmt.Errorf("%w: static token %q requires client_id", auth.ErrMisconfigured, token)
			}
		}
	case auth.ProviderHTTP:
		if strings.TrimSpace(config.HTTP.URL) == "" {
			return fmt.Errorf("%w: http provider requires auth.http.url", auth.ErrMisconfigured)
		}
		if err := validateAbsoluteHTTPURL(config.HTTP.URL, "http provider"); err != nil {
			return fmt.Errorf("%w: %v", auth.ErrMisconfigured, err)
		}
		if !validHTTPHeaderValue(config.HTTP.InternalToken) {
			return fmt.Errorf("%w: http provider internal_token contains invalid characters", auth.ErrMisconfigured)
		}
		if _, err := parseOptionalPositiveDuration(config.HTTP.Timeout); err != nil {
			return fmt.Errorf("%w: invalid auth.http.timeout: %v", auth.ErrMisconfigured, err)
		}
		if config.HTTP.MaxInFlight < 0 {
			return fmt.Errorf("%w: http provider max_in_flight must be greater than 0", auth.ErrMisconfigured)
		}
	case auth.ProviderJWT:
		if strings.TrimSpace(config.JWT.Issuer) == "" ||
			strings.TrimSpace(config.JWT.Audience) == "" ||
			strings.TrimSpace(config.JWT.JWKSURL) == "" ||
			len(config.JWT.Algorithms) == 0 {
			return fmt.Errorf("%w: jwt provider requires issuer, audience, jwks_url, and algorithms", auth.ErrMisconfigured)
		}
		if err := validateAbsoluteHTTPURL(config.JWT.JWKSURL, "jwt provider JWKS"); err != nil {
			return fmt.Errorf("%w: %v", auth.ErrMisconfigured, err)
		}
		for _, algorithm := range config.JWT.Algorithms {
			if !supportedJWKSAlgorithm(algorithm) {
				return fmt.Errorf("%w: jwt provider algorithm %q is unsupported for JWKS verification", auth.ErrMisconfigured, algorithm)
			}
		}
		if _, err := parseOptionalNonNegativeDuration(config.JWT.ClockSkew); err != nil {
			return fmt.Errorf("%w: invalid auth.jwt.clock_skew: %v", auth.ErrMisconfigured, err)
		}
		if _, err := parseOptionalPositiveDuration(config.JWT.RefreshInterval); err != nil {
			return fmt.Errorf("%w: invalid auth.jwt.refresh_interval: %v", auth.ErrMisconfigured, err)
		}
		if _, err := parseOptionalPositiveDuration(config.JWT.FetchTimeout); err != nil {
			return fmt.Errorf("%w: invalid auth.jwt.fetch_timeout: %v", auth.ErrMisconfigured, err)
		}
		if config.JWT.MaxResponseBodySize < 0 {
			return fmt.Errorf("%w: jwt provider max_response_body_size must be greater than 0", auth.ErrMisconfigured)
		}
	default:
		return fmt.Errorf("%w: unsupported auth provider %q", auth.ErrMisconfigured, provider)
	}
	return nil
}

func supportedJWKSAlgorithm(name string) bool {
	method := jwtlib.GetSigningMethod(strings.TrimSpace(name))
	switch method.(type) {
	case *jwtlib.SigningMethodRSA, *jwtlib.SigningMethodRSAPSS, *jwtlib.SigningMethodECDSA, *jwtlib.SigningMethodEd25519:
		return true
	default:
		return false
	}
}

func validateAbsoluteHTTPURL(rawURL, label string) error {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil ||
		parsedURL.Fragment != "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("%s requires an absolute http or https URL", label)
	}
	return nil
}

func validHTTPHeaderValue(value string) bool {
	for index := range len(value) {
		if value[index] < 0x20 || value[index] == 0x7f {
			return false
		}
	}
	return true
}

func (c *File) validateUpstreamRouteConflicts(collector *validationCollector) {
	ranges := make([]validationMsgIDRange, 0, len(c.Upstream.Routes))
	routeNames := make(map[string]int)

	for index, route := range c.Upstream.Routes {
		if !upstreamRouteEnabled(route) {
			continue
		}
		label := upstreamRouteLabel(route, index)
		trimmedName := strings.TrimSpace(route.Name)
		if trimmedName != "" {
			if previousIndex, exists := routeNames[trimmedName]; exists {
				collector.addProblem("enabled upstream route %s duplicates route name from route #%d", label, previousIndex+1)
			} else {
				routeNames[trimmedName] = index
			}
		}
		if err := validateMsgIDRange(route); err != nil {
			continue
		}
		maxMsgID := route.MsgIDMax
		if maxMsgID == 0 {
			maxMsgID = route.MsgIDMin
		}
		if maxMsgID-route.MsgIDMin > 10000 {
			collector.addProblem("enabled upstream route %s msg_id range is too large: %d-%d", label, route.MsgIDMin, maxMsgID)
			continue
		}
		for _, reserved := range reservedMsgIDs() {
			if route.MsgIDMin <= reserved && reserved <= maxMsgID {
				collector.addProblem("enabled upstream route %s uses reserved msg_id %d", label, reserved)
			}
		}
		ranges = append(ranges, validationMsgIDRange{
			label: label,
			min:   route.MsgIDMin,
			max:   maxMsgID,
		})
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].min == ranges[j].min {
			return ranges[i].max < ranges[j].max
		}
		return ranges[i].min < ranges[j].min
	})
	for index := 1; index < len(ranges); index++ {
		previous := ranges[index-1]
		current := ranges[index]
		if current.min <= previous.max {
			collector.addProblem(
				"enabled upstream route %s msg_id range %d-%d overlaps %s range %d-%d",
				current.label,
				current.min,
				current.max,
				previous.label,
				previous.min,
				previous.max,
			)
		}
	}
}

func (c *File) validateOperationalWarnings(collector *validationCollector) {
	if c.Cluster.Enabled {
		registryType := strings.ToLower(strings.TrimSpace(c.Cluster.Registry.Type))
		if registryType == "" {
			registryType = server.DefaultClusterConfig().Registry.Type
		}
		if registryType != "redis" {
			collector.addWarning("cluster is enabled with %q registry; multi-node deployments should use redis registry", registryType)
		}
		if strings.TrimSpace(c.Cluster.InternalAddr) == "" {
			collector.addWarning("cluster is enabled but cluster.internal_addr is empty; startup defaults may not be reachable by peer gateways")
		}
	}

	internalHTTPEnabled := c.InternalHTTP.Enabled == nil || *c.InternalHTTP.Enabled
	if internalHTTPEnabled {
		addr := server.DefaultConfig().InternalHTTPAddr
		if c.InternalHTTP.Addr != nil {
			addr = *c.InternalHTTP.Addr
		}
		mode := strings.ToLower(strings.TrimSpace(c.InternalHTTP.Auth.Mode))
		if mode == "" {
			mode = server.InternalHTTPAuthModeToken
		}
		if mode == server.InternalHTTPAuthModeToken && internalHTTPBindsWildcard(addr) {
			collector.addWarning("internal_http.addr %q listens on all interfaces with token auth; prefer HMAC or a private network boundary", addr)
		}
	}

	storageType := strings.ToLower(strings.TrimSpace(c.Downlink.Storage.Type))
	if storageType == "" {
		storageType = server.DefaultConfig().DownlinkStorage.Type
	}
	if c.Cluster.Enabled && storageType == "memory" {
		collector.addWarning("cluster is enabled with memory downlink storage; queued messages are not durable across gateway restarts")
	}
}

func internalHTTPBindsWildcard(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.HasPrefix(addr, "0.0.0.0:") || strings.HasPrefix(addr, "[::]:") || strings.HasPrefix(addr, ":")
	}
	return host == "" || host == "0.0.0.0" || host == "::" || host == "[::]"
}

func upstreamRouteEnabled(route UpstreamRouteConfig) bool {
	return route.Enabled == nil || *route.Enabled
}

func upstreamRouteLabel(route UpstreamRouteConfig, index int) string {
	if name := strings.TrimSpace(route.Name); name != "" {
		return fmt.Sprintf("%q", name)
	}
	return fmt.Sprintf("#%d", index+1)
}

func reservedMsgIDs() []uint32 {
	return []uint32{protocol.MsgIDAck, protocol.MsgIDDownlinkAck, protocol.MsgIDBind}
}

type validationMsgIDRange struct {
	label string
	min   uint32
	max   uint32
}

type validationCollector struct {
	problemsList []string
	warningsList []string
	causesList   []error
}

func (c *validationCollector) addError(err error) {
	if err == nil {
		return
	}
	message := strings.TrimPrefix(err.Error(), "config: ")
	c.problemsList = append(c.problemsList, message)
	c.causesList = append(c.causesList, err)
}

func (c *validationCollector) addProblem(format string, args ...any) {
	c.problemsList = append(c.problemsList, fmt.Sprintf(format, args...))
}

func (c *validationCollector) addWarning(format string, args ...any) {
	c.warningsList = append(c.warningsList, fmt.Sprintf(format, args...))
}

func (c *validationCollector) warnings() []string {
	return append([]string(nil), c.warningsList...)
}

func (c *validationCollector) err() error {
	if len(c.problemsList) == 0 {
		return nil
	}
	return &ValidationError{
		Problems: append([]string(nil), c.problemsList...),
		Causes:   append([]error(nil), c.causesList...),
	}
}
