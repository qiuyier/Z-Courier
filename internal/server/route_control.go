package server

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const (
	routeReloadTriggerAdminAPI = "admin_api"
	routeReloadTriggerSIGHUP   = "sighup"
	routeReloadHistoryLimit    = 20

	routeReloadStagePrecondition   = "precondition"
	routeReloadStageSourceRead     = "source_read"
	routeReloadStageParse          = "parse"
	routeReloadStageValidation     = "validation"
	routeReloadStageCandidateBuild = "candidate_build"
	routeReloadStageActivation     = "activation"
	routeReloadStageOperation      = "operation"
)

var (
	errRouteReloadDisabled       = errors.New("route reload is disabled")
	errRouteCandidateLoadFailed  = errors.New("route candidate load failed")
	errRouteCandidateBuildFailed = errors.New("route candidate build failed")
)

type routeReloadOptions struct {
	DryRun             bool
	ExpectedGeneration uint64
	Trigger            string
}

type routeReloadActor struct {
	AuthMode       string
	Principal      string
	Role           string
	AdminSessionID string
	AuthKeyID      string
	Method         string
	Path           string
	RemoteAddr     string
}

type routeReloadOutcome struct {
	Code               string
	Result             string
	Reason             string
	Stage              string
	HTTPStatus         int
	Trigger            string
	DryRun             bool
	ExpectedGeneration uint64
	Changed            bool
	WarningCount       int
	Duration           time.Duration
	Old                routeGenerationSnapshot
	Candidate          routeGenerationSnapshot
	Active             routeGenerationSnapshot
	Retiring           *routeGenerationSnapshot
}

type routeReloadAttemptSnapshot struct {
	Operation            string
	Trigger              string
	Stage                string
	Result               string
	Reason               string
	StartedAt            time.Time
	CompletedAt          time.Time
	Duration             time.Duration
	ExpectedGeneration   uint64
	OldGeneration        uint64
	CandidateGeneration  uint64
	CandidateFingerprint string
	CandidateRouteCount  int
	ActiveGeneration     uint64
	Changed              bool
	WarningCount         int
}

type routeControlRuntimeSnapshot struct {
	OperationsInFlight int
	LastStartedAt      time.Time
	LastSuccessAt      time.Time
	LastFailureAt      time.Time
	RecentAttempts     []routeReloadAttemptSnapshot
}

type routeControlSnapshot struct {
	Enabled      bool
	Closed       bool
	DrainTimeout time.Duration
	Active       *routeGenerationSnapshot
	Retiring     *routeGenerationSnapshot
	Runtime      routeControlRuntimeSnapshot
}

type routeControl struct {
	manager     *routeManager
	loader      UpstreamRouteLoader
	gatewayNode string
	audit       adminaudit.Recorder
	logger      *zap.Logger
	runtimeMu   sync.RWMutex
	runtime     routeControlRuntimeSnapshot
}

func newRouteControl(config Config, manager *routeManager, audit adminaudit.Recorder, logger *zap.Logger) *routeControl {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &routeControl{
		manager:     manager,
		loader:      config.UpstreamRoutesFile.Loader,
		gatewayNode: config.GatewayNode,
		audit:       audit,
		logger:      logger,
	}
}

func (c *routeControl) Status() routeControlSnapshot {
	if c == nil {
		return routeControlSnapshot{}
	}
	runtime := c.runtimeSnapshot()
	if c.manager == nil || c.loader == nil {
		return routeControlSnapshot{Runtime: runtime}
	}
	snapshot := c.manager.Snapshot()
	return routeControlSnapshot{
		Enabled:      true,
		Closed:       snapshot.Closed,
		DrainTimeout: c.manager.drainTimeout,
		Active:       snapshot.Active,
		Retiring:     snapshot.Retiring,
		Runtime:      runtime,
	}
}

func (c *routeControl) ActiveRoutes() []UpstreamRouteConfig {
	if c == nil || c.manager == nil {
		return nil
	}
	return c.manager.activeRoutes()
}

