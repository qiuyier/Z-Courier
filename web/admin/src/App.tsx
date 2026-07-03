import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import {
  ArrowClockwise,
  ArrowSquareOut,
  ChartLineUp,
  CheckCircle,
  Circuitry,
  Database,
  DownloadSimple,
  FileText,
  Gauge,
  GitBranch,
  PlugsConnected,
  LockKey,
  MagnifyingGlass,
  Pulse,
  RadioButton,
  ShieldCheck,
  Warning,
  XCircle,
} from "@phosphor-icons/react";
import {
  APIError,
  discardMessage,
  fetchAdminCheck,
  fetchAdminSession,
  fetchClientRoute,
  fetchDiagnosisBundle,
  fetchDiagnostics,
  fetchMessage,
  fetchMessages,
  fetchOverview,
  fetchRoutes,
  fetchSessions,
  loginAdminSession,
  logoutAdminSession,
  requeueMessage,
} from "./api";
import type {
  AdminCheck,
  AdminCheckResult,
  AdminClientRouteLookup,
  AdminConsoleSession,
  AdminDiagnosisBundle,
  AdminDiagnostics,
  AdminMonitoringLinks,
  AdminMessages,
  AdminOverview,
  AdminRoute,
  AdminRoutes,
  AdminSession,
  AdminSessions,
  Dependency,
  MessageStatus,
  MessageStatusResponse,
} from "./types";

const messageStatuses: MessageStatus[] = ["failed", "pending", "sent", "delivered", "discarded"];
const messageRepairPermission = "message:repair";

type MetricContext = {
  label: string;
  metric: string;
  promql: string;
  hint: string;
};

const overviewMetricContexts: MetricContext[] = [
  {
    label: "Readiness",
    metric: "z_courier_gateway_readiness",
    promql: 'z_courier_gateway_readiness{status="ready"}',
    hint: "Use this to confirm whether this gateway should receive traffic.",
  },
  {
    label: "Online Sessions",
    metric: "z_courier_sessions_online",
    promql: "z_courier_sessions_online",
    hint: "Tracks local TCP sessions on the selected gateway node.",
  },
  {
    label: "Unique Clients",
    metric: "z_courier_clients_online",
    promql: "z_courier_clients_online",
    hint: "Separates client cardinality from multi-device session count.",
  },
  {
    label: "Upstream Forwarding",
    metric: "z_courier_upstream_forward_total",
    promql: "sum by (route, target_type, result) (rate(z_courier_upstream_forward_total[5m]))",
    hint: "Shows whether client packets are reaching HTTP or NSQ upstream routes.",
  },
];

const diagnosticsMetricContexts: MetricContext[] = [
  {
    label: "Route Degradation",
    metric: "z_courier_upstream_route_degraded",
    promql: "z_courier_upstream_route_degraded",
    hint: "One-hot route state for HTTP upstream routes with consecutive failures.",
  },
  {
    label: "Internal HTTP Pressure",
    metric: "z_courier_internal_http_inflight",
    promql: "sum by (path) (z_courier_internal_http_inflight)",
    hint: "Useful when backend push, admin, or peer requests compete for capacity.",
  },
  {
    label: "Retry Worker",
    metric: "z_courier_downlink_retry_messages_total",
    promql: "sum by (result) (rate(z_courier_downlink_retry_messages_total[5m]))",
    hint: "Confirms whether queued downlink messages are being retried or failing.",
  },
  {
    label: "Peer Push",
    metric: "z_courier_cluster_peer_push_total",
    promql: "sum by (target_node, result) (rate(z_courier_cluster_peer_push_total[5m]))",
    hint: "Use this when a client is connected to a different gateway node.",
  },
  {
    label: "ACK Latency",
    metric: "z_courier_downlink_ack_latency_seconds",
    promql: "histogram_quantile(0.99, sum by (le, msg_id) (rate(z_courier_downlink_ack_latency_seconds_bucket[5m])))",
    hint: "Tracks the tail latency between gateway send and client ACK.",
  },
  {
    label: "Rate Limit Rejects",
    metric: "z_courier_rate_limit_rejected_total",
    promql: "sum by (msg_id) (rate(z_courier_rate_limit_rejected_total[5m]))",
    hint: "Separates legitimate load from packets blocked by gateway rate limiting.",
  },
];

type RemoteState<T> =
  | { status: "idle"; data?: undefined; error?: undefined }
  | { status: "loading"; data?: T; error?: undefined }
  | { status: "ready"; data: T; error?: undefined }
  | { status: "error"; data?: T; error: string };

type PageID = "overview" | "routes" | "sessions" | "messages" | "checks" | "diagnostics";
type MessageAction = "requeue" | "discard";
type ClientRouteStatus = "idle" | "local" | "remote" | "offline" | "stale";
type AuthState =
  | { status: "checking"; session?: undefined; error?: string }
  | { status: "anonymous"; session?: undefined; error?: string }
  | { status: "authenticated"; session: AdminConsoleSession; error?: undefined };
type MessageActionDialogState = {
  action: MessageAction;
  message: MessageStatusResponse;
  reason: string;
  error?: string;
} | null;

const navItems = [
  { id: "overview" as const, label: "Overview", icon: Pulse, disabled: false },
  { id: "routes" as const, label: "Routes", icon: GitBranch, disabled: false },
  { id: "sessions" as const, label: "Sessions", icon: RadioButton, disabled: false },
  { id: "messages" as const, label: "Messages", icon: Database, disabled: false },
  { id: "checks" as const, label: "Checks", icon: ShieldCheck, disabled: false },
  { id: "diagnostics" as const, label: "Diagnostics", icon: Gauge, disabled: false },
];

