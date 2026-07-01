import type { AdminOverview, AdminRoutes } from "./types";

const internalTokenHeader = "X-ZCourier-Internal-Token";

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

export async function fetchOverview(token: string, signal?: AbortSignal): Promise<AdminOverview> {
  return fetchAdminJSON<AdminOverview>("/internal/admin/overview", token, signal);
}

export async function fetchRoutes(token: string, signal?: AbortSignal): Promise<AdminRoutes> {
  return fetchAdminJSON<AdminRoutes>("/internal/admin/routes", token, signal);
}