func (c *routeControl) Execute(
	ctx context.Context,
	options routeReloadOptions,
	actor routeReloadActor,
) (routeReloadOutcome, error) {
	startedAt := time.Now()
	options.Trigger = normalizeRouteReloadTrigger(options.Trigger)
	c.beginAttempt(startedAt)
	outcome := routeReloadOutcome{
		Trigger:            options.Trigger,
		DryRun:             options.DryRun,
		ExpectedGeneration: options.ExpectedGeneration,
	}
	if c == nil || c.manager == nil || c.loader == nil {
		return c.completeAttempt(outcome, errRouteReloadDisabled, startedAt, actor)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	managerSnapshot := c.manager.Snapshot()
	if managerSnapshot.Active != nil {
		outcome.Old = *managerSnapshot.Active
	}
	build := func(buildCtx context.Context) (*routeGeneration, error) {
		outcome.Old = c.manager.activeSnapshot()
		snapshot, err := c.loader(buildCtx)
		if err != nil {
			return nil, errors.Join(errRouteCandidateLoadFailed, err)
		}
		outcome.WarningCount = len(snapshot.Warnings)
		candidate, err := buildRouteGeneration(snapshot.Routes)
		if err != nil {
			return nil, errors.Join(errRouteCandidateBuildFailed, err)
		}
		outcome.Candidate = snapshotRouteGeneration(candidate)
		return candidate, nil
	}

	var operationErr error
	if options.DryRun {
		outcome.Candidate, operationErr = c.manager.DryRun(ctx, options.ExpectedGeneration, build)
	} else {
		outcome.Active, operationErr = c.manager.Reload(ctx, options.ExpectedGeneration, build)
	}
	current := c.manager.Snapshot()
	if current.Active != nil {
		outcome.Active = *current.Active
	}
	outcome.Retiring = current.Retiring
	if outcome.Candidate.Fingerprint != "" && outcome.Old.Fingerprint != "" {
		outcome.Changed = outcome.Candidate.Fingerprint != outcome.Old.Fingerprint
	}
	return c.completeAttempt(outcome, operationErr, startedAt, actor)
}

func finishRouteReloadOutcome(outcome routeReloadOutcome, err error, startedAt time.Time) routeReloadOutcome {
	outcome.Duration = time.Since(startedAt)
	if err == nil {
		outcome.Code = "ok"
		outcome.HTTPStatus = 200
		if outcome.DryRun {
			outcome.Result = "validated"
			outcome.Reason = "route candidate is valid"
			outcome.Stage = routeReloadStageValidation
		} else {
			outcome.Result = "reloaded"
			outcome.Reason = "route candidate was activated"
			outcome.Stage = routeReloadStageActivation
		}
		return outcome
	}

	outcome.Code, outcome.HTTPStatus, outcome.Reason, outcome.Stage = classifyRouteReloadError(err)
	outcome.Result = outcome.Code
	return outcome
}

func classifyRouteReloadError(err error) (code string, status int, reason string, stage string) {
	var loadErr *UpstreamRouteLoadError
	if errors.As(err, &loadErr) {
		switch loadErr.Stage {
		case UpstreamRouteLoadStageSourceRead:
			return "source_read_failed", 422, "the configured route source could not be read", routeReloadStageSourceRead
		case UpstreamRouteLoadStageParse:
			return "parse_failed", 422, "the configured route file could not be parsed", routeReloadStageParse
		case UpstreamRouteLoadStageValidation:
			return "validation_failed", 422, "the configured route file did not pass validation", routeReloadStageValidation
		}
	}
	switch {
	case errors.Is(err, errRouteReloadDisabled):
		return "reload_disabled", 409, "route reload is not enabled for this gateway", routeReloadStagePrecondition
	case errors.Is(err, errRouteReloadBusy):
		return "reload_busy", 409, "the previous route generation is still retiring", routeReloadStagePrecondition
	case errors.Is(err, errRouteGenerationConflict):
		return "generation_conflict", 409, "the active route generation changed before this operation", routeReloadStagePrecondition
	case errors.Is(err, errRouteCandidateLoadFailed):
		return "candidate_load_failed", 422, "the configured route candidate could not be loaded", routeReloadStageOperation
	case errors.Is(err, errRouteCandidateBuildFailed), errors.Is(err, errRouteCandidateInvalid):
		return "candidate_build_failed", 422, "the validated route candidate could not be constructed", routeReloadStageCandidateBuild
	case errors.Is(err, context.DeadlineExceeded):
		return "reload_failed", 504, "the route reload operation timed out", routeReloadStageOperation
	case errors.Is(err, context.Canceled):
		return "reload_failed", 408, "the route reload operation was canceled", routeReloadStageOperation
	case errors.Is(err, errRouteManagerClosed):
		return "reload_failed", 503, "the route manager is shutting down", routeReloadStageOperation
	default:
		return "reload_failed", 500, "the route reload operation failed", routeReloadStageOperation
	}
}

func (c *routeControl) beginAttempt(startedAt time.Time) {
	if c == nil {
		return
	}
	c.runtimeMu.Lock()
	c.runtime.OperationsInFlight++
	c.runtime.LastStartedAt = startedAt.UTC()
	c.runtimeMu.Unlock()
}

func (c *routeControl) completeAttempt(
	outcome routeReloadOutcome,
	err error,
	startedAt time.Time,
	actor routeReloadActor,
) (routeReloadOutcome, error) {
	outcome = finishRouteReloadOutcome(outcome, err, startedAt)
	c.finishAttempt(startedAt, outcome)
	metrics.RecordRouteReload(outcome.Trigger, outcome.Result, outcome.Duration, startedAt.Add(outcome.Duration))
	c.record(outcome, actor)
	return outcome, err
}

func (c *routeControl) finishAttempt(startedAt time.Time, outcome routeReloadOutcome) {
	if c == nil {
		return
	}
	completedAt := startedAt.Add(outcome.Duration).UTC()
	attempt := routeReloadAttemptSnapshot{
		Operation:            routeReloadOperation(outcome.DryRun),
		Trigger:              outcome.Trigger,
		Stage:                outcome.Stage,
		Result:               outcome.Result,
		Reason:               outcome.Reason,
		StartedAt:            startedAt.UTC(),
		CompletedAt:          completedAt,
		Duration:             outcome.Duration,
		ExpectedGeneration:   outcome.ExpectedGeneration,
		OldGeneration:        outcome.Old.Number,
		CandidateGeneration:  outcome.Candidate.Number,
		CandidateFingerprint: outcome.Candidate.Fingerprint,
		CandidateRouteCount:  outcome.Candidate.RouteCount,
		ActiveGeneration:     outcome.Active.Number,
		Changed:              outcome.Changed,
		WarningCount:         outcome.WarningCount,
	}

	c.runtimeMu.Lock()
	if c.runtime.OperationsInFlight > 0 {
		c.runtime.OperationsInFlight--
	}
	if outcome.HTTPStatus < 400 {
		c.runtime.LastSuccessAt = completedAt
	} else {
		c.runtime.LastFailureAt = completedAt
	}
	c.runtime.RecentAttempts = append(c.runtime.RecentAttempts, routeReloadAttemptSnapshot{})
	copy(c.runtime.RecentAttempts[1:], c.runtime.RecentAttempts)
	c.runtime.RecentAttempts[0] = attempt
	if len(c.runtime.RecentAttempts) > routeReloadHistoryLimit {
		c.runtime.RecentAttempts = c.runtime.RecentAttempts[:routeReloadHistoryLimit]
	}
	c.runtimeMu.Unlock()
}

func (c *routeControl) runtimeSnapshot() routeControlRuntimeSnapshot {
	if c == nil {
		return routeControlRuntimeSnapshot{}
	}
	c.runtimeMu.RLock()
	defer c.runtimeMu.RUnlock()
	out := c.runtime
	out.RecentAttempts = append([]routeReloadAttemptSnapshot(nil), c.runtime.RecentAttempts...)
	return out
}

func routeReloadOperation(dryRun bool) string {
	if dryRun {
		return "validate"
	}
	return "reload"
}

func normalizeRouteReloadTrigger(trigger string) string {
	if strings.EqualFold(strings.TrimSpace(trigger), routeReloadTriggerSIGHUP) {
		return routeReloadTriggerSIGHUP
	}
	return routeReloadTriggerAdminAPI
}

func (c *routeControl) record(outcome routeReloadOutcome, actor routeReloadActor) {
	if c == nil {
		return
	}
	action := "route_reload"
	if outcome.DryRun {
		action = "route_reload_validate"
	}
	details := map[string]string{
		"trigger":               outcome.Trigger,
		"stage":                 outcome.Stage,
		"dry_run":               strconv.FormatBool(outcome.DryRun),
		"expected_generation":   strconv.FormatUint(outcome.ExpectedGeneration, 10),
		"changed":               strconv.FormatBool(outcome.Changed),
		"warning_count":         strconv.Itoa(outcome.WarningCount),
		"duration_ms":           strconv.FormatInt(outcome.Duration.Milliseconds(), 10),
		"old_generation":        strconv.FormatUint(outcome.Old.Number, 10),
		"active_generation":     strconv.FormatUint(outcome.Active.Number, 10),
		"candidate_routes":      strconv.Itoa(outcome.Candidate.RouteCount),
		"candidate_fingerprint": outcome.Candidate.Fingerprint,
	}
	if outcome.Active.Fingerprint != "" {
		details["active_fingerprint"] = outcome.Active.Fingerprint
	}
	if outcome.Retiring != nil {
		details["retiring_generation"] = strconv.FormatUint(outcome.Retiring.Number, 10)
	}

	adminaudit.Record(c.audit, adminaudit.Entry{
		Action:         action,
		Result:         outcome.Result,
		HTTPStatus:     outcome.HTTPStatus,
		GatewayNode:    c.gatewayNode,
		AuthMode:       actor.AuthMode,
		Principal:      actor.Principal,
		Role:           actor.Role,
		AdminSessionID: actor.AdminSessionID,
		AuthKeyID:      actor.AuthKeyID,
		Method:         actor.Method,
		Path:           actor.Path,
		RemoteAddr:     actor.RemoteAddr,
		Permission:     routeReloadPermission(actor),
		Reason:         outcome.Reason,
		Details:        details,
	})

	fields := []zap.Field{
		zap.String("audit_event", action),
		zap.String("result", outcome.Result),
		zap.String("trigger", outcome.Trigger),
		zap.Bool("dry_run", outcome.DryRun),
		zap.Bool("changed", outcome.Changed),
		zap.Uint64("old_generation", outcome.Old.Number),
		zap.Uint64("active_generation", outcome.Active.Number),
		zap.Int("candidate_routes", outcome.Candidate.RouteCount),
		zap.Int("warning_count", outcome.WarningCount),
		zap.Duration("duration", outcome.Duration),
	}
	if outcome.HTTPStatus >= 400 {
		c.logger.Warn("route reload operation completed", fields...)
		return
	}
	c.logger.Info("route reload operation completed", fields...)
}

func routeReloadPermission(actor routeReloadActor) string {
	if actor.AuthMode == httpauth.ModeAdminSession {
		return adminPermissionRouteReload
	}
	return ""
}