export default function App() {
  const [authState, setAuthState] = useState<AuthState>({ status: "checking" });
  const [loginToken, setLoginToken] = useState("");
  const [loginPending, setLoginPending] = useState(false);
  const [state, setState] = useState<RemoteState<AdminOverview>>({ status: "idle" });
  const [routeState, setRouteState] = useState<RemoteState<AdminRoutes>>({ status: "idle" });
  const [sessionsState, setSessionsState] = useState<RemoteState<AdminSessions>>({ status: "idle" });
  const [clientRouteState, setClientRouteState] = useState<RemoteState<AdminClientRouteLookup>>({ status: "idle" });
  const [diagnosticsState, setDiagnosticsState] = useState<RemoteState<AdminDiagnostics>>({ status: "idle" });
  const [diagnosisBundleState, setDiagnosisBundleState] = useState<RemoteState<AdminDiagnosisBundle>>({ status: "idle" });
  const [checkState, setCheckState] = useState<RemoteState<AdminCheck>>({ status: "idle" });
  const [messagesState, setMessagesState] = useState<RemoteState<AdminMessages>>({ status: "idle" });
  const [messageLookupState, setMessageLookupState] = useState<RemoteState<MessageStatusResponse>>({ status: "idle" });
  const [checkTimeout, setCheckTimeout] = useState("2s");
  const [checkRanAt, setCheckRanAt] = useState<Date | null>(null);
  const [sessionClientID, setSessionClientID] = useState("");
  const [sessionDeviceID, setSessionDeviceID] = useState("");
  const [sessionLimit, setSessionLimit] = useState(100);
  const [diagnosisClientID, setDiagnosisClientID] = useState("");
  const [diagnosisDeviceID, setDiagnosisDeviceID] = useState("");
  const [diagnosisProbeTimeout, setDiagnosisProbeTimeout] = useState("2s");
  const [diagnosisMessageLimit, setDiagnosisMessageLimit] = useState(20);
  const [diagnosisSessionLimit, setDiagnosisSessionLimit] = useState(100);
  const [messageStatus, setMessageStatus] = useState<MessageStatus>("failed");
  const [messageLimit, setMessageLimit] = useState(100);
  const [messageLookupID, setMessageLookupID] = useState("");
  const [messageActionDialog, setMessageActionDialog] = useState<MessageActionDialogState>(null);
  const [messageActionPending, setMessageActionPending] = useState(false);
  const [activePage, setActivePage] = useState<PageID>("overview");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const authenticated = authState.status === "authenticated";
  const canRepairMessages = authState.status === "authenticated" && adminSessionAllows(authState.session, messageRepairPermission);

  const clearConsoleState = useCallback(() => {
    setState({ status: "idle" });
    setRouteState({ status: "idle" });
    setSessionsState({ status: "idle" });
    setClientRouteState({ status: "idle" });
    setDiagnosticsState({ status: "idle" });
    setDiagnosisBundleState({ status: "idle" });
    setCheckState({ status: "idle" });
    setMessagesState({ status: "idle" });
    setMessageLookupState({ status: "idle" });
    setMessageActionDialog(null);
    setCheckRanAt(null);
    setUpdatedAt(null);
  }, []);

  const expireSession = useCallback(() => {
    clearConsoleState();
    setAuthState({ status: "anonymous", error: "Session expired. Sign in again." });
  }, [clearConsoleState]);

  const refresh = useCallback(
    async (signal?: AbortSignal) => {
      if (!authenticated) {
        setState({ status: "idle" });
        return;
      }
      setState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchOverview(signal);
        setState({ status: "ready", data });
        setUpdatedAt(new Date());
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession],
  );

  const refreshRoutes = useCallback(
    async (signal?: AbortSignal) => {
      if (!authenticated) {
        setRouteState({ status: "idle" });
        return;
      }
      setRouteState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchRoutes(signal);
        setRouteState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setRouteState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession],
  );

  const refreshSessions = useCallback(
    async (signal?: AbortSignal) => {
      if (!authenticated) {
        setSessionsState({ status: "idle" });
        return;
      }
      setSessionsState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchSessions(sessionClientID, sessionLimit, signal);
        setSessionsState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setSessionsState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession, sessionClientID, sessionLimit],
  );

  const lookupClientRoute = useCallback(
    async (clientIDOverride?: string, deviceIDOverride?: string, signal?: AbortSignal) => {
      const clientID = (clientIDOverride ?? sessionClientID).trim();
      const deviceID = (deviceIDOverride ?? sessionDeviceID).trim();
      if (!authenticated || clientID === "" || deviceID === "") {
        setClientRouteState({ status: "idle" });
        return;
      }
      setClientRouteState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchClientRoute(clientID, deviceID, signal);
        setClientRouteState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setClientRouteState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession, sessionClientID, sessionDeviceID],
  );

  const lookupSessionRoute = useCallback(
    (session: AdminSession) => {
      setSessionClientID(session.client_id);
      setSessionDeviceID(session.device_id);
      void lookupClientRoute(session.client_id, session.device_id);
    },
    [lookupClientRoute],
  );

  const refreshDiagnostics = useCallback(
    async (signal?: AbortSignal) => {
      if (!authenticated) {
        setDiagnosticsState({ status: "idle" });
        return;
      }
      setDiagnosticsState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchDiagnostics(signal);
        setDiagnosticsState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setDiagnosticsState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession],
  );

  const downloadDiagnosisBundle = useCallback(async () => {
    if (!authenticated) {
      setDiagnosisBundleState({ status: "idle" });
      return;
    }
    if (diagnosisDeviceID.trim() !== "" && diagnosisClientID.trim() === "") {
      setDiagnosisBundleState({ status: "error", error: "client_id is required when device_id is set" });
      return;
    }

    setDiagnosisBundleState((current) => ({ status: "loading", data: current.data }));
    try {
      const data = await fetchDiagnosisBundle({
        clientID: diagnosisClientID,
        deviceID: diagnosisDeviceID,
        messageLimit: diagnosisMessageLimit,
        probeTimeout: diagnosisProbeTimeout,
        sessionLimit: diagnosisSessionLimit,
      });
      setDiagnosisBundleState({ status: "ready", data });
      downloadJSON(data, diagnosisBundleFilename(data));
    } catch (error) {
      if (isUnauthorized(error)) {
        expireSession();
        return;
      }
      const message = requestErrorMessage(error);
      setDiagnosisBundleState((current) => ({ status: "error", data: current.data, error: message }));
    }
  }, [
    authenticated,
    diagnosisClientID,
    diagnosisDeviceID,
    diagnosisMessageLimit,
    diagnosisProbeTimeout,
    diagnosisSessionLimit,
    expireSession,
  ]);

  const runCheck = useCallback(
    async (signal?: AbortSignal) => {
      if (!authenticated) {
        setCheckState({ status: "idle" });
        return;
      }
      setCheckState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchAdminCheck(checkTimeout, signal);
        setCheckState({ status: "ready", data });
        setCheckRanAt(new Date());
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setCheckState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, checkTimeout, expireSession],
  );

  const refreshMessages = useCallback(
    async (signal?: AbortSignal) => {
      if (!authenticated) {
        setMessagesState({ status: "idle" });
        return;
      }
      setMessagesState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchMessages(messageStatus, messageLimit, signal);
        setMessagesState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setMessagesState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession, messageLimit, messageStatus],
  );

  const lookupMessage = useCallback(
    async (signal?: AbortSignal) => {
      const messageID = messageLookupID.trim();
      if (!authenticated || messageID === "") {
        setMessageLookupState({ status: "idle" });
        return;
      }
      setMessageLookupState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchMessage(messageID, signal);
        setMessageLookupState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          expireSession();
          return;
        }
        const message = requestErrorMessage(error);
        setMessageLookupState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [authenticated, expireSession, messageLookupID],
  );

  const openMessageAction = useCallback((action: MessageAction, message: MessageStatusResponse) => {
    if (!canRepairMessages) {
      return;
    }
    setMessageActionDialog({ action, message, reason: "" });
  }, [canRepairMessages]);

  const closeMessageAction = useCallback(() => {
    if (messageActionPending) {
      return;
    }
    setMessageActionDialog(null);
  }, [messageActionPending]);

  const updateMessageActionReason = useCallback((reason: string) => {
    setMessageActionDialog((current) => current ? { ...current, reason, error: undefined } : current);
  }, []);

  const confirmMessageAction = useCallback(async () => {
    if (!messageActionDialog || !authenticated || !canRepairMessages) {
      return;
    }

    const messageID = messageActionDialog.message.message_id?.trim() ?? "";
    const reason = messageActionDialog.reason.trim();
    if (messageID === "") {
      setMessageActionDialog((current) => current ? { ...current, error: "message_id is required" } : current);
      return;
    }
    if (messageActionDialog.action === "discard" && reason === "") {
      setMessageActionDialog((current) => current ? { ...current, error: "discard reason is required" } : current);
      return;
    }

    setMessageActionPending(true);
    try {
      const updated =
        messageActionDialog.action === "requeue"
          ? await requeueMessage(messageID)
          : await discardMessage(messageID, reason);
      if (messageLookupID.trim() === messageID) {
        setMessageLookupState({ status: "ready", data: updated });
      }
      setMessageActionDialog(null);
      await refreshMessages();
    } catch (error) {
      if (isUnauthorized(error)) {
        expireSession();
        return;
      }
      const message = requestErrorMessage(error);
      setMessageActionDialog((current) => current ? { ...current, error: message } : current);
    } finally {
      setMessageActionPending(false);
    }
  }, [authenticated, canRepairMessages, expireSession, messageActionDialog, messageLookupID, refreshMessages]);

  useEffect(() => {
    const controller = new AbortController();

    void fetchAdminSession(controller.signal)
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        if (response.session) {
          setAuthState({ status: "authenticated", session: response.session });
          return;
        }
        setAuthState({ status: "anonymous" });
      })
      .catch((error) => {
        if (controller.signal.aborted) {
          return;
        }
        if (isUnauthorized(error)) {
          setAuthState({ status: "anonymous" });
          return;
        }
        setAuthState({ status: "anonymous", error: requestErrorMessage(error) });
      });

    return () => {
      controller.abort();
    };
  }, []);

  useEffect(() => {
    if (!authenticated) {
      setState({ status: "idle" });
      return;
    }

    const controller = new AbortController();
    void refresh(controller.signal);
    const timer = window.setInterval(() => {
      void refresh(controller.signal);
    }, 15000);

    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [authenticated, refresh]);

  useEffect(() => {
    if (activePage !== "routes") {
      return;
    }
    if (!authenticated) {
      setRouteState({ status: "idle" });
      return;
    }

    const controller = new AbortController();
    void refreshRoutes(controller.signal);
    return () => {
      controller.abort();
    };
  }, [activePage, authenticated, refreshRoutes]);

  useEffect(() => {
    if (activePage !== "sessions") {
      return;
    }
    if (!authenticated) {
      setSessionsState({ status: "idle" });
      setClientRouteState({ status: "idle" });
      return;
    }

    const controller = new AbortController();
    void refreshSessions(controller.signal);
    return () => {
      controller.abort();
    };
  }, [activePage, authenticated, refreshSessions]);

  useEffect(() => {
    if (activePage !== "diagnostics") {
      return;
    }
    if (!authenticated) {
      setDiagnosticsState({ status: "idle" });
      return;
    }

    const controller = new AbortController();
    void refreshDiagnostics(controller.signal);
    return () => {
      controller.abort();
    };
  }, [activePage, authenticated, refreshDiagnostics]);

  useEffect(() => {
    if (activePage !== "messages") {
      return;
    }
    if (!authenticated) {
      setMessagesState({ status: "idle" });
      return;
    }

    const controller = new AbortController();
    void refreshMessages(controller.signal);
    return () => {
      controller.abort();
    };
  }, [activePage, authenticated, refreshMessages]);

  const login = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      const token = loginToken.trim();
      if (token === "") {
        setAuthState({ status: "anonymous", error: "internal token is required" });
        return;
      }

      setLoginPending(true);
      try {
        const response = await loginAdminSession(token);
        if (!response.session) {
          throw new Error("login response did not include a session");
        }
        setAuthState({ status: "authenticated", session: response.session });
        setLoginToken("");
      } catch (error) {
        clearConsoleState();
        setAuthState({ status: "anonymous", error: requestErrorMessage(error) });
      } finally {
        setLoginPending(false);
      }
    },
    [clearConsoleState, loginToken],
  );

  const logout = useCallback(async () => {
    setLoginPending(true);
    try {
      await logoutAdminSession();
    } catch {
      // Local UI state is still cleared so a stale browser session is not trusted.
    } finally {
      clearConsoleState();
      setAuthState({ status: "anonymous" });
      setLoginPending(false);
    }
  }, [clearConsoleState]);

  const refreshCurrentPage = useCallback(() => {
    if (activePage === "routes") {
      void refreshRoutes();
      return;
    }
    if (activePage === "sessions") {
      void refreshSessions();
      if (sessionClientID.trim() !== "" && sessionDeviceID.trim() !== "") {
        void lookupClientRoute();
      }
      return;
    }
    if (activePage === "diagnostics") {
      void refreshDiagnostics();
      return;
    }
    if (activePage === "messages") {
      void refreshMessages();
      return;
    }
    if (activePage === "checks") {
      void runCheck();
      return;
    }
    void refresh();
  }, [
    activePage,
    lookupClientRoute,
    refresh,
    refreshDiagnostics,
    refreshMessages,
    refreshRoutes,
    refreshSessions,
    runCheck,
    sessionClientID,
    sessionDeviceID,
  ]);

  const overview = state.data;
  const ready = overview?.readiness.ready ?? false;
  const statusText =
    overview?.readiness.status ??
    (authState.status === "checking" ? "checking session" : state.status === "error" ? "not connected" : authenticated ? "connecting" : "auth required");
  const pageTitle =
    activePage === "routes"
      ? "Routes"
      : activePage === "sessions"
        ? "Sessions"
        : activePage === "messages"
          ? "Messages"
          : activePage === "checks"
            ? "Checks"
            : activePage === "diagnostics"
              ? "Diagnostics"
              : "Operations Overview";

  return (
    <main className="min-h-[100dvh] bg-mist text-ink">
      <div className="mx-auto grid max-w-[1400px] grid-cols-1 gap-5 px-4 py-4 md:px-6 lg:grid-cols-[236px_minmax(0,1fr)] lg:py-6">
        <aside className="min-w-0 rounded-lg border border-line bg-white/86 p-3 shadow-diffusion backdrop-blur">
          <div className="flex items-center gap-3 px-2 py-2">
            <div className="grid size-10 place-items-center rounded-lg border border-line bg-zinc-950 text-white">
              <Circuitry size={20} weight="duotone" />
            </div>
            <div>
              <p className="text-sm font-semibold tracking-tight">Z-Courier</p>
              <p className="text-xs text-zinc-500">Admin Console</p>
            </div>
          </div>

          <nav className="mt-5 grid gap-1">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = item.id === activePage;
              return (
                <button
                  key={item.label}
                  className={[
                    "group flex items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition duration-300 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-45",
                    active
                      ? "bg-zinc-950 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.08)]"
                      : "text-zinc-600 hover:bg-zinc-100 hover:text-ink",
                  ].join(" ")}
                  disabled={item.disabled}
                  onClick={() => setActivePage(item.id)}
                  type="button"
                >
                  <Icon size={18} weight={active ? "duotone" : "regular"} />
                  <span className="min-w-0 truncate">{item.label}</span>
                </button>
              );
            })}
          </nav>

          <section className="mt-6 min-w-0 overflow-hidden rounded-lg border border-line bg-zinc-50 p-3">
            {authState.status === "authenticated" ? (
              <div className="grid min-w-0 gap-3">
                <div className="flex min-w-0 items-center gap-2">
                  <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-emerald-50 text-emerald-700">
                    <ShieldCheck size={16} weight="duotone" />
                  </div>
                  <div className="min-w-0">
                    <p className="truncate text-sm font-semibold text-ink">{authState.session.principal}</p>
                    <p className="truncate font-mono text-xs text-zinc-500">{authState.session.role}</p>
                  </div>
                </div>
                <div className="grid min-w-0 gap-2 rounded-lg border border-line bg-white p-3">
                  <LineItem label="Expires" value={formatMillis(authState.session.expires_in_ms)} />
                  <LineItem label="Session" value={shortID(authState.session.session_id)} />
                </div>
                <button
                  className="inline-flex w-full min-w-0 items-center justify-center gap-2 rounded-lg border border-line bg-white px-3 py-2 text-sm font-medium text-ink transition duration-300 hover:border-zinc-300 hover:bg-zinc-50 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-45"
                  disabled={loginPending}
                  onClick={() => void logout()}
                  type="button"
                >
                  <XCircle size={16} className="shrink-0" weight="bold" />
                  <span className="min-w-0 truncate">{loginPending ? "Signing out..." : "Sign Out"}</span>
                </button>
              </div>
            ) : (
              <form className="grid min-w-0 gap-2" onSubmit={login}>
                <label className="block text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="internal-token">
                  Internal Token
                </label>
                <div className="flex min-w-0 items-center gap-2 overflow-hidden rounded-lg border border-line bg-white px-2.5 py-2 focus-within:border-accent">
                  <LockKey size={16} className="shrink-0 text-zinc-400" weight="duotone" />
                  <input
                    autoComplete="current-password"
                    disabled={authState.status === "checking" || loginPending}
                    id="internal-token"
                    className="w-full min-w-0 flex-1 truncate bg-transparent text-sm outline-none placeholder:text-zinc-400 disabled:cursor-not-allowed disabled:opacity-60"
                    onChange={(event) => setLoginToken(event.target.value)}
                    placeholder="dev-internal-token"
                    type="password"
                    value={loginToken}
                  />
                </div>
                <button
                  className="mt-1 inline-flex w-full min-w-0 items-center justify-center gap-2 rounded-lg bg-zinc-950 px-3 py-2 text-sm font-medium text-white transition duration-300 hover:bg-zinc-800 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-45"
                  disabled={authState.status === "checking" || loginPending}
                  type="submit"
                >
                  <PlugsConnected size={16} className="shrink-0" weight="bold" />
                  <span className="min-w-0 truncate">
                    {authState.status === "checking" ? "Checking..." : loginPending ? "Signing in..." : "Sign In"}
                  </span>
                </button>
                {authState.error && <p className="min-w-0 break-words font-mono text-xs leading-relaxed text-amber-700">{authState.error}</p>}
              </form>
            )}
          </section>
        </aside>

        <section className="grid min-w-0 gap-5">
          <header className="flex flex-col justify-between gap-4 rounded-lg border border-line bg-white px-5 py-4 shadow-diffusion md:flex-row md:items-center">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.16em] text-zinc-500">Gateway Control Plane</p>
              <h1 className="mt-1 text-2xl font-semibold tracking-tight text-ink md:text-3xl">{pageTitle}</h1>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <StatusPill ready={ready} status={statusText} />
              <button
                className="inline-flex items-center gap-2 rounded-lg border border-line bg-white px-3 py-2 text-sm font-medium text-ink transition duration-300 hover:border-zinc-300 hover:bg-zinc-50 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-45"
                disabled={!authenticated}
                onClick={refreshCurrentPage}
                type="button"
              >
                <ArrowClockwise size={16} weight="bold" />
                Refresh
              </button>
            </div>
          </header>

          {state.status === "idle" && !authenticated && <AuthEmptyState checking={authState.status === "checking"} />}

          {activePage === "overview" && state.status === "error" && <ErrorBanner message={state.error} />}
          {activePage === "routes" && routeState.status === "error" && <ErrorBanner message={routeState.error} />}
          {activePage === "sessions" && sessionsState.status === "error" && <ErrorBanner message={sessionsState.error} />}
          {activePage === "sessions" && clientRouteState.status === "error" && <ErrorBanner message={clientRouteState.error} />}
          {activePage === "messages" && messagesState.status === "error" && <ErrorBanner message={messagesState.error} />}
          {activePage === "checks" && checkState.status === "error" && <ErrorBanner message={checkState.error} />}
          {activePage === "diagnostics" && diagnosticsState.status === "error" && <ErrorBanner message={diagnosticsState.error} />}

          {activePage === "overview" && overview ? (
            <Dashboard overview={overview} updatedAt={updatedAt} />
          ) : activePage === "overview" && state.status === "loading" ? (
            <OverviewSkeleton />
          ) : activePage === "routes" && authenticated && (routeState.status !== "error" || routeState.data) ? (
            <RoutesPage state={routeState} />
          ) : activePage === "sessions" && authenticated && (sessionsState.status !== "error" || sessionsState.data) ? (
            <SessionsPage
              clientID={sessionClientID}
              deviceID={sessionDeviceID}
              limit={sessionLimit}
              onClientIDChange={setSessionClientID}
              onDeviceIDChange={setSessionDeviceID}
              onLimitChange={setSessionLimit}
              onLookupRoute={() => lookupClientRoute()}
              onSessionRouteLookup={lookupSessionRoute}
              onSessionsRefresh={refreshSessions}
              routeState={clientRouteState}
              state={sessionsState}
            />
          ) : activePage === "messages" && authenticated && (messagesState.status !== "error" || messagesState.data) ? (
            <MessagesPage
              canRepairMessages={canRepairMessages}
              limit={messageLimit}
              lookupID={messageLookupID}
              lookupState={messageLookupState}
              onLimitChange={setMessageLimit}
              onMessageAction={openMessageAction}
              onLookupIDChange={setMessageLookupID}
              onLookupSubmit={lookupMessage}
              onStatusChange={setMessageStatus}
              selectedStatus={messageStatus}
              state={messagesState}
            />
          ) : activePage === "checks" && authenticated && (checkState.status !== "error" || checkState.data) ? (
            <ChecksPage
              onRun={runCheck}
              onTimeoutChange={setCheckTimeout}
              ranAt={checkRanAt}
              state={checkState}
              timeout={checkTimeout}
            />
          ) : activePage === "diagnostics" && authenticated && (diagnosticsState.status !== "error" || diagnosticsState.data) ? (
            <DiagnosticsPage
              bundleState={diagnosisBundleState}
              clientID={diagnosisClientID}
              deviceID={diagnosisDeviceID}
              messageLimit={diagnosisMessageLimit}
              onClientIDChange={setDiagnosisClientID}
              onDeviceIDChange={setDiagnosisDeviceID}
              onDownloadBundle={downloadDiagnosisBundle}
              onMessageLimitChange={setDiagnosisMessageLimit}
              onProbeTimeoutChange={setDiagnosisProbeTimeout}
              onSessionLimitChange={setDiagnosisSessionLimit}
              probeTimeout={diagnosisProbeTimeout}
              sessionLimit={diagnosisSessionLimit}
              state={diagnosticsState}
            />
          ) : null}
        </section>
      </div>
      {messageActionDialog && (
        <MessageActionDialog
          dialog={messageActionDialog}
          onClose={closeMessageAction}
          onConfirm={confirmMessageAction}
          onReasonChange={updateMessageActionReason}
          pending={messageActionPending}
        />
      )}
    </main>
  );
}

