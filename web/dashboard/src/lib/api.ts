/**
 * API client for AgentPlane REST API.
 * Attaches auth headers and handles 401 redirects.
 */

import { getToken, touchSession, clearAuth } from './auth';

const BASE_URL = '/api/v1';

export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
  ) {
    super(`API error ${status}`);
    this.name = 'ApiError';
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = getToken();
  if (!token) {
    window.location.href = '/login';
    throw new ApiError(401, { error: 'not authenticated' });
  }

  touchSession();

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`,
    ...(init?.headers as Record<string, string>),
  };

  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers,
  });

  if (res.status === 401) {
    clearAuth();
    window.location.href = '/login';
    throw new ApiError(401, { error: 'unauthorized' });
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body);
  }

  return res.json() as Promise<T>;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
};
