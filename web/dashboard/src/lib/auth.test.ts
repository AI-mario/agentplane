import { describe, it, expect, beforeEach } from 'vitest';
import { setAuth, getAuth, getToken, isAuthenticated, clearAuth, touchSession } from './auth';

describe('auth', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  it('returns null when no auth is set', () => {
    expect(getAuth()).toBeNull();
    expect(getToken()).toBeNull();
    expect(isAuthenticated()).toBe(false);
  });

  it('stores and retrieves auth state', () => {
    setAuth('my-api-key', 'api_key');
    expect(isAuthenticated()).toBe(true);
    expect(getToken()).toBe('my-api-key');
    const state = getAuth();
    expect(state?.method).toBe('api_key');
    expect(state?.expiresAt).toBeGreaterThan(Date.now());
  });

  it('clears auth state on logout', () => {
    setAuth('test-token', 'oauth2');
    expect(isAuthenticated()).toBe(true);
    clearAuth();
    expect(isAuthenticated()).toBe(false);
    expect(getToken()).toBeNull();
  });

  it('expires session after idle timeout', () => {
    setAuth('key', 'api_key');
    // Manually expire
    const raw = sessionStorage.getItem('agentplane_token');
    const state = JSON.parse(raw!);
    state.expiresAt = Date.now() - 1000;
    sessionStorage.setItem('agentplane_token', JSON.stringify(state));

    expect(isAuthenticated()).toBe(false);
    expect(getToken()).toBeNull();
  });

  it('touchSession extends expiry', () => {
    setAuth('key', 'api_key');
    const before = getAuth()!.expiresAt;
    // Simulate time passing
    const raw = sessionStorage.getItem('agentplane_token');
    const state = JSON.parse(raw!);
    state.expiresAt = Date.now() + 1000; // almost expired
    sessionStorage.setItem('agentplane_token', JSON.stringify(state));

    touchSession();
    const after = getAuth()!.expiresAt;
    expect(after).toBeGreaterThan(before - 30 * 60 * 1000 + 1000);
  });
});
