import type {
  AdminCheck,
  AdminClientRouteLookup,
  AdminDiagnosisBundle,
  AdminDiagnostics,
  AdminMessages,
  AdminOverview,
  AdminRoutes,
  AdminSessions,
  MessageStatus,
  MessageStatusResponse,
} from "./types";

const internalTokenHeader = "X-ZCourier-Internal-Token";

export type DiagnosisBundleParams = {
  probeTimeout: string;
  messageLimit: number;
  sessionLimit: number;
  clientID: string;
  deviceID: string;
};

export class APIError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

async function fetchAdminJSON<T>(path: string, token: string, signal?: AbortSignal): Promise<T> {
  const headers = new Headers();
  if (token.trim() !== "") {
    headers.set(internalTokenHeader, token.trim());
  }

  const response = await fetch(path, {
    method: "GET",
    headers,
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

async function postAdminJSON<T>(path: string, token: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const headers = new Headers();
  headers.set("Content-Type", "application/json");
  if (token.trim() !== "") {
    headers.set(internalTokenHeader, token.trim());
  }

  const response = await fetch(path, {
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

export async function fetchOverview(token: string, signal?: AbortSignal): Promise<AdminOverview> {
  return fetchAdminJSON<AdminOverview>("/internal/admin/overview", token, signal);
}

export async function fetchRoutes(token: string, signal?: AbortSignal): Promise<AdminRoutes> {
  return fetchAdminJSON<AdminRoutes>("/internal/admin/routes", token, signal);
}

export async function fetchSessions(
  token: string,
  clientID: string,
  limit: number,
  signal?: AbortSignal,
): Promise<AdminSessions> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (clientID.trim() !== "") {
    query.set("client_id", clientID.trim());
  }
  return fetchAdminJSON<AdminSessions>(`/internal/debug/sessions?${query.toString()}`, token, signal);
}

export async function fetchClientRoute(
  token: string,
  clientID: string,
  deviceID: string,
  signal?: AbortSignal,
): Promise<AdminClientRouteLookup> {
  const query = new URLSearchParams({
    client_id: clientID.trim(),
    device_id: deviceID.trim(),
  });
  return fetchAdminJSON<AdminClientRouteLookup>(`/internal/debug/route?${query.toString()}`, token, signal);
}

export async function fetchDiagnostics(token: string, signal?: AbortSignal): Promise<AdminDiagnostics> {
  return fetchAdminJSON<AdminDiagnostics>("/internal/admin/diagnostics", token, signal);
}

export async function fetchAdminCheck(token: string, timeout: string, signal?: AbortSignal): Promise<AdminCheck> {
  const query = new URLSearchParams();
  if (timeout.trim() !== "") {
    query.set("timeout", timeout.trim());
  }
  const suffix = query.toString() ? `?${query.toString()}` : "";
  return fetchAdminJSON<AdminCheck>(`/internal/admin/check${suffix}`, token, signal);
}

export async function fetchDiagnosisBundle(
  token: string,
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
  return fetchAdminJSON<AdminDiagnosisBundle>(`/internal/admin/diagnose?${query.toString()}`, token, signal);
}

export async function fetchMessages(
  token: string,
  status: MessageStatus,
  limit: number,
  signal?: AbortSignal,
): Promise<AdminMessages> {
  const query = new URLSearchParams({
    status,
    limit: String(limit),
  });
  return fetchAdminJSON<AdminMessages>(`/internal/messages?${query.toString()}`, token, signal);
}

export async function fetchMessage(token: string, messageID: string, signal?: AbortSignal): Promise<MessageStatusResponse> {
  const query = new URLSearchParams({ message_id: messageID });
  return fetchAdminJSON<MessageStatusResponse>(`/internal/message/status?${query.toString()}`, token, signal);
}

export async function requeueMessage(token: string, messageID: string, signal?: AbortSignal): Promise<MessageStatusResponse> {
  return postAdminJSON<MessageStatusResponse>("/internal/message/requeue", token, { message_id: messageID }, signal);
}

export async function discardMessage(
  token: string,
  messageID: string,
  reason: string,
  signal?: AbortSignal,
): Promise<MessageStatusResponse> {
  return postAdminJSON<MessageStatusResponse>("/internal/message/discard", token, { message_id: messageID, reason }, signal);
}