function Dashboard({ overview, updatedAt }: { overview: AdminOverview; updatedAt: Date | null }) {
  const dependencies = overview.dependencies ?? [];
  const unhealthyDependencies = useMemo(
    () => dependencies.filter((dependency) => dependency.status !== "ok"),
    [dependencies],
  );

  return (
    <div className="grid gap-5 xl:grid-cols-[1.35fr_0.85fr]">
      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="flex flex-col justify-between gap-4 md:flex-row md:items-start">
          <div>
            <p className="text-sm font-medium text-zinc-500">Node</p>
            <h2 className="mt-1 text-3xl font-semibold tracking-tight md:text-4xl">{overview.gateway_node}</h2>
          </div>
          <div className="rounded-lg border border-line bg-zinc-50 px-3 py-2 text-right">
            <p className="text-xs uppercase tracking-[0.14em] text-zinc-500">Last Refresh</p>
            <p className="mt-1 font-mono text-sm">{updatedAt ? updatedAt.toLocaleTimeString() : "--"}</p>
          </div>
        </div>

        <div className="mt-8 grid gap-4 md:grid-cols-[1.2fr_0.8fr]">
          <MetricBlock
            icon={<RadioButton size={18} weight="duotone" />}
            label="Online Sessions"
            value={overview.sessions.online}
            detail={`${overview.sessions.unique_clients} unique clients`}
          />
          <MetricBlock
            icon={<ChartLineUp size={18} weight="duotone" />}
            label="Upstream Routes"
            value={overview.upstream.routes}
            detail="active route definitions"
          />
        </div>
      </section>

      <section className="rounded-lg border border-line bg-zinc-950 p-5 text-white shadow-diffusion">
        <div className="flex items-center justify-between gap-3">
          <div>
            <p className="text-sm text-zinc-400">Readiness</p>
            <h2 className="mt-1 text-2xl font-semibold tracking-tight">{overview.readiness.status}</h2>
          </div>
          <div
            className={[
              "grid size-11 place-items-center rounded-lg",
              overview.readiness.ready ? "bg-accent/18 text-emerald-200" : "bg-amber-400/18 text-amber-200",
            ].join(" ")}
          >
            {overview.readiness.ready ? <CheckCircle size={22} weight="duotone" /> : <Warning size={22} weight="duotone" />}
          </div>
        </div>
        <div className="mt-7 grid gap-3 border-t border-white/10 pt-4">
          <LineItem label="Internal HTTP" value={overview.internal_http.enabled ? "enabled" : "disabled"} />
          <LineItem label="Auth Mode" value={overview.internal_http.auth_mode ?? "--"} />
          <LineItem label="Max In-Flight" value={overview.internal_http.max_in_flight?.toLocaleString() ?? "--"} />
        </div>
      </section>

      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion xl:col-span-2">
        <div className="grid gap-5 lg:grid-cols-[0.9fr_1.1fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Cluster</p>
            <h2 className="mt-1 text-2xl font-semibold tracking-tight">
              {overview.cluster.enabled ? "Enabled" : "Single Node"}
            </h2>
            <div className="mt-6 grid gap-3">
              <LineItem label="Registry" value={overview.cluster.registry_type ?? "--"} />
              <LineItem label="Route TTL" value={overview.cluster.registry_ttl ?? "--"} />
              <LineItem label="Peer Auth" value={overview.cluster.peer_auth_mode ?? "--"} />
            </div>
          </div>

          <div className="rounded-lg border border-line bg-zinc-50 p-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <p className="text-sm font-medium text-zinc-500">Dependencies</p>
                <h3 className="mt-1 text-xl font-semibold tracking-tight">
                  {unhealthyDependencies.length === 0 ? "Healthy" : `${unhealthyDependencies.length} attention needed`}
                </h3>
              </div>
              <ShieldCheck size={24} className="text-accent" weight="duotone" />
            </div>
            <DependencyList dependencies={dependencies} />
          </div>
        </div>
      </section>

      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion xl:col-span-2">
        <div className="grid gap-5 md:grid-cols-[1.1fr_0.9fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Downlink Delivery</p>
            <h2 className="mt-1 text-2xl font-semibold tracking-tight">{overview.downlink.storage_type}</h2>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <LineItem label="Store" value={overview.downlink.store_configured ? "configured" : "not configured"} />
            <LineItem label="Retry Interval" value={overview.downlink.retry_interval ?? "--"} />
            <LineItem label="Retry Delay" value={overview.downlink.retry_delay ?? "--"} />
            <LineItem label="ACK Timeout" value={overview.downlink.ack_timeout ?? "--"} />
          </div>
        </div>
      </section>

      <MonitoringContextPanel
        className="xl:col-span-2"
        description="Open the configured monitoring surfaces or copy the PromQL snippets into Prometheus when investigating this gateway node."
        metrics={overviewMetricContexts}
        monitoring={overview.admin_console?.monitoring}
        title="Monitoring Context"
      />
    </div>
  );
}

function RoutesPage({ state }: { state: RemoteState<AdminRoutes> }) {
  const routes = state.data?.routes ?? [];
  const httpRoutes = routes.filter((route) => route.target_type === "http").length;
  const nsqRoutes = routes.filter((route) => route.target_type === "nsq").length;
  const limitedRoutes = routes.filter((route) => (route.max_in_flight ?? 0) > 0).length;

  if (state.status === "loading" && !state.data) {
    return <RoutesSkeleton />;
  }

  if (state.status !== "loading" && routes.length === 0) {
    return <RoutesEmptyState />;
  }

  return (
    <div className="grid gap-5">
      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="grid gap-5 lg:grid-cols-[0.85fr_1.15fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Route Table</p>
            <h2 className="mt-1 font-mono text-5xl tracking-tight text-ink">{state.data?.total ?? routes.length}</h2>
            <p className="mt-2 text-sm text-zinc-500">gateway node: {state.data?.gateway_node ?? "--"}</p>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <MetricRow label="HTTP targets" value={httpRoutes.toLocaleString()} />
            <MetricRow label="NSQ targets" value={nsqRoutes.toLocaleString()} />
            <MetricRow label="Capacity limits" value={limitedRoutes.toLocaleString()} />
            <MetricRow label="Refresh state" value={state.status} />
          </div>
        </div>
      </section>

      <section className="grid gap-3">
        {routes.map((route, index) => (
          <RouteCard key={`${route.name}-${route.msg_id_min}-${route.msg_id_max ?? "single"}`} route={route} index={index} />
        ))}
      </section>
    </div>
  );
}

function SessionsPage({
  clientID,
  deviceID,
  limit,
  onClientIDChange,
  onDeviceIDChange,
  onLimitChange,
  onLookupRoute,
  onSessionRouteLookup,
  onSessionsRefresh,
  routeState,
  state,
}: {
  clientID: string;
  deviceID: string;
  limit: number;
  onClientIDChange: (clientID: string) => void;
  onDeviceIDChange: (deviceID: string) => void;
  onLimitChange: (limit: number) => void;
  onLookupRoute: () => void | Promise<void>;
  onSessionRouteLookup: (session: AdminSession) => void;
  onSessionsRefresh: () => void | Promise<void>;
  routeState: RemoteState<AdminClientRouteLookup>;
  state: RemoteState<AdminSessions>;
}) {
  const sessions = state.data?.sessions ?? [];
  const total = state.data?.total ?? sessions.length;
  const uniqueClients = state.data?.unique_clients ?? new Set(sessions.map((session) => session.client_id)).size;
  const routeStatus = clientRouteStatus(routeState.data);
  const loadingSessions = state.status === "loading";
  const loadingRoute = routeState.status === "loading";

  if (state.status === "loading" && !state.data) {
    return <SessionsSkeleton />;
  }

  return (
    <div className="grid gap-5">
      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="grid gap-5 xl:grid-cols-[0.95fr_1.05fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Local Sessions</p>
            <div className="mt-2 flex flex-wrap items-end gap-3">
              <h2 className="font-mono text-5xl tracking-tight text-ink">{total.toLocaleString()}</h2>
              <RouteStatusBadge status={routeStatus} />
            </div>
            <p className="mt-2 text-sm text-zinc-500">gateway node: {state.data?.gateway_node ?? "--"}</p>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            <MetricRow label="Loaded" value={sessions.length.toLocaleString()} />
            <MetricRow label="Unique Clients" value={uniqueClients.toLocaleString()} />
            <MetricRow label="Limit" value={(state.data?.limit ?? limit).toLocaleString()} />
            <MetricRow label="Refresh State" value={state.status} />
          </div>
        </div>
      </section>

      <section className="grid gap-5 xl:grid-cols-[0.82fr_1.18fr]">
        <article className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
          <form
            className="grid gap-3"
            onSubmit={(event) => {
              event.preventDefault();
              void onSessionsRefresh();
            }}
          >
            <p className="text-sm font-medium text-zinc-500">Session Filter</p>
            <label className="mt-2 text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="session-client-filter">
              ClientID
            </label>
            <input
              className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 placeholder:text-zinc-400 focus:border-accent"
              id="session-client-filter"
              onChange={(event) => onClientIDChange(event.target.value)}
              placeholder="load-client-0"
              value={clientID}
            />

            <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="session-limit">
              Limit
            </label>
            <input
              className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 focus:border-accent"
              id="session-limit"
              max={1000}
              min={1}
              onChange={(event) => onLimitChange(clampSessionLimit(event.target.value))}
              type="number"
              value={limit}
            />

            <button
              className="mt-2 inline-flex items-center justify-center gap-2 rounded-lg bg-zinc-950 px-4 py-2 text-sm font-medium text-white transition duration-300 hover:bg-zinc-800 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-50"
              disabled={loadingSessions}
              type="submit"
            >
              <ArrowClockwise size={16} weight="bold" />
              {loadingSessions ? "Loading..." : "Refresh Sessions"}
            </button>
          </form>
        </article>

        <SessionRouteLookupPanel
          clientID={clientID}
          deviceID={deviceID}
          onClientIDChange={onClientIDChange}
          onDeviceIDChange={onDeviceIDChange}
          onLookupRoute={onLookupRoute}
          routeState={routeState}
          running={loadingRoute}
        />
      </section>

      {state.status !== "loading" && sessions.length === 0 ? (
        <SessionsEmptyState filtered={(state.data?.client_id ?? "") !== ""} />
      ) : (
        <section className="grid gap-3">
          {sessions.map((session, index) => (
            <SessionCard
              index={index}
              key={`${session.session_id}-${session.conn_id}`}
              onLookupRoute={onSessionRouteLookup}
              session={session}
            />
          ))}
        </section>
      )}
    </div>
  );
}

