import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  ArrowClockwise,
  ChartLineUp,
  CheckCircle,
  Circuitry,
  Database,
  GitBranch,
  PlugsConnected,
  LockKey,
  Pulse,
  RadioButton,
  ShieldCheck,
  Warning,
  XCircle,
} from "@phosphor-icons/react";
import { fetchOverview, fetchRoutes } from "./api";
import type { AdminOverview, AdminRoute, AdminRoutes, Dependency } from "./types";

const tokenStorageKey = "z-courier-console-token";

type RemoteState<T> =
  | { status: "idle"; data?: undefined; error?: undefined }
  | { status: "loading"; data?: T; error?: undefined }
  | { status: "ready"; data: T; error?: undefined }
  | { status: "error"; data?: T; error: string };

type PageID = "overview" | "routes" | "messages" | "diagnostics";

const navItems = [
  { id: "overview" as const, label: "Overview", icon: Pulse, disabled: false },
  { id: "routes" as const, label: "Routes", icon: GitBranch, disabled: false },
  { id: "messages" as const, label: "Messages", icon: Database, disabled: true },
  { id: "diagnostics" as const, label: "Diagnostics", icon: Pulse, disabled: true },
];

export default function App() {
  const [draftToken, setDraftToken] = useState(() => window.sessionStorage.getItem(tokenStorageKey) ?? "");
  const [activeToken, setActiveToken] = useState(() => window.sessionStorage.getItem(tokenStorageKey) ?? "");
  const [state, setState] = useState<RemoteState<AdminOverview>>(() =>
    window.sessionStorage.getItem(tokenStorageKey) ? { status: "loading" } : { status: "idle" },
  );
  const [routeState, setRouteState] = useState<RemoteState<AdminRoutes>>({ status: "idle" });
  const [activePage, setActivePage] = useState<PageID>("overview");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

  useEffect(() => {
    if (activeToken.trim() === "") {
      window.sessionStorage.removeItem(tokenStorageKey);
    } else {
      window.sessionStorage.setItem(tokenStorageKey, activeToken.trim());
    }
  }, [activeToken]);

  const refresh = useCallback(
    async (signal?: AbortSignal) => {
      if (activeToken.trim() === "") {
        setState({ status: "idle" });
        return;
      }
      setState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchOverview(activeToken, signal);
        setState({ status: "ready", data });
        setUpdatedAt(new Date());
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        const message = error instanceof Error ? error.message : "unknown_error";
        setState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [activeToken],
  );

  const refreshRoutes = useCallback(
    async (signal?: AbortSignal) => {
      if (activeToken.trim() === "") {
        setRouteState({ status: "idle" });
        return;
      }
      setRouteState((current) => ({ status: "loading", data: current.data }));
      try {
        const data = await fetchRoutes(activeToken, signal);
        setRouteState({ status: "ready", data });
      } catch (error) {
        if (signal?.aborted) {
          return;
        }
        const message = error instanceof Error ? error.message : "unknown_error";
        setRouteState((current) => ({ status: "error", data: current.data, error: message }));
      }
    },
    [activeToken],
  );

  useEffect(() => {
    if (activeToken.trim() === "") {
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
  }, [activeToken, refresh]);

  useEffect(() => {
    if (activePage !== "routes") {
      return;
    }
    if (activeToken.trim() === "") {
      setRouteState({ status: "idle" });
      return;
    }

    const controller = new AbortController();
    void refreshRoutes(controller.signal);
    return () => {
      controller.abort();
    };
  }, [activePage, activeToken, refreshRoutes]);

  const connect = useCallback(() => {
    const nextToken = draftToken.trim();
    setActiveToken(nextToken);
    if (nextToken === "") {
      setState({ status: "idle" });
      setRouteState({ status: "idle" });
      setUpdatedAt(null);
    }
  }, [draftToken]);

  const refreshCurrentPage = useCallback(() => {
    if (activePage === "routes") {
      void refreshRoutes();
      return;
    }
    void refresh();
  }, [activePage, refresh, refreshRoutes]);

  const overview = state.data;
  const ready = overview?.readiness.ready ?? false;
  const statusText = overview?.readiness.status ?? (state.status === "error" ? "not connected" : activeToken ? "connecting" : "auth required");
  const pageTitle = activePage === "routes" ? "Routes" : "Operations Overview";

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
            <form
              className="grid min-w-0 gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                connect();
              }}
            >
              <label className="block text-xs font-semibold uppercase tracking-[0.14em] text-zinc-500" htmlFor="internal-token">
                Internal Token
              </label>
              <div className="flex min-w-0 items-center gap-2 overflow-hidden rounded-lg border border-line bg-white px-2.5 py-2 focus-within:border-accent">
                <LockKey size={16} className="shrink-0 text-zinc-400" weight="duotone" />
                <input
                  id="internal-token"
                  className="w-full min-w-0 flex-1 truncate bg-transparent text-sm outline-none placeholder:text-zinc-400"
                  onChange={(event) => setDraftToken(event.target.value)}
                  placeholder="dev-internal-token"
                  type="password"
                  value={draftToken}
                />
              </div>
              <button
                className="mt-1 inline-flex w-full min-w-0 items-center justify-center gap-2 rounded-lg bg-zinc-950 px-3 py-2 text-sm font-medium text-white transition duration-300 hover:bg-zinc-800 active:translate-y-px"
                type="submit"
              >
                <PlugsConnected size={16} className="shrink-0" weight="bold" />
                <span className="min-w-0 truncate">Connect</span>
              </button>
              <p className="min-w-0 break-words text-xs leading-relaxed text-zinc-500">Stored in this browser session after connect.</p>
            </form>
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
                disabled={activeToken.trim() === ""}
                onClick={refreshCurrentPage}
                type="button"
              >
                <ArrowClockwise size={16} weight="bold" />
                Refresh
              </button>
            </div>
          </header>

          {state.status === "idle" && <AuthEmptyState />}

          {activePage === "overview" && state.status === "error" && <ErrorBanner message={state.error} />}
          {activePage === "routes" && routeState.status === "error" && <ErrorBanner message={routeState.error} />}

          {activePage === "overview" && overview ? (
            <Dashboard overview={overview} updatedAt={updatedAt} />
          ) : activePage === "overview" && state.status === "loading" ? (
            <OverviewSkeleton />
          ) : activePage === "routes" && activeToken.trim() !== "" && (routeState.status !== "error" || routeState.data) ? (
            <RoutesPage state={routeState} />
          ) : null}
        </section>
      </div>
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

function AuthEmptyState() {
  return (
    <div className="rounded-lg border border-line bg-white px-5 py-8 shadow-diffusion">
      <div className="max-w-xl">
        <div className="grid size-12 place-items-center rounded-lg border border-line bg-zinc-50 text-accent">
          <LockKey size={22} weight="duotone" />
        </div>
        <h2 className="mt-5 text-2xl font-semibold tracking-tight">Connect to Internal HTTP</h2>
        <p className="mt-2 text-sm leading-relaxed text-zinc-500">
          Enter the internal token in the left panel to load gateway overview data.
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

function RoutesSkeleton() {
  return (
    <div className="grid gap-5">
      <SkeletonPanel className="h-52" />
      <SkeletonPanel className="h-36" />
      <SkeletonPanel className="h-36" />
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
