/**
 * Authentication module.
 * Supports API key and OAuth2 methods matching the REST API auth methods.
 * Session idle timeout default: 30 minutes.
 */

const TOKEN_KEY = 'agentplane_token';
const TOKEN_EXPIRY_KEY = 'agentplane_token_expiry';
const SESSION_TIMEOUT_MS = 30 * 60 * 1000; // 30 minutes

export interface AuthState {
  token: string;
  method: 'api_key' | 'oauth2';
  expiresAt: number; // unix timestamp ms
}

/** Store token after login. */
export function setAuth(token: string, method: 'api_key' | 'oauth2'): void {
  const expiresAt = Date.now() + SESSION_TIMEOUT_MS;
  const state: AuthState = { token, method, expiresAt };
  sessionStorage.setItem(TOKEN_KEY, JSON.stringify(state));
  sessionStorage.setItem(TOKEN_EXPIRY_KEY, String(expiresAt));
}

/** Refresh session expiry on activity. */
export function touchSession(): void {
  const raw = sessionStorage.getItem(TOKEN_KEY);
  if (!raw) return;
  const state: AuthState = JSON.parse(raw);
  state.expiresAt = Date.now() + SESSION_TIMEOUT_MS;
  sessionStorage.setItem(TOKEN_KEY, JSON.stringify(state));
  sessionStorage.setItem(TOKEN_EXPIRY_KEY, String(state.expiresAt));
}

/** Get current auth state or null if not authenticated / expired. */
export function getAuth(): AuthState | null {
  const raw = sessionStorage.getItem(TOKEN_KEY);
  if (!raw) return null;
  const state: AuthState = JSON.parse(raw);
  if (Date.now() > state.expiresAt) {
    clearAuth();
    return null;
  }
  return state;
}

/** Get token string for Authorization header. */
export function getToken(): string | null {
  const auth = getAuth();
  return auth?.token ?? null;
}

/** Check if user is authenticated. */
export function isAuthenticated(): boolean {
  return getAuth() !== null;
}

/** Clear auth state (logout). */
export function clearAuth(): void {
  sessionStorage.removeItem(TOKEN_KEY);
  sessionStorage.removeItem(TOKEN_EXPIRY_KEY);
}