function SessionRouteLookupPanel({
  clientID,
  deviceID,
  onClientIDChange,
  onDeviceIDChange,
  onLookupRoute,
  routeState,
  running,
}: {
  clientID: string;
  deviceID: string;
  onClientIDChange: (clientID: string) => void;
  onDeviceIDChange: (deviceID: string) => void;
  onLookupRoute: () => void | Promise<void>;
  routeState: RemoteState<AdminClientRouteLookup>;
  running: boolean;
}) {
  return (
    <article className="rounded-lg border border-line bg-zinc-950 p-5 text-white shadow-diffusion">
      <form
        className="grid gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          void onLookupRoute();
        }}
      >
        <p className="text-sm text-zinc-400">Client Route Lookup</p>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="grid gap-2">
            <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-400" htmlFor="route-client-id">
              ClientID
            </label>
            <input
              className="min-w-0 rounded-lg border border-white/10 bg-white/10 px-3 py-2 font-mono text-sm text-white outline-none transition duration-300 placeholder:text-zinc-500 focus:border-emerald-400"
              id="route-client-id"
              onChange={(event) => onClientIDChange(event.target.value)}
              placeholder="load-client-0"
              value={clientID}
            />
          </div>
          <div className="grid gap-2">
            <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-400" htmlFor="route-device-id">
              DeviceID
            </label>
            <input
              className="min-w-0 rounded-lg border border-white/10 bg-white/10 px-3 py-2 font-mono text-sm text-white outline-none transition duration-300 placeholder:text-zinc-500 focus:border-emerald-400"
              id="route-device-id"
              onChange={(event) => onDeviceIDChange(event.target.value)}
              placeholder="device-0"
              value={deviceID}
            />
          </div>
        </div>
        <button
          className="inline-flex items-center justify-center gap-2 rounded-lg bg-white px-4 py-2 text-sm font-medium text-zinc-950 transition duration-300 hover:bg-zinc-100 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-45"
          disabled={clientID.trim() === "" || deviceID.trim() === "" || running}
          type="submit"
        >
          <MagnifyingGlass size={16} weight="bold" />
          {running ? "Looking..." : "Lookup Route"}
        </button>
      </form>

      {routeState.status === "loading" && (
        <div className="mt-5 rounded-lg border border-white/10 bg-white/10 p-4">
          <div className="h-4 w-36 rounded bg-white/10" />
          <div className="mt-4 grid gap-2">
            <div className="h-10 rounded bg-white/10" />
            <div className="h-10 rounded bg-white/10" />
          </div>
        </div>
      )}

      {routeState.data && <ClientRouteResult lookup={routeState.data} />}
    </article>
  );
}

function ClientRouteResult({ lookup }: { lookup: AdminClientRouteLookup }) {
  const status = clientRouteStatus(lookup);
  const local = lookup.local_session;
  const clusterRoute = lookup.cluster_route;

  return (
    <div className="mt-5 grid gap-4">
      <div className="rounded-lg border border-white/10 bg-white/10 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <RouteStatusBadge status={status} />
            <h3 className="mt-3 break-words font-mono text-lg font-semibold tracking-tight">
              {lookup.client_id || "--"} / {lookup.device_id || "--"}
            </h3>
          </div>
          <p className="font-mono text-xs text-zinc-400">{lookup.gateway_node}</p>
        </div>
        <div className="mt-4 grid gap-3 border-t border-white/10 pt-4 md:grid-cols-2">
          <DarkLineItem label="Local Session" value={lookup.local_session_found ? "found" : "missing"} />
          <DarkLineItem label="Cluster" value={lookup.cluster_enabled ? "enabled" : "disabled"} />
          <DarkLineItem label="Cluster Route" value={lookup.cluster_route_found ? "found" : "missing"} />
          <DarkLineItem label="Resolved As" value={status} />
        </div>
      </div>

      {local && (
        <div className="rounded-lg border border-emerald-300/20 bg-emerald-300/10 p-4">
          <p className="text-sm font-medium text-emerald-100">Local Session</p>
          <div className="mt-4 grid gap-3 md:grid-cols-2">
            <DarkLineItem label="SessionID" value={local.session_id || "--"} />
            <DarkLineItem label="ConnID" value={local.conn_id?.toLocaleString() ?? "--"} />
            <DarkLineItem label="Gateway" value={local.gateway_node || "--"} />
            <DarkLineItem label="Token" value={local.token_id || "--"} />
            <DarkLineItem label="Connected" value={formatOptionalDate(local.connected_at)} />
            <DarkLineItem label="Last Seen" value={formatOptionalDate(local.last_seen_at)} />
          </div>
        </div>
      )}

      {clusterRoute && (
        <div className="rounded-lg border border-sky-300/20 bg-sky-300/10 p-4">
          <p className="text-sm font-medium text-sky-100">Cluster Route</p>
          <div className="mt-4 grid gap-3 md:grid-cols-2">
            <DarkLineItem label="Gateway" value={clusterRoute.gateway_node || "--"} />
            <DarkLineItem label="Internal" value={clusterRoute.internal_addr || "--"} />
            <DarkLineItem label="SessionID" value={clusterRoute.session_id || "--"} />
            <DarkLineItem label="Token" value={clusterRoute.token_id || "--"} />
            <DarkLineItem label="Updated" value={formatOptionalDate(clusterRoute.updated_at)} />
            <DarkLineItem label="Expires In" value={formatMillis(clusterRoute.expires_in_ms)} />
          </div>
        </div>
      )}

      {!local && !clusterRoute && (
        <div className="rounded-lg border border-white/10 bg-white/10 px-4 py-5">
          <p className="font-medium text-white">No online route found</p>
          <p className="mt-1 text-sm text-zinc-400">
            {lookup.cluster_enabled ? "No local session or cluster route matched this client/device." : "Cluster registry is disabled on this gateway."}
          </p>
        </div>
      )}
    </div>
  );
}

function SessionCard({
  index,
  onLookupRoute,
  session,
}: {
  index: number;
  onLookupRoute: (session: AdminSession) => void;
  session: AdminSession;
}) {
  return (
    <article
      className="animate-rise overflow-hidden rounded-lg border border-line bg-white shadow-diffusion"
      style={{ animationDelay: `${index * 45}ms` }}
    >
      <div className="grid gap-4 p-4 lg:grid-cols-[0.75fr_1.25fr] lg:p-5">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge label="online" tone="ok" />
            <span className="rounded-md border border-line bg-zinc-50 px-2.5 py-1 font-mono text-xs text-zinc-600">
              conn {session.conn_id?.toLocaleString() ?? "--"}
            </span>
          </div>
          <h3 className="mt-4 break-words font-mono text-lg font-semibold tracking-tight">{session.client_id || "--"}</h3>
          <p className="mt-2 break-words text-sm text-zinc-500">{session.device_id || "--"}</p>
          <button
            className="mt-5 inline-flex items-center justify-center gap-2 rounded-lg border border-line bg-white px-3 py-2 text-xs font-medium text-ink transition duration-300 hover:border-zinc-300 hover:bg-zinc-50 active:translate-y-px"
            onClick={() => onLookupRoute(session)}
            type="button"
          >
            <MagnifyingGlass size={14} weight="bold" />
            Lookup Route
          </button>
        </div>

        <div className="grid min-w-0 gap-3 md:grid-cols-2 xl:grid-cols-3">
          <MessageField label="SessionID" value={session.session_id || "--"} wide />
          <MessageField label="Gateway" value={session.gateway_node || "--"} />
          <MessageField label="Token" value={session.token_id || "--"} />
          <MessageField label="Connected" value={formatOptionalDate(session.connected_at)} />
          <MessageField label="Last Seen" value={formatOptionalDate(session.last_seen_at)} />
          <MessageField label="DeviceID" value={session.device_id || "--"} />
        </div>
      </div>
    </article>
  );
}

function ChecksPage({
  onRun,
  onTimeoutChange,
  ranAt,
  state,
  timeout,
}: {
  onRun: () => void | Promise<void>;
  onTimeoutChange: (timeout: string) => void;
  ranAt: Date | null;
  state: RemoteState<AdminCheck>;
  timeout: string;
}) {
  const checks = state.data?.checks ?? [];
  const counts = checkCounts(checks);
  const aggregateStatus = state.data?.status ?? "skipped";
  const running = state.status === "loading";

  return (
    <div className="grid gap-5">
      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="grid gap-5 xl:grid-cols-[0.95fr_1.05fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Dependency Check</p>
            <div className="mt-2 flex flex-wrap items-end gap-3">
              <h2 className="font-mono text-5xl tracking-tight text-ink">{checks.length.toLocaleString()}</h2>
              <CheckStatusBadge status={aggregateStatus} />
            </div>
            <p className="mt-2 text-sm text-zinc-500">gateway node: {state.data?.gateway_node ?? "--"}</p>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            <MetricRow label="OK" value={counts.ok.toLocaleString()} />
            <MetricRow label="Degraded" value={counts.degraded.toLocaleString()} />
            <MetricRow label="Failed" value={counts.failed.toLocaleString()} />
            <MetricRow label="Skipped" value={counts.skipped.toLocaleString()} />
          </div>
        </div>
      </section>

      <section className="grid gap-5 xl:grid-cols-[0.8fr_1.2fr]">
        <article className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
          <p className="text-sm font-medium text-zinc-500">Run Check</p>
          <label className="mt-5 block text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="check-timeout">
            Timeout
          </label>
          <div className="mt-2 grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
            <input
              className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 focus:border-accent"
              id="check-timeout"
              onChange={(event) => onTimeoutChange(event.target.value)}
              placeholder="2s"
              value={timeout}
            />
            <button
              className="inline-flex items-center justify-center gap-2 rounded-lg bg-zinc-950 px-4 py-2 text-sm font-medium text-white transition duration-300 hover:bg-zinc-800 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-50"
              disabled={running}
              onClick={() => void onRun()}
              type="button"
            >
              <ShieldCheck size={16} weight="bold" />
              {running ? "Checking..." : "Run Check"}
            </button>
          </div>
          <p className="mt-3 text-xs text-zinc-500">Examples: 1s, 2s, 5s. Max timeout is 30s.</p>
        </article>

        <article className="rounded-lg border border-line bg-zinc-950 p-5 text-white shadow-diffusion">
          <p className="text-sm text-zinc-400">Last Result</p>
          <h3 className="mt-1 text-2xl font-semibold tracking-tight">{state.data?.status ?? "not run"}</h3>
          <div className="mt-6 grid gap-3 border-t border-white/10 pt-4">
            <DarkLineItem label="Timeout" value={state.data?.timeout || timeout || "--"} />
            <DarkLineItem label="Last Run" value={ranAt ? ranAt.toLocaleTimeString() : "--"} />
            <DarkLineItem label="Refresh State" value={state.status} />
            <DarkLineItem label="Checks" value={checks.length.toLocaleString()} />
          </div>
        </article>
      </section>

      {state.status === "loading" && !state.data ? (
        <ChecksSkeleton />
      ) : checks.length === 0 ? (
        <ChecksEmptyState onRun={onRun} running={running} />
      ) : (
        <section className="grid gap-3">
          {checks.map((check, index) => (
            <CheckResultCard check={check} index={index} key={`${check.name}-${index}`} />
          ))}
        </section>
      )}
    </div>
  );
}

function CheckResultCard({ check, index }: { check: AdminCheckResult; index: number }) {
  return (
    <article
      className="animate-rise overflow-hidden rounded-lg border border-line bg-white shadow-diffusion"
      style={{ animationDelay: `${index * 45}ms` }}
    >
      <div className="grid gap-4 p-4 lg:grid-cols-[0.7fr_1.3fr] lg:p-5">
        <div className="min-w-0">
          <CheckStatusBadge status={check.status} />
          <h3 className="mt-4 break-words font-mono text-lg font-semibold tracking-tight">{check.name}</h3>
          <p className="mt-2 break-words text-sm text-zinc-500">{check.target || "--"}</p>
        </div>
        <div className="grid min-w-0 gap-3 md:grid-cols-2">
          <MessageField label="Latency" value={check.latency || "--"} />
          <MessageField label="Status" value={check.status} />
          <MessageField label="Target" value={check.target || "--"} />
          <MessageField label="Error" value={check.error || "--"} wide />
        </div>
      </div>
    </article>
  );
}

