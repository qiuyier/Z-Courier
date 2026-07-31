import type {
  AdminAudit,
  AdminCheck,
  AdminClientRouteLookup,
  AdminClusterRoutes,
  AdminDiagnosisBundle,
  AdminDiagnostics,
  AdminMessages,
  AdminOverview,
  AdminRoutes,
  AdminRouteReload,
  AdminSessionResponse,
  AdminSessionDisconnectRequest,
  AdminSessionDisconnectResponse,
  AdminSessions,
  BulkRequeueResponse,
  DownlinkTestPushResponse,
  MessageStatus,
  MessageStatusResponse,
  RetryScanResponse,
} from "./types";

export type DiagnosisBundleParams = {
  probeTimeout: string;
  messageLimit: number;
  sessionLimit: number;
  clientID: string;
  deviceID: string;
};

export type SessionListParams = {
  clientID: string;
  deviceID: string;
  sessionID: string;
  limit: number;
};

export type AuditListParams = {
  action: string;
  result: string;
  principal: string;
  clientID: string;
  sessionID: string;
  messageID: string;
  cursor?: string;
  limit: number;
};

export type MessageListParams = {
  status: MessageStatus;
  limit: number;
  cursor?: string;
};

export type DownlinkTestPushParams = {
  clientID: string;
  deviceID: string;
  msgID: number;
  messageID: string;
  traceID: string;
  ackRequired: boolean;
  body: string;
};

export class APIError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

let adminCSRFToken = "";

export function setAdminCSRFToken(token?: string) {
  adminCSRFToken = token?.trim() ?? "";
}

async function fetchAdminJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    method: "GET",
    signal,
  });

  if (!response.ok) {
    let message = `Request failed with ${response.status}`;
    try {
      const body = (await response.json()) as { code?: string };
      if (body.code) {
        message = body.code;
      }
    } catch {
      // Keep the HTTP status message when the response is not JSON.
    }
    throw new APIError(message, response.status);
  }

  return (await response.json()) as T;
}

async function postAdminJSON<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const headers = new Headers();
  headers.set("Content-Type", "application/json");
  if (adminCSRFToken !== "") {
    headers.set("X-ZCourier-CSRF-Token", adminCSRFToken);
  }

  const response = await fetch(path, {
    credentials: "same-origin",
    method: "POST",
    headers,
    body: JSON.stringify(body),
    signal,
  });

  if (!response.ok) {
    let message = `Request failed with ${response.status}`;
    try {
      const responseBody = (await response.json()) as { code?: string; reason?: string };
      if (responseBody.code && responseBody.reason) {
        message = `${responseBody.code}: ${responseBody.reason}`;
      } else if (responseBody.code) {
        message = responseBody.code;
      }
    } catch {
      // Keep the HTTP status message when the response is not JSON.
    }
    throw new APIError(message, response.status);
  }

  return (await response.json()) as T;
}

export async function loginAdminSession(token: string, signal?: AbortSignal): Promise<AdminSessionResponse> {
  return postAdminJSON<AdminSessionResponse>("/internal/admin/session/login", { token }, signal);
}

export async function fetchAdminSession(signal?: AbortSignal): Promise<AdminSessionResponse> {
  return fetchAdminJSON<AdminSessionResponse>("/internal/admin/session/me", signal);
}

export async function logoutAdminSession(signal?: AbortSignal): Promise<AdminSessionResponse> {
  return postAdminJSON<AdminSessionResponse>("/internal/admin/session/logout", {}, signal);
}

export async function fetchOverview(signal?: AbortSignal): Promise<AdminOverview> {
  return fetchAdminJSON<AdminOverview>("/internal/admin/overview", signal);
}

export async function fetchRoutes(signal?: AbortSignal): Promise<AdminRoutes> {
  return fetchAdminJSON<AdminRoutes>("/internal/admin/routes", signal);
}

export async function fetchRouteReloadStatus(signal?: AbortSignal): Promise<AdminRouteReload> {
  return fetchAdminJSON<AdminRouteReload>("/internal/admin/routes/status", signal);
}

export async function validateRouteReload(expectedGeneration: number, signal?: AbortSignal): Promise<AdminRouteReload> {
  return postAdminJSON<AdminRouteReload>(
    "/internal/admin/routes/reload",
    { dry_run: true, expected_generation: expectedGeneration },
    signal,
  );
}

export async function activateRouteReload(expectedGeneration: number, signal?: AbortSignal): Promise<AdminRouteReload> {
  return postAdminJSON<AdminRouteReload>(
    "/internal/admin/routes/reload",
    { dry_run: false, expected_generation: expectedGeneration },
    signal,
  );
}

export async function fetchAudit(params: AuditListParams, signal?: AbortSignal): Promise<AdminAudit> {
  const query = new URLSearchParams({ limit: String(params.limit) });
  if (params.action.trim() !== "") {
    query.set("action", params.action.trim());
  }
  if (params.result.trim() !== "") {
    query.set("result", params.result.trim());
  }
  if (params.principal.trim() !== "") {
    query.set("principal", params.principal.trim());
  }
  if (params.clientID.trim() !== "") {
    query.set("client_id", params.clientID.trim());
  }
  if (params.sessionID.trim() !== "") {
    query.set("session_id", params.sessionID.trim());
  }
  if (params.messageID.trim() !== "") {
    query.set("message_id", params.messageID.trim());
  }
  if (params.cursor?.trim()) {
    query.set("cursor", params.cursor.trim());
  }
  return fetchAdminJSON<AdminAudit>(`/internal/admin/audit?${query.toString()}`, signal);
}

