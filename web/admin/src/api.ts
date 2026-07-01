import type { AdminOverview } from "./types";

const internalTokenHeader = "X-ZCourier-Internal-Token";

export class APIError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

export async function fetchOverview(token: string, signal?: AbortSignal): Promise<AdminOverview> {
  const headers = new Headers();
  if (token.trim() !== "") {
    headers.set(internalTokenHeader, token.trim());
  }

  const response = await fetch("/internal/admin/overview", {
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

  return (await response.json()) as AdminOverview;
}