function CheckStatusBadge({ status }: { status?: string }) {
  const normalized = status || "--";
  const tone =
    normalized === "ok"
      ? "bg-emerald-50 text-emerald-800"
      : normalized === "degraded"
        ? "bg-amber-50 text-amber-800"
        : normalized === "failed"
          ? "bg-rose-50 text-rose-800"
          : "bg-zinc-100 text-zinc-700";

  return (
    <span className={["inline-flex items-center gap-2 rounded-md px-2.5 py-1 font-mono text-xs font-medium uppercase", tone].join(" ")}>
      <span
        className={[
          "size-1.5 rounded-full",
          normalized === "ok"
            ? "bg-accent"
            : normalized === "degraded"
              ? "bg-amber-500"
              : normalized === "failed"
                ? "bg-rose-500"
                : "bg-zinc-400",
        ].join(" ")}
      />
      {normalized}
    </span>
  );
}

function RouteStatusBadge({ status }: { status: ClientRouteStatus }) {
  const tone =
    status === "local"
      ? "bg-emerald-50 text-emerald-800"
      : status === "remote"
        ? "bg-sky-50 text-sky-800"
        : status === "stale"
          ? "bg-amber-50 text-amber-800"
          : status === "offline"
            ? "bg-rose-50 text-rose-800"
            : "bg-zinc-100 text-zinc-700";
  const dot =
    status === "local"
      ? "bg-accent"
      : status === "remote"
        ? "bg-sky-500"
        : status === "stale"
          ? "bg-amber-500"
          : status === "offline"
            ? "bg-rose-500"
            : "bg-zinc-400";

  return (
    <span className={["inline-flex items-center gap-2 rounded-md px-2.5 py-1 font-mono text-xs font-medium uppercase", tone].join(" ")}>
      <span className={["size-1.5 rounded-full", dot].join(" ")} />
      {status}
    </span>
  );
}

function MessagesPage({
  canRepairMessages,
  limit,
  lookupID,
  lookupState,
  onLimitChange,
  onLookupIDChange,
  onLookupSubmit,
  onMessageAction,
  onStatusChange,
  selectedStatus,
  state,
}: {
  canRepairMessages: boolean;
  limit: number;
  lookupID: string;
  lookupState: RemoteState<MessageStatusResponse>;
  onLimitChange: (limit: number) => void;
  onLookupIDChange: (messageID: string) => void;
  onLookupSubmit: () => void | Promise<void>;
  onMessageAction: (action: MessageAction, message: MessageStatusResponse) => void;
  onStatusChange: (status: MessageStatus) => void;
  selectedStatus: MessageStatus;
  state: RemoteState<AdminMessages>;
}) {
  const messages = state.data?.messages ?? [];
  const total = state.data?.total ?? messages.length;
  const ackRequired = messages.filter((message) => message.ack_required).length;
  const retryScheduled = messages.filter((message) => Boolean(message.next_retry_at)).length;

  if (state.status === "loading" && !state.data) {
    return <MessagesSkeleton />;
  }

  return (
    <div className="grid gap-5">
      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="grid gap-5 xl:grid-cols-[0.95fr_1.05fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Stored Messages</p>
            <div className="mt-2 flex flex-wrap items-end gap-3">
              <h2 className="font-mono text-5xl tracking-tight text-ink">{total.toLocaleString()}</h2>
              <MessageStatusBadge status={selectedStatus} />
            </div>
            <div className="mt-5 flex flex-wrap gap-2" role="tablist" aria-label="Message status">
              {messageStatuses.map((status) => {
                const active = status === selectedStatus;
                return (
                  <button
                    aria-pressed={active}
                    className={[
                      "rounded-lg border px-3 py-2 text-sm font-medium capitalize transition duration-300 active:translate-y-px",
                      active
                        ? "border-zinc-950 bg-zinc-950 text-white"
                        : "border-line bg-white text-zinc-600 hover:border-zinc-300 hover:bg-zinc-50 hover:text-ink",
                    ].join(" ")}
                    key={status}
                    onClick={() => onStatusChange(status)}
                    type="button"
                  >
                    {status}
                  </button>
                );
              })}
            </div>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            <MetricRow label="Loaded" value={messages.length.toLocaleString()} />
            <MetricRow label="Limit" value={limit.toLocaleString()} />
            <MetricRow label="ACK Required" value={ackRequired.toLocaleString()} />
            <MetricRow label="Retry Scheduled" value={retryScheduled.toLocaleString()} />
          </div>
        </div>
      </section>

      <section className="grid gap-5 xl:grid-cols-[0.8fr_1.2fr]">
        <article className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
          <p className="text-sm font-medium text-zinc-500">List Filter</p>
          <label className="mt-5 block text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="message-limit">
            Limit
          </label>
          <input
            className="mt-2 w-full rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 focus:border-accent"
            id="message-limit"
            max={1000}
            min={1}
            onChange={(event) => onLimitChange(clampMessageLimit(event.target.value))}
            type="number"
            value={limit}
          />
          <p className="mt-3 text-xs text-zinc-500">Gateway caps list responses at 1000 rows.</p>
        </article>

        <MessageLookupPanel
          canRepairMessages={canRepairMessages}
          lookupID={lookupID}
          lookupState={lookupState}
          onMessageAction={onMessageAction}
          onLookupIDChange={onLookupIDChange}
          onLookupSubmit={onLookupSubmit}
        />
      </section>

      {state.status !== "loading" && messages.length === 0 ? (
        <MessagesEmptyState status={selectedStatus} />
      ) : (
        <section className="grid gap-3">
          {messages.map((message, index) => (
            <MessageCard
              key={message.message_id || `${message.status}-${index}`}
              message={message}
              index={index}
              canRepairMessages={canRepairMessages}
              onAction={onMessageAction}
            />
          ))}
        </section>
      )}
    </div>
  );
}

function MessageLookupPanel({
  canRepairMessages,
  lookupID,
  lookupState,
  onMessageAction,
  onLookupIDChange,
  onLookupSubmit,
}: {
  canRepairMessages: boolean;
  lookupID: string;
  lookupState: RemoteState<MessageStatusResponse>;
  onMessageAction: (action: MessageAction, message: MessageStatusResponse) => void;
  onLookupIDChange: (messageID: string) => void;
  onLookupSubmit: () => void | Promise<void>;
}) {
  return (
    <article className="rounded-lg border border-line bg-zinc-950 p-5 text-white shadow-diffusion">
      <form
        className="grid gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          void onLookupSubmit();
        }}
      >
        <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-400" htmlFor="message-lookup">
          MessageID
        </label>
        <div className="grid gap-2 md:grid-cols-[minmax(0,1fr)_auto]">
          <input
            className="min-w-0 rounded-lg border border-white/10 bg-white/10 px-3 py-2 font-mono text-sm text-white outline-none transition duration-300 placeholder:text-zinc-500 focus:border-emerald-400"
            id="message-lookup"
            onChange={(event) => onLookupIDChange(event.target.value)}
            placeholder="message-1"
            value={lookupID}
          />
          <button
            className="inline-flex items-center justify-center gap-2 rounded-lg bg-white px-3 py-2 text-sm font-medium text-zinc-950 transition duration-300 hover:bg-zinc-100 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-45"
            disabled={lookupID.trim() === "" || lookupState.status === "loading"}
            type="submit"
          >
            <MagnifyingGlass size={16} weight="bold" />
            Lookup
          </button>
        </div>
      </form>

      {lookupState.status === "loading" && (
        <div className="mt-5 rounded-lg border border-white/10 bg-white/10 p-4">
          <div className="h-4 w-36 rounded bg-white/10" />
          <div className="mt-4 grid gap-2">
            <div className="h-10 rounded bg-white/10" />
            <div className="h-10 rounded bg-white/10" />
          </div>
        </div>
      )}

      {lookupState.status === "error" && (
        <div className="mt-5 rounded-lg border border-amber-300/30 bg-amber-300/10 px-4 py-3 text-sm text-amber-100">
          <p className="font-semibold">Lookup failed</p>
          <p className="mt-1 break-words font-mono text-xs">{lookupState.error}</p>
        </div>
      )}

      {lookupState.data && (
        <div className="mt-5 rounded-lg border border-white/10 bg-white/10 p-4">
          <div className="flex min-w-0 items-center justify-between gap-3">
            <p className="truncate font-mono text-sm font-medium">{lookupState.data.message_id || "--"}</p>
            <MessageStatusBadge status={lookupState.data.status} />
          </div>
          <div className="mt-4 grid gap-3 md:grid-cols-2">
            <DarkLineItem label="Client" value={lookupState.data.client_id || "--"} />
            <DarkLineItem label="Device" value={lookupState.data.device_id || "--"} />
            <DarkLineItem label="MsgID" value={lookupState.data.msg_id?.toLocaleString() ?? "--"} />
            <DarkLineItem label="Attempts" value={lookupState.data.attempts?.toLocaleString() ?? "0"} />
          </div>
          <div className="mt-4 border-t border-white/10 pt-4">
            <MessageActionButtons canRepairMessages={canRepairMessages} message={lookupState.data} onAction={onMessageAction} variant="dark" />
          </div>
        </div>
      )}
    </article>
  );
}

function MessageCard({
  canRepairMessages,
  message,
  index,
  onAction,
}: {
  canRepairMessages: boolean;
  message: MessageStatusResponse;
  index: number;
  onAction: (action: MessageAction, message: MessageStatusResponse) => void;
}) {
  return (
    <article
      className="animate-rise overflow-hidden rounded-lg border border-line bg-white shadow-diffusion"
      style={{ animationDelay: `${index * 45}ms` }}
    >
      <div className="grid gap-4 p-4 lg:grid-cols-[0.75fr_1.25fr] lg:p-5">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <MessageStatusBadge status={message.status} />
            <span className="rounded-md border border-line bg-zinc-50 px-2.5 py-1 font-mono text-xs text-zinc-600">
              MsgID {message.msg_id?.toLocaleString() ?? "--"}
            </span>
          </div>
          <h3 className="mt-4 truncate font-mono text-lg font-semibold tracking-tight">{message.message_id || "--"}</h3>
          <p className="mt-2 truncate text-sm text-zinc-500">
            {message.client_id || "--"} / {message.device_id || "--"}
          </p>
          <div className="mt-5">
            <MessageActionButtons canRepairMessages={canRepairMessages} message={message} onAction={onAction} />
          </div>
        </div>

        <div className="grid min-w-0 gap-3 md:grid-cols-2 xl:grid-cols-3">
          <MessageField label="Attempts" value={message.attempts?.toLocaleString() ?? "0"} />
          <MessageField label="ACK" value={message.ack_required ? "required" : "not required"} />
          <MessageField label="Body" value={`${message.body_size_bytes?.toLocaleString() ?? 0} bytes`} />
          <MessageField label="Updated" value={formatOptionalDate(message.updated_at)} />
          <MessageField label="Next Retry" value={formatOptionalDate(message.next_retry_at)} />
          <MessageField label="Claim" value={message.claim_owner || "--"} />
          <MessageField label="Claim Until" value={formatOptionalDate(message.claim_until)} />
          <MessageField label="Sent" value={formatOptionalDate(message.sent_at)} />
          <MessageField label="Delivered" value={formatOptionalDate(message.delivered_at)} />
          <MessageField label="Last Error" value={message.last_error || "--"} wide />
        </div>
      </div>
    </article>
  );
}

function MessageActionButtons({
  canRepairMessages,
  message,
  onAction,
  variant = "light",
}: {
  canRepairMessages: boolean;
  message: MessageStatusResponse;
  onAction: (action: MessageAction, message: MessageStatusResponse) => void;
  variant?: "light" | "dark";
}) {
  const hasMessageID = Boolean(message.message_id);
  const canRequeueMessage = canRepairMessages && canRequeue(message.status) && hasMessageID;
  const canDiscardMessage = canRepairMessages && canDiscard(message.status) && hasMessageID;
  const requeueTitle = !canRepairMessages
    ? "Requires operator role"
    : canRequeueMessage
      ? "Requeue message"
      : "This message cannot be requeued";
  const discardTitle = !canRepairMessages
    ? "Requires operator role"
    : canDiscardMessage
      ? "Discard message"
      : "This message cannot be discarded";
  const lightRequeue =
    "border-line bg-white text-ink hover:border-zinc-300 hover:bg-zinc-50 disabled:bg-zinc-50 disabled:text-zinc-400";
  const lightDiscard =
    "border-rose-200 bg-rose-50 text-rose-800 hover:border-rose-300 hover:bg-rose-100 disabled:border-line disabled:bg-zinc-50 disabled:text-zinc-400";
  const darkRequeue =
    "border-white/10 bg-white/10 text-white hover:bg-white/15 disabled:text-zinc-500";
  const darkDiscard =
    "border-rose-300/30 bg-rose-300/10 text-rose-100 hover:bg-rose-300/15 disabled:border-white/10 disabled:bg-white/5 disabled:text-zinc-500";

  return (
    <div className="flex min-w-0 flex-wrap gap-2">
      <button
        className={[
          "inline-flex min-w-0 items-center justify-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition duration-300 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60",
          variant === "dark" ? darkRequeue : lightRequeue,
        ].join(" ")}
        disabled={!canRequeueMessage}
        onClick={() => onAction("requeue", message)}
        title={requeueTitle}
        type="button"
      >
        <ArrowClockwise size={14} weight="bold" />
        Requeue
      </button>
      <button
        className={[
          "inline-flex min-w-0 items-center justify-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition duration-300 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60",
          variant === "dark" ? darkDiscard : lightDiscard,
        ].join(" ")}
        disabled={!canDiscardMessage}
        onClick={() => onAction("discard", message)}
        title={discardTitle}
        type="button"
      >
        <XCircle size={14} weight="bold" />
        Discard
      </button>
    </div>
  );
}