export async function fetchSessions(params: SessionListParams, signal?: AbortSignal): Promise<AdminSessions> {
  const query = new URLSearchParams({ limit: String(params.limit) });
  if (params.sessionID.trim() !== "") {
    query.set("session_id", params.sessionID.trim());
  }
  if (params.clientID.trim() !== "") {
    query.set("client_id", params.clientID.trim());
  }
  if (params.deviceID.trim() !== "") {
    query.set("device_id", params.deviceID.trim());
  }
  return fetchAdminJSON<AdminSessions>(`/internal/debug/sessions?${query.toString()}`, signal);
}

export async function fetchClusterRoutes(params: SessionListParams, signal?: AbortSignal): Promise<AdminClusterRoutes> {
  const query = new URLSearchParams({ limit: String(params.limit) });
  if (params.sessionID.trim() !== "") {
    query.set("session_id", params.sessionID.trim());
  }
  if (params.clientID.trim() !== "") {
    query.set("client_id", params.clientID.trim());
  }
  if (params.deviceID.trim() !== "") {
    query.set("device_id", params.deviceID.trim());
  }
  return fetchAdminJSON<AdminClusterRoutes>(`/internal/debug/cluster/routes?${query.toString()}`, signal);
}

export async function fetchClientRoute(
  clientID: string,
  deviceID: string,
  signal?: AbortSignal,
): Promise<AdminClientRouteLookup> {
  const query = new URLSearchParams({
    client_id: clientID.trim(),
    device_id: deviceID.trim(),
  });
  return fetchAdminJSON<AdminClientRouteLookup>(`/internal/debug/route?${query.toString()}`, signal);
}

export async function disconnectSession(
  request: AdminSessionDisconnectRequest,
  signal?: AbortSignal,
): Promise<AdminSessionDisconnectResponse> {
  return postAdminJSON<AdminSessionDisconnectResponse>("/internal/debug/session/disconnect", request, signal);
}

export async function sendDownlinkTestPush(
  params: DownlinkTestPushParams,
  signal?: AbortSignal,
): Promise<DownlinkTestPushResponse> {
  const messageID = params.messageID.trim() || generatedTestPushID();
  const traceID = params.traceID.trim() || messageID;
  return postAdminJSON<DownlinkTestPushResponse>(
    "/internal/debug/push",
    {
      client_id: params.clientID.trim(),
      device_id: params.deviceID.trim(),
      msg_id: params.msgID,
      message_id: messageID,
      trace_id: traceID,
      ack_required: params.ackRequired,
      body: encodeUTF8Base64(params.body),
    },
    signal,
  );
}

export async function fetchDiagnostics(signal?: AbortSignal): Promise<AdminDiagnostics> {
  return fetchAdminJSON<AdminDiagnostics>("/internal/admin/diagnostics", signal);
}

export async function fetchAdminCheck(timeout: string, signal?: AbortSignal): Promise<AdminCheck> {
  const query = new URLSearchParams();
  if (timeout.trim() !== "") {
    query.set("timeout", timeout.trim());
  }
  const suffix = query.toString() ? `?${query.toString()}` : "";
  return fetchAdminJSON<AdminCheck>(`/internal/admin/check${suffix}`, signal);
}

export async function fetchDiagnosisBundle(
  params: DiagnosisBundleParams,
  signal?: AbortSignal,
): Promise<AdminDiagnosisBundle> {
  const query = new URLSearchParams({
    probe_timeout: params.probeTimeout.trim() || "2s",
    message_limit: String(params.messageLimit),
    session_limit: String(params.sessionLimit),
  });
  if (params.clientID.trim() !== "") {
    query.set("client_id", params.clientID.trim());
  }
  if (params.deviceID.trim() !== "") {
    query.set("device_id", params.deviceID.trim());
  }
  return fetchAdminJSON<AdminDiagnosisBundle>(`/internal/admin/diagnose?${query.toString()}`, signal);
}

export async function fetchMessages(
  params: MessageListParams,
  signal?: AbortSignal,
): Promise<AdminMessages> {
  const query = new URLSearchParams({
    status: params.status,
    limit: String(params.limit),
  });
  if (params.cursor?.trim()) {
    query.set("cursor", params.cursor.trim());
  }
  return fetchAdminJSON<AdminMessages>(`/internal/messages?${query.toString()}`, signal);
}

export async function fetchMessage(messageID: string, signal?: AbortSignal): Promise<MessageStatusResponse> {
  const query = new URLSearchParams({ message_id: messageID });
  return fetchAdminJSON<MessageStatusResponse>(`/internal/message/status?${query.toString()}`, signal);
}

export async function requeueMessage(messageID: string, signal?: AbortSignal): Promise<MessageStatusResponse> {
  return postAdminJSON<MessageStatusResponse>("/internal/message/requeue", { message_id: messageID }, signal);
}

export async function requeueMessages(messageIDs: string[], signal?: AbortSignal): Promise<BulkRequeueResponse> {
  return postAdminJSON<BulkRequeueResponse>("/internal/messages/requeue", { message_ids: messageIDs }, signal);
}

export async function discardMessage(
  messageID: string,
  reason: string,
  signal?: AbortSignal,
): Promise<MessageStatusResponse> {
  return postAdminJSON<MessageStatusResponse>("/internal/message/discard", { message_id: messageID, reason }, signal);
}

export async function runRetryScan(signal?: AbortSignal): Promise<RetryScanResponse> {
  return postAdminJSON<RetryScanResponse>("/internal/messages/retry/scan", {}, signal);
}

function generatedTestPushID(): string {
  return `console-push-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function encodeUTF8Base64(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  const chunkSize = 0x8000;
  for (let index = 0; index < bytes.length; index += chunkSize) {
    const chunk = bytes.subarray(index, index + chunkSize);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}