function MessageActionDialog({
  dialog,
  onClose,
  onConfirm,
  onReasonChange,
  pending,
}: {
  dialog: NonNullable<MessageActionDialogState>;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  onReasonChange: (reason: string) => void;
  pending: boolean;
}) {
  const messageID = dialog.message.message_id || "--";
  const isDiscard = dialog.action === "discard";
  const confirmDisabled = pending || messageID === "--" || (isDiscard && dialog.reason.trim() === "");

  return (
    <div className="fixed inset-0 z-30 grid place-items-center bg-zinc-950/45 px-4 py-6 backdrop-blur-sm" role="dialog" aria-modal="true">
      <form
        className="w-full max-w-lg animate-rise overflow-hidden rounded-lg border border-white/10 bg-white shadow-[0_24px_80px_-32px_rgba(0,0,0,0.45)]"
        onSubmit={(event) => {
          event.preventDefault();
          void onConfirm();
        }}
      >
        <div className="border-b border-line bg-zinc-50 px-5 py-4">
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500">
            {isDiscard ? "Discard Message" : "Requeue Message"}
          </p>
          <h2 className="mt-2 break-words font-mono text-lg font-semibold tracking-tight text-ink">{messageID}</h2>
        </div>

        <div className="grid gap-4 p-5">
          <div className="rounded-lg border border-line bg-white px-4 py-3">
            <LineItem label="Current Status" value={dialog.message.status || "--"} />
            <LineItem label="Attempts" value={dialog.message.attempts?.toLocaleString() ?? "0"} />
            <LineItem label="Client" value={dialog.message.client_id || "--"} />
          </div>

          <p className="text-sm leading-relaxed text-zinc-600">
            {isDiscard
              ? "Discard stops retry delivery for this message and stores the reason as the latest error."
              : "Requeue moves this message back to pending and clears attempts, retry time, claim, sent state, and last error."}
          </p>

          {isDiscard && (
            <div className="grid gap-2">
              <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="discard-reason">
                Reason
              </label>
              <textarea
                className="min-h-24 resize-y rounded-lg border border-line bg-white px-3 py-2 text-sm outline-none transition duration-300 placeholder:text-zinc-400 focus:border-accent"
                id="discard-reason"
                onChange={(event) => onReasonChange(event.target.value)}
                placeholder="manual discard after backend confirmation"
                value={dialog.reason}
              />
              <p className="text-xs text-zinc-500">Required. This will be visible in the message last_error field.</p>
            </div>
          )}

          {dialog.error && (
            <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
              <p className="font-semibold">Action failed</p>
              <p className="mt-1 break-words font-mono text-xs">{dialog.error}</p>
            </div>
          )}
        </div>

        <div className="flex flex-col-reverse gap-2 border-t border-line bg-zinc-50 px-5 py-4 sm:flex-row sm:justify-end">
          <button
            className="inline-flex items-center justify-center rounded-lg border border-line bg-white px-4 py-2 text-sm font-medium text-ink transition duration-300 hover:bg-zinc-50 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-50"
            disabled={pending}
            onClick={onClose}
            type="button"
          >
            Cancel
          </button>
          <button
            className={[
              "inline-flex items-center justify-center rounded-lg px-4 py-2 text-sm font-medium text-white transition duration-300 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-50",
              isDiscard ? "bg-rose-700 hover:bg-rose-800" : "bg-zinc-950 hover:bg-zinc-800",
            ].join(" ")}
            disabled={confirmDisabled}
            type="submit"
          >
            {pending ? "Working..." : isDiscard ? "Discard" : "Requeue"}
          </button>
        </div>
      </form>
    </div>
  );
}

function MessageField({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return (
    <div className={["min-w-0 rounded-lg border border-line bg-zinc-50 px-3 py-2", wide ? "xl:col-span-2" : ""].join(" ")}>
      <p className="text-xs uppercase tracking-[0.14em] text-zinc-500">{label}</p>
      <p className="mt-1 break-words font-mono text-sm text-ink">{value}</p>
    </div>
  );
}

function MessageStatusBadge({ status }: { status?: MessageStatus | string }) {
  const normalized = status || "--";
  const tone =
    normalized === "delivered"
      ? "bg-emerald-50 text-emerald-800"
      : normalized === "failed" || normalized === "discarded"
        ? "bg-rose-50 text-rose-800"
        : normalized === "sent"
          ? "bg-sky-50 text-sky-800"
          : "bg-amber-50 text-amber-800";

  return (
    <span className={["rounded-md px-2.5 py-1 font-mono text-xs font-medium uppercase", tone].join(" ")}>
      {normalized}
    </span>
  );
}

function DiagnosticsPage({
  bundleState,
  clientID,
  deviceID,
  messageLimit,
  onClientIDChange,
  onDeviceIDChange,
  onDownloadBundle,
  onMessageLimitChange,
  onProbeTimeoutChange,
  onSessionLimitChange,
  probeTimeout,
  sessionLimit,
  state,
}: {
  bundleState: RemoteState<AdminDiagnosisBundle>;
  clientID: string;
  deviceID: string;
  messageLimit: number;
  onClientIDChange: (clientID: string) => void;
  onDeviceIDChange: (deviceID: string) => void;
  onDownloadBundle: () => void | Promise<void>;
  onMessageLimitChange: (limit: number) => void;
  onProbeTimeoutChange: (timeout: string) => void;
  onSessionLimitChange: (limit: number) => void;
  probeTimeout: string;
  sessionLimit: number;
  state: RemoteState<AdminDiagnostics>;
}) {
  const diagnostics = state.data;
  if (state.status === "loading" && !diagnostics) {
    return <DiagnosticsSkeleton />;
  }
  if (!diagnostics) {
    return <DiagnosticsEmptyState />;
  }

  const warnings = diagnostics.warnings ?? [];
  const httpRouteStates = diagnostics.upstream.http_route_states ?? [];
  const dependencyIssues = diagnostics.dependencies.filter((dependency) => !healthyDependencyStatus(dependency.status)).length;

  return (
    <div className="grid gap-5">
      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="grid gap-5 xl:grid-cols-[0.95fr_1.05fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Gateway Node</p>
            <h2 className="mt-1 text-3xl font-semibold tracking-tight md:text-4xl">{diagnostics.gateway_node}</h2>
            <div className="mt-5 flex flex-wrap gap-2">
              <StatusBadge label={diagnostics.readiness.status} tone={diagnostics.readiness.ready ? "ok" : "warn"} />
              <StatusBadge label={diagnostics.runtime.started ? "runtime started" : "runtime idle"} tone={diagnostics.runtime.started ? "ok" : "warn"} />
            </div>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            <MetricRow label="Uptime" value={diagnostics.runtime.uptime || "--"} />
            <MetricRow label="Online Sessions" value={diagnostics.sessions.online.toLocaleString()} />
            <MetricRow label="Unique Clients" value={diagnostics.sessions.unique_clients.toLocaleString()} />
            <MetricRow label="Warnings" value={warnings.length.toLocaleString()} />
          </div>
        </div>
      </section>

      <DiagnosisBundlePanel
        clientID={clientID}
        deviceID={deviceID}
        messageLimit={messageLimit}
        onClientIDChange={onClientIDChange}
        onDeviceIDChange={onDeviceIDChange}
        onDownload={onDownloadBundle}
        onMessageLimitChange={onMessageLimitChange}
        onProbeTimeoutChange={onProbeTimeoutChange}
        onSessionLimitChange={onSessionLimitChange}
        probeTimeout={probeTimeout}
        sessionLimit={sessionLimit}
        state={bundleState}
      />

      {warnings.length > 0 && (
        <section className="grid gap-3">
          {warnings.map((warning, index) => (
            <article
              className="animate-rise rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-amber-950"
              key={warning.code}
              style={{ animationDelay: `${index * 45}ms` }}
            >
              <div className="flex items-start gap-3">
                <Warning size={18} className="mt-0.5 shrink-0" weight="duotone" />
                <div className="min-w-0">
                  <p className="font-mono text-xs font-semibold uppercase tracking-[0.14em]">{warning.code}</p>
                  <p className="mt-1 break-words text-sm">{warning.message}</p>
                </div>
              </div>
            </article>
          ))}
        </section>
      )}

      <section className="grid gap-5 xl:grid-cols-[1.05fr_0.95fr]">
        <article className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
          <div className="flex items-start justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-zinc-500">Dependencies</p>
              <h3 className="mt-1 text-2xl font-semibold tracking-tight">
                {dependencyIssues === 0 ? "Healthy" : `${dependencyIssues} issue${dependencyIssues > 1 ? "s" : ""}`}
              </h3>
            </div>
            <ShieldCheck size={24} className="text-accent" weight="duotone" />
          </div>
          <DependencyList dependencies={diagnostics.dependencies} />
        </article>

        <article className="rounded-lg border border-line bg-zinc-950 p-5 text-white shadow-diffusion">
          <p className="text-sm text-zinc-400">Auth and Capacity</p>
          <h3 className="mt-1 text-2xl font-semibold tracking-tight">{diagnostics.auth.provider}</h3>
          <div className="mt-6 grid gap-3 border-t border-white/10 pt-4">
            <DarkLineItem label="Verifier" value={diagnostics.auth.verifier_loaded ? "loaded" : "missing"} />
            <DarkLineItem label="Rate Limit" value={diagnostics.capacity.rate_limit_enabled ? "enabled" : "disabled"} />
            <DarkLineItem label="Rate Window" value={diagnostics.capacity.rate_limit_window || "--"} />
            <DarkLineItem label="Internal In-Flight" value={diagnostics.capacity.internal_http_max_in_flight?.toLocaleString() ?? "--"} />
          </div>
        </article>
      </section>

      <section className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="grid gap-5 lg:grid-cols-[0.9fr_1.1fr]">
          <div>
            <p className="text-sm font-medium text-zinc-500">Traffic Runtime</p>
            <h3 className="mt-1 text-2xl font-semibold tracking-tight">{diagnostics.upstream.routes.toLocaleString()} upstream routes</h3>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <MetricRow label="HTTP Routes" value={diagnostics.upstream.http_routes.toLocaleString()} />
            <MetricRow label="NSQ Routes" value={diagnostics.upstream.nsq_routes.toLocaleString()} />
            <MetricRow label="Capacity-Limited" value={diagnostics.upstream.routes_with_capacity_limit.toLocaleString()} />
            <MetricRow label="Downlink Store" value={diagnostics.downlink.store_configured ? diagnostics.downlink.storage_type : "not configured"} />
          </div>
        </div>
      </section>

      <MonitoringContextPanel
        description="Use these signals beside dependency checks to separate gateway pressure, upstream failure, retry behavior, and cluster peer delivery issues."
        metrics={diagnosticsMetricContexts}
        monitoring={diagnostics.admin_console?.monitoring}
        title="Operational Signals"
      />

      {httpRouteStates.length > 0 && (
        <section className="grid gap-3">
          {httpRouteStates.map((route, index) => (
            <article
              className="animate-rise rounded-lg border border-line bg-white p-4 shadow-diffusion"
              key={route.name}
              style={{ animationDelay: `${index * 45}ms` }}
            >
              <div className="grid gap-4 lg:grid-cols-[0.75fr_1.25fr]">
                <div className="min-w-0">
                  <StatusBadge label={route.status} tone={route.status === "healthy" ? "ok" : "warn"} />
                  <h3 className="mt-3 truncate text-xl font-semibold tracking-tight">{route.name}</h3>
                  <p className="mt-1 text-sm text-zinc-500">{route.target_type}</p>
                </div>
                <div className="grid min-w-0 gap-3 md:grid-cols-2">
                  <MetricRow label="Failures" value={(route.consecutive_failures ?? 0).toLocaleString()} />
                  <MetricRow label="Updated" value={formatOptionalDate(route.updated_at)} />
                  <MetricRow label="Last Success" value={formatOptionalDate(route.last_success_at)} />
                  <MetricRow label="Reason" value={route.last_reason || "--"} />
                </div>
              </div>
            </article>
          ))}
        </section>
      )}

      <section className="grid gap-5 xl:grid-cols-2">
        <DiagnosticsConfigPanel title="Internal HTTP" rows={[
          ["Enabled", diagnostics.internal_http.enabled ? "enabled" : "disabled"],
          ["Address", diagnostics.internal_http.addr || "--"],
          ["Auth Mode", diagnostics.internal_http.auth_mode || "--"],
          ["Max Body", diagnostics.internal_http.max_request_body_size?.toLocaleString() ?? "--"],
        ]} />
        <DiagnosticsConfigPanel title="Cluster" rows={[
          ["Enabled", diagnostics.cluster.enabled ? "enabled" : "disabled"],
          ["Registry", diagnostics.cluster.registry_type || "--"],
          ["Registry TTL", diagnostics.cluster.registry_ttl || "--"],
          ["Peer Auth", diagnostics.cluster.peer_auth_mode || "--"],
        ]} />
      </section>
    </div>
  );
}

function DiagnosisBundlePanel({
  clientID,
  deviceID,
  messageLimit,
  onClientIDChange,
  onDeviceIDChange,
  onDownload,
  onMessageLimitChange,
  onProbeTimeoutChange,
  onSessionLimitChange,
  probeTimeout,
  sessionLimit,
  state,
}: {
  clientID: string;
  deviceID: string;
  messageLimit: number;
  onClientIDChange: (clientID: string) => void;
  onDeviceIDChange: (deviceID: string) => void;
  onDownload: () => void | Promise<void>;
  onMessageLimitChange: (limit: number) => void;
  onProbeTimeoutChange: (timeout: string) => void;
  onSessionLimitChange: (limit: number) => void;
  probeTimeout: string;
  sessionLimit: number;
  state: RemoteState<AdminDiagnosisBundle>;
}) {
  const running = state.status === "loading";
  const bundle = state.data;
  const sections = Object.entries(bundle?.sections ?? {});
  const failedSections = sections.filter(([, section]) => Boolean(section.error) || !successfulHTTPStatus(section.http_status));
  const requiresClient = deviceID.trim() !== "" && clientID.trim() === "";

  return (
    <section className="grid gap-5 xl:grid-cols-[0.95fr_1.05fr]">
      <article className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
        <div className="flex items-start justify-between gap-3">
          <div>
            <p className="text-sm font-medium text-zinc-500">Diagnosis Bundle</p>
            <h3 className="mt-1 text-2xl font-semibold tracking-tight">Safe JSON attachment</h3>
          </div>
          <FileText size={24} className="text-accent" weight="duotone" />
        </div>

        <form
          className="mt-5 grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            void onDownload();
          }}
        >
          <div className="grid gap-3 md:grid-cols-2">
            <div className="grid gap-2">
              <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="diagnosis-client-id">
                ClientID
              </label>
              <input
                className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 placeholder:text-zinc-400 focus:border-accent"
                id="diagnosis-client-id"
                onChange={(event) => onClientIDChange(event.target.value)}
                placeholder="optional"
                value={clientID}
              />
            </div>
            <div className="grid gap-2">
              <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="diagnosis-device-id">
                DeviceID
              </label>
              <input
                className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 placeholder:text-zinc-400 focus:border-accent"
                id="diagnosis-device-id"
                onChange={(event) => onDeviceIDChange(event.target.value)}
                placeholder="optional"
                value={deviceID}
              />
            </div>
          </div>

          <div className="grid gap-3 md:grid-cols-3">
            <div className="grid gap-2">
              <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="diagnosis-probe-timeout">
                Probe Timeout
              </label>
              <input
                className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 focus:border-accent"
                id="diagnosis-probe-timeout"
                onChange={(event) => onProbeTimeoutChange(event.target.value)}
                placeholder="2s"
                value={probeTimeout}
              />
            </div>
            <div className="grid gap-2">
              <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="diagnosis-message-limit">
                Message Limit
              </label>
              <input
                className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 focus:border-accent"
                id="diagnosis-message-limit"
                max={1000}
                min={1}
                onChange={(event) => onMessageLimitChange(clampMessageLimit(event.target.value))}
                type="number"
                value={messageLimit}
              />
            </div>
            <div className="grid gap-2">
              <label className="text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="diagnosis-session-limit">
                Session Limit
              </label>
              <input
                className="min-w-0 rounded-lg border border-line bg-white px-3 py-2 font-mono text-sm outline-none transition duration-300 focus:border-accent"
                id="diagnosis-session-limit"
                max={1000}
                min={1}
                onChange={(event) => onSessionLimitChange(clampSessionLimit(event.target.value))}
                type="number"
                value={sessionLimit}
              />
            </div>
          </div>

          <button
            className="inline-flex items-center justify-center gap-2 rounded-lg bg-zinc-950 px-4 py-2 text-sm font-medium text-white transition duration-300 hover:bg-zinc-800 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-50"
            disabled={running || requiresClient}
            type="submit"
          >
            <DownloadSimple size={16} weight="bold" />
            {running ? "Collecting..." : "Download Bundle"}
          </button>
          <p className="text-xs leading-relaxed text-zinc-500">
            Includes overview, diagnostics, dependency checks, routes, failed messages, and optional client route sections.
          </p>
        </form>

        {requiresClient && (
          <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
            ClientID is required when DeviceID is set.
          </div>
        )}

        {state.status === "error" && (
          <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
            <p className="font-semibold">Bundle collection failed</p>
            <p className="mt-1 break-words font-mono text-xs">{state.error}</p>
          </div>
        )}
      </article>

      <article className="rounded-lg border border-line bg-zinc-950 p-5 text-white shadow-diffusion">
        <p className="text-sm text-zinc-400">Last Bundle</p>
        <h3 className="mt-1 text-2xl font-semibold tracking-tight">{bundle?.collection_status ?? "not collected"}</h3>
        <div className="mt-6 grid gap-3 border-t border-white/10 pt-4">
          <DarkLineItem label="Generated" value={formatOptionalDate(bundle?.generated_at)} />
          <DarkLineItem label="Target" value={bundle?.target_url || "--"} />
          <DarkLineItem label="Sections" value={sections.length.toLocaleString()} />
          <DarkLineItem label="Failed" value={failedSections.length.toLocaleString()} />
        </div>
        {sections.length > 0 && (
          <div className="mt-5 grid gap-2">
            {sections.map(([name, section]) => (
              <div className="flex min-w-0 items-center justify-between gap-3 rounded-lg border border-white/10 bg-white/10 px-3 py-2" key={name}>
                <span className="min-w-0 truncate font-mono text-xs text-zinc-200">{name}</span>
                <span className="shrink-0 font-mono text-xs text-zinc-400">{section.error ? "error" : section.http_status ?? "--"}</span>
              </div>
            ))}
          </div>
        )}
      </article>
    </section>
  );
}

function MonitoringContextPanel({
  className = "",
  description,
  metrics,
  monitoring,
  title,
}: {
  className?: string;
  description: string;
  metrics: MetricContext[];
  monitoring?: AdminMonitoringLinks;
  title: string;
}) {
  const prometheusURL = monitoring?.prometheus_url?.trim() ?? "";
  const grafanaURL = monitoring?.grafana_url?.trim() ?? "";
  const dashboardURL = monitoring?.dashboard_url?.trim() ?? "";
  const configuredLinks = [
    { label: "Prometheus", value: prometheusURL },
    { label: "Grafana", value: grafanaURL },
    { label: "Dashboard", value: dashboardURL },
  ].filter((item) => item.value !== "");

  return (
    <section className={["rounded-lg border border-line bg-white p-5 shadow-diffusion", className].join(" ")}>
      <div className="grid gap-5 xl:grid-cols-[0.72fr_1.28fr]">
        <div className="min-w-0">
          <p className="text-sm font-medium text-zinc-500">{title}</p>
          <h3 className="mt-1 text-2xl font-semibold tracking-tight">Metrics and runbook links</h3>
          <p className="mt-3 max-w-[64ch] text-sm leading-relaxed text-zinc-500">{description}</p>

          <div className="mt-5 grid gap-2">
            {configuredLinks.length === 0 ? (
              <div className="rounded-lg border border-dashed border-line bg-zinc-50 px-4 py-4 text-sm text-zinc-500">
                Monitoring links are not configured for this gateway.
              </div>
            ) : (
              configuredLinks.map((link) => (
                <a
                  className="group inline-flex min-w-0 items-center justify-between gap-3 rounded-lg border border-line bg-zinc-50 px-4 py-3 text-sm font-medium text-ink transition duration-300 hover:border-zinc-300 hover:bg-white active:translate-y-px"
                  href={link.value}
                  key={link.label}
                  rel="noreferrer"
                  target="_blank"
                >
                  <span className="min-w-0 truncate">{link.label}</span>
                  <ArrowSquareOut size={16} className="shrink-0 text-zinc-400 transition duration-300 group-hover:text-accent" weight="bold" />
                </a>
              ))
            )}
          </div>
        </div>

        <div className="grid min-w-0 gap-3">
          {metrics.map((metric, index) => (
            <MetricContextCard
              key={`${metric.metric}-${index}`}
              metric={metric}
              prometheusURL={prometheusURL}
              styleDelay={index * 45}
            />
          ))}
        </div>
      </div>
    </section>
  );
}

function MetricContextCard({
  metric,
  prometheusURL,
  styleDelay,
}: {
  metric: MetricContext;
  prometheusURL: string;
  styleDelay: number;
}) {
  const queryURL = prometheusQueryURL(prometheusURL, metric.promql);

  return (
    <article
      className="animate-rise rounded-lg border border-line bg-zinc-50 px-4 py-3"
      style={{ animationDelay: `${styleDelay}ms` }}
    >
      <div className="grid gap-3 lg:grid-cols-[0.52fr_1.48fr]">
        <div className="min-w-0">
          <p className="text-sm font-semibold tracking-tight text-ink">{metric.label}</p>
          <p className="mt-1 break-words font-mono text-xs text-zinc-500">{metric.metric}</p>
        </div>
        <div className="min-w-0">
          <div className="rounded-lg border border-line bg-white px-3 py-2">
            <p className="break-words font-mono text-xs leading-relaxed text-zinc-700">{metric.promql}</p>
          </div>
          <div className="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <p className="min-w-0 text-xs leading-relaxed text-zinc-500">{metric.hint}</p>
            {queryURL && (
              <a
                className="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg border border-line bg-white px-3 py-2 text-xs font-medium text-ink transition duration-300 hover:border-zinc-300 hover:bg-zinc-100 active:translate-y-px"
                href={queryURL}
                rel="noreferrer"
                target="_blank"
              >
                Open Query
                <ArrowSquareOut size={13} weight="bold" />
              </a>
            )}
          </div>
        </div>
      </div>
    </article>
  );
}

function RouteCard({ route, index }: { route: AdminRoute; index: number }) {
  const detailRows = routeDetails(route);

  return (
    <article
      className="animate-rise overflow-hidden rounded-lg border border-line bg-white shadow-diffusion"
      style={{ animationDelay: `${index * 45}ms` }}
    >
      <div className="grid gap-4 p-4 lg:grid-cols-[0.7fr_1.3fr] lg:p-5">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-md bg-zinc-950 px-2.5 py-1 font-mono text-xs font-medium text-white">
              {route.target_type.toUpperCase()}
            </span>
            <span className="rounded-md border border-line bg-zinc-50 px-2.5 py-1 font-mono text-xs text-zinc-600">
              {routeRangeLabel(route)}
            </span>
          </div>
          <h3 className="mt-4 truncate text-xl font-semibold tracking-tight">{route.name}</h3>
          <p className="mt-2 text-sm text-zinc-500">
            max in-flight: <span className="font-mono text-ink">{route.max_in_flight?.toLocaleString() ?? "--"}</span>
          </p>
        </div>

        <div className="grid min-w-0 gap-3 md:grid-cols-2">
          {detailRows.map((item) => (
            <div className="min-w-0 rounded-lg border border-line bg-zinc-50 px-3 py-2" key={item.label}>
              <p className="text-xs uppercase tracking-[0.14em] text-zinc-500">{item.label}</p>
              <p className="mt-1 break-words font-mono text-sm text-ink">{item.value}</p>
            </div>
          ))}
        </div>
      </div>
    </article>
  );
}

function MetricRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-3 rounded-lg border border-line bg-zinc-50 px-4 py-3">
      <span className="text-sm text-zinc-500">{label}</span>
      <span className="truncate text-right font-mono text-sm font-medium text-ink">{value}</span>
    </div>
  );
}

function DarkLineItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-3 border-b border-white/10 pb-2 last:border-0 last:pb-0">
      <span className="text-sm text-zinc-400">{label}</span>
      <span className="truncate text-right font-mono text-sm text-white">{value}</span>
    </div>
  );
}

function StatusBadge({ label, tone }: { label: string; tone: "ok" | "warn" }) {
  return (
    <span
      className={[
        "inline-flex items-center gap-2 rounded-md px-2.5 py-1 text-xs font-medium",
        tone === "ok" ? "bg-emerald-50 text-emerald-800" : "bg-amber-50 text-amber-800",
      ].join(" ")}
    >
      <span className={["size-1.5 rounded-full", tone === "ok" ? "bg-accent" : "bg-amber-500"].join(" ")} />
      {label}
    </span>
  );
}

function DiagnosticsConfigPanel({ title, rows }: { title: string; rows: Array<[string, string]> }) {
  return (
    <article className="rounded-lg border border-line bg-white p-5 shadow-diffusion">
      <p className="text-sm font-medium text-zinc-500">{title}</p>
      <div className="mt-5 grid gap-3">
        {rows.map(([label, value]) => (
          <LineItem key={label} label={label} value={value} />
        ))}
      </div>
    </article>
  );
}

function DependencyList({ dependencies }: { dependencies: Dependency[] }) {
  if (dependencies.length === 0) {
    return (
      <div className="mt-4 rounded-lg border border-dashed border-line bg-white px-4 py-6 text-sm text-zinc-500">
        No dependency probes are registered.
      </div>
    );
  }

  return (
    <div className="mt-4 divide-y divide-line rounded-lg border border-line bg-white">
      {dependencies.map((dependency, index) => (
        <div
          className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 px-4 py-3 animate-rise"
          key={`${dependency.name}-${index}`}
          style={{ animationDelay: `${index * 55}ms` }}
        >
          {dependency.status === "ok" ? (
            <CheckCircle size={18} className="text-accent" weight="duotone" />
          ) : (
            <XCircle size={18} className="text-amber-600" weight="duotone" />
          )}
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{dependency.name}</p>
            {dependency.reason && <p className="truncate text-xs text-zinc-500">{dependency.reason}</p>}
          </div>
          <span className="rounded-md bg-zinc-100 px-2 py-1 font-mono text-xs text-zinc-600">{dependency.status}</span>
        </div>
      ))}
    </div>
  );
}

function MetricBlock({
  icon,
  label,
  value,
  detail,
}: {
  icon: ReactNode;
  label: string;
  value: number;
  detail: string;
}) {
  return (
    <div className="rounded-lg border border-line bg-zinc-50 p-4">
      <div className="flex items-center gap-2 text-sm font-medium text-zinc-500">
        <span className="text-accent">{icon}</span>
        {label}
      </div>
      <p className="mt-4 font-mono text-5xl tracking-tight text-ink">{value.toLocaleString()}</p>
      <p className="mt-2 text-sm text-zinc-500">{detail}</p>
    </div>
  );
}

function StatusPill({ ready, status }: { ready: boolean; status: string }) {
  return (
    <div
      className={[
        "inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-sm font-medium",
        ready ? "border-emerald-200 bg-emerald-50 text-emerald-800" : "border-amber-200 bg-amber-50 text-amber-800",
      ].join(" ")}
    >
      <span className={["size-2 rounded-full", ready ? "bg-accent animate-soft-pulse" : "bg-amber-500"].join(" ")} />
      {status}
    </div>
  );
}

function LineItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-3 border-b border-line/70 pb-2 last:border-0 last:pb-0">
      <span className="text-sm text-zinc-500">{label}</span>
      <span className="truncate text-right font-mono text-sm text-ink">{value}</span>
    </div>
  );
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
      <div className="flex items-start gap-3">
        <Warning size={18} className="mt-0.5 shrink-0" weight="duotone" />
        <div>
          <p className="font-semibold">Request failed</p>
          <p className="mt-1 font-mono text-xs">{message}</p>
        </div>
      </div>
    </div>
  );
}

function AuthEmptyState({ checking }: { checking: boolean }) {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <LockKey size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">{checking ? "Checking Admin Session" : "Sign In Required"}</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          {checking ? "Restoring the current browser session." : "Use the internal token in the left panel to create a short-lived session."}
        </p>
      </div>
    </div>
  );
}

function RoutesEmptyState() {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <GitBranch size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">No Routes</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          No upstream route definitions were returned by the gateway.
        </p>
      </div>
    </div>
  );
}

function SessionsEmptyState({ filtered }: { filtered: boolean }) {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <RadioButton size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">No Sessions</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          {filtered ? "No local session matched this client filter." : "This gateway did not return local online sessions."}
        </p>
      </div>
    </div>
  );
}

function MessagesEmptyState({ status }: { status: MessageStatus }) {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <Database size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">No {status} Messages</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          The gateway returned an empty message list for this status.
        </p>
      </div>
    </div>
  );
}

function ChecksEmptyState({ onRun, running }: { onRun: () => void | Promise<void>; running: boolean }) {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <ShieldCheck size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">No Check Result</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          Run a dependency check to inspect auth, storage, cluster registry, and upstream targets.
        </p>
        <button
          className="mt-5 inline-flex items-center justify-center gap-2 rounded-lg bg-zinc-950 px-4 py-2 text-sm font-medium text-white transition duration-300 hover:bg-zinc-800 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-50"
          disabled={running}
          onClick={() => void onRun()}
          type="button"
        >
          <ShieldCheck size={16} weight="bold" />
          {running ? "Checking..." : "Run Check"}
        </button>
      </div>
    </div>
  );
}

function DiagnosticsEmptyState() {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <Gauge size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">No Diagnostics</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          The gateway did not return diagnostic data.
        </p>
      </div>
    </div>
  );
}

function ChecksSkeleton() {
  return (
    <div className="grid gap-5">
      <SkeletonPanel className="h-40" />
      <SkeletonPanel className="h-40" />
      <SkeletonPanel className="h-40" />
    </div>
  );
}

function MessagesSkeleton() {
  return (
    <div className="grid gap-5">
      <SkeletonPanel className="h-52" />
      <div className="grid gap-5 xl:grid-cols-[0.8fr_1.2fr]">
        <SkeletonPanel className="h-48" />
        <SkeletonPanel className="h-48" />
      </div>
      <SkeletonPanel className="h-40" />
      <SkeletonPanel className="h-40" />
    </div>
  );
}

function SessionsSkeleton() {
  return (
    <div className="grid gap-5">
      <SkeletonPanel className="h-52" />
      <div className="grid gap-5 xl:grid-cols-[0.82fr_1.18fr]">
        <SkeletonPanel className="h-64" />
        <SkeletonPanel className="h-64" />
      </div>
      <SkeletonPanel className="h-40" />
      <SkeletonPanel className="h-40" />
    </div>
  );
}

function RoutesSkeleton() {
  return (
    <div className="grid gap-5">
      <SkeletonPanel className="h-52" />
      <SkeletonPanel className="h-36" />
      <SkeletonPanel className="h-36" />
    </div>
  );
}

function DiagnosticsSkeleton() {
  return (
    <div className="grid gap-5">
      <SkeletonPanel className="h-56" />
      <div className="grid gap-5 xl:grid-cols-2">
        <SkeletonPanel className="h-72" />
        <SkeletonPanel className="h-72" />
      </div>
      <SkeletonPanel className="h-52" />
    </div>
  );
}

function OverviewSkeleton() {
  return (
    <div className="grid gap-5 xl:grid-cols-[1.35fr_0.85fr]">
      <SkeletonPanel className="h-72" />
      <SkeletonPanel className="h-72" />
      <SkeletonPanel className="h-80 xl:col-span-2" />
    </div>
  );
}

function routeRangeLabel(route: AdminRoute): string {
  if (!route.msg_id_max || route.msg_id_max === route.msg_id_min) {
    return `MsgID ${route.msg_id_min}`;
  }
  return `MsgID ${route.msg_id_min}-${route.msg_id_max}`;
}

function routeDetails(route: AdminRoute): Array<{ label: string; value: string }> {
  if (route.http) {
    return [
      { label: "URL", value: route.http.url || "--" },
      { label: "Timeout", value: route.http.timeout || "--" },
      { label: "Target", value: "HTTP" },
      { label: "Range", value: routeRangeLabel(route) },
    ];
  }

  if (route.nsq) {
    return [
      { label: "Topic", value: route.nsq.topic || "--" },
      { label: "Addresses", value: route.nsq.addresses?.join(", ") || "--" },
      { label: "Publish Mode", value: route.nsq.publish_mode || "--" },
      { label: "Retry Attempts", value: route.nsq.retry_attempts?.toLocaleString() ?? "--" },
    ];
  }

  return [
    { label: "Target", value: route.target_type || "--" },
    { label: "Range", value: routeRangeLabel(route) },
  ];
}

function healthyDependencyStatus(status: string): boolean {
  return status === "configured" || status === "ok" || status === "healthy" || status === "disabled";
}

function clientRouteStatus(lookup?: AdminClientRouteLookup): ClientRouteStatus {
  if (!lookup) {
    return "idle";
  }
  if (lookup.local_session_found) {
    return "local";
  }
  if (lookup.cluster_route_found) {
    if ((lookup.cluster_route?.expires_in_ms ?? 1) <= 0) {
      return "stale";
    }
    return "remote";
  }
  return "offline";
}

function successfulHTTPStatus(status?: number): boolean {
  return status != null && status >= 200 && status < 300;
}

function checkCounts(checks: AdminCheckResult[]): Record<"ok" | "degraded" | "failed" | "skipped", number> {
  return checks.reduce(
    (counts, check) => {
      if (check.status === "ok" || check.status === "degraded" || check.status === "failed" || check.status === "skipped") {
        counts[check.status] += 1;
      }
      return counts;
    },
    { ok: 0, degraded: 0, failed: 0, skipped: 0 },
  );
}

function canRequeue(status?: MessageStatus | string): boolean {
  return Boolean(status) && status !== "delivered" && status !== "discarded";
}

function canDiscard(status?: MessageStatus | string): boolean {
  return Boolean(status) && status !== "delivered" && status !== "discarded";
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof APIError && error.status === 401;
}

function requestErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "unknown_error";
}

function adminSessionAllows(session: AdminConsoleSession, permission: string): boolean {
  if (session.permissions?.includes(permission)) {
    return true;
  }
  if (session.role === "admin") {
    return true;
  }
  return session.role === "operator" && permission === messageRepairPermission;
}

function shortID(value: string): string {
  if (value.length <= 12) {
    return value;
  }
  return `${value.slice(0, 8)}...${value.slice(-4)}`;
}

function formatOptionalDate(value?: string | null): string {
  if (!value || value.startsWith("0001-01-01")) {
    return "--";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "--";
  }
  return date.toLocaleString();
}

function formatMillis(value?: number): string {
  if (value == null) {
    return "--";
  }
  if (value <= 0) {
    return "expired";
  }
  if (value < 1000) {
    return `${value}ms`;
  }
  const seconds = value / 1000;
  if (seconds < 60) {
    return `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds).toLocaleString()}s`;
  }
  const minutes = seconds / 60;
  if (minutes < 60) {
    return `${minutes < 10 ? minutes.toFixed(1) : Math.round(minutes).toLocaleString()}m`;
  }
  const hours = minutes / 60;
  if (hours < 48) {
    return `${hours < 10 ? hours.toFixed(1) : Math.round(hours).toLocaleString()}h`;
  }
  const days = hours / 24;
  return `${days < 10 ? days.toFixed(1) : Math.round(days).toLocaleString()}d`;
}

function clampMessageLimit(value: string): number {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed)) {
    return 1;
  }
  return Math.min(1000, Math.max(1, parsed));
}

function clampSessionLimit(value: string): number {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed)) {
    return 1;
  }
  return Math.min(1000, Math.max(1, parsed));
}

function diagnosisBundleFilename(bundle: AdminDiagnosisBundle): string {
  const timestamp = bundle.generated_at ? new Date(bundle.generated_at) : new Date();
  const value = Number.isNaN(timestamp.getTime()) ? new Date() : timestamp;
  return `z-courier-diagnose-${value.toISOString().replace(/[:.]/g, "-")}.json`;
}

function prometheusQueryURL(baseURL: string, expression: string): string {
  const base = baseURL.trim().replace(/\/+$/, "");
  if (base === "") {
    return "";
  }
  const query = new URLSearchParams({
    "g0.expr": expression,
    "g0.range_input": "1h",
    "g0.tab": "0",
  });
  return `${base}/graph?${query.toString()}`;
}

function downloadJSON(value: unknown, filename: string) {
  const blob = new Blob([`${JSON.stringify(value, null, 2)}\n`], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

function SkeletonPanel({ className }: { className: string }) {
  return (
    <div className={`relative overflow-hidden rounded-lg border border-line bg-white shadow-diffusion ${className}`}>
      <div className="absolute inset-0 -translate-x-full bg-gradient-to-r from-transparent via-zinc-100 to-transparent animate-shimmer" />
      <div className="p-5">
        <div className="h-4 w-28 rounded-md bg-zinc-100" />
        <div className="mt-4 h-8 w-48 rounded-md bg-zinc-100" />
        <div className="mt-8 grid gap-3">
          <div className="h-12 rounded-md bg-zinc-100" />
          <div className="h-12 rounded-md bg-zinc-100" />
        </div>
      </div>
    </div>
  );
}
