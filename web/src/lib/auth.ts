import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  createElement,
} from 'react';
import type { ReactNode } from 'react';
// `api` imports the token helpers from this module, so this edge closes a
// cycle. It is safe and deliberate rather than overlooked: neither module
// touches the other's bindings while it is evaluating — `revokeSession` is
// only ever called from inside `logout()` — and it is a hoisted function
// declaration, so the binding exists whichever module the bundler evaluates
// first. The alternative was to leave the server call to whoever happens to
// call `logout()`, which would mean a future sign-out surface silently not
// revoking anything. CLAUDE.md §1 allows exactly one network client, so the
// fetch has to live in `api.ts`; only the direction of the import was open.
import { revokeSession } from './api';

// ---------------------------------------------------------------------------
// Token storage helpers
// ---------------------------------------------------------------------------

const ACCESS_TOKEN_KEY = 'azimuthal_access_token';
const REFRESH_TOKEN_KEY = 'azimuthal_refresh_token';

export function getToken(): string | null {
  return localStorage.getItem(ACCESS_TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(ACCESS_TOKEN_KEY, token);
}

export function removeToken(): void {
  localStorage.removeItem(ACCESS_TOKEN_KEY);
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_KEY);
}

export function setRefreshToken(token: string): void {
  localStorage.setItem(REFRESH_TOKEN_KEY, token);
}

export function removeRefreshToken(): void {
  localStorage.removeItem(REFRESH_TOKEN_KEY);
}

// ---------------------------------------------------------------------------
// JWT helpers
// ---------------------------------------------------------------------------

interface JWTPayload {
  sub: string;
  exp: number;
  iat: number;
  email: string;
  org_id: string;
  role: string;
}

function decodeJWTPayload(token: string): JWTPayload | null {
  try {
    const parts = token.split('.');
    if (parts.length !== 3) return null;
    const payload = parts[1];
    const decoded = atob(payload.replace(/-/g, '+').replace(/_/g, '/'));
    return JSON.parse(decoded) as JWTPayload;
  } catch {
    return null;
  }
}

function isTokenExpired(token: string): boolean {
  const payload = decodeJWTPayload(token);
  if (!payload) return true;
  // Consider expired if less than 30 seconds remain
  return payload.exp * 1000 < Date.now() + 30_000;
}

export function isAuthenticated(): boolean {
  const token = getToken();
  if (!token) return false;
  return !isTokenExpired(token);
}

// getCurrentOrgId returns the org the current session belongs to, decoded
// from the stored JWT. Every space resource URL is org+space scoped, so the
// API client needs this without threading orgId through each component.
export function getCurrentOrgId(): string {
  const token = getToken();
  if (!token) return '';
  return decodeJWTPayload(token)?.org_id ?? '';
}

/**
 * logout revokes the session on the server, then forgets it locally.
 *
 * The order matters and so does the finally. Revoking needs the token, so it
 * has to happen before the clear; and the clear has to happen whatever the
 * network did, because a person who has pressed Sign out must end up signed
 * out of this browser even if the server is unreachable. Before the v0.4.1
 * trust patch this function was the local clear alone, which meant a normal
 * sign-out only made the browser forget a credential that went on working.
 *
 * Two limits, stated rather than glossed:
 *
 *  - A FAILED REVOCATION IS SWALLOWED, AND THE PERSON IS NOT TOLD. The server
 *    answers 500 when it could not revoke, and it does that deliberately — see
 *    Handler.Logout, which refuses to say "logged out" to somebody who may be
 *    signing out because they think they are compromised. That information
 *    dies here. Throwing instead would leave the caller navigating away from a
 *    rejected promise, and there is no surface in this app for the message, so
 *    swallowing is what the change shipped with; it is a gap in the product,
 *    not a considered position, and it is recorded as such rather than
 *    dressed up. The session survives on the server until the token expires.
 *  - THE EXPIRY-TIMER PATH USUALLY CANNOT REVOKE. AuthProvider's timer fires
 *    when isTokenExpired says so, which is true from 30 SECONDS BEFORE expiry
 *    onwards — so inside that window the token is still valid server-side and
 *    the revocation succeeds; past it the request 401s and nothing is revoked.
 *    Note the refresh token is still in storage at that moment, so a
 *    refresh-then-revoke would close the gap. It is not done here because it
 *    turns a timer tick into two network round trips on every idle session,
 *    and the case that matters — a deliberate Sign out — always carries a live
 *    token.
 */
export async function logout(): Promise<void> {
  try {
    // No token means nothing to revoke, and an unauthenticated POST would only
    // produce a 401 to swallow.
    if (getToken()) {
      await revokeSession();
    }
  } catch {
    // Deliberately swallowed — see the note above. The clear below is what
    // signs the person out of this browser, and it runs regardless.
  } finally {
    removeToken();
    removeRefreshToken();
  }
}

// ---------------------------------------------------------------------------
// User type derived from JWT
// ---------------------------------------------------------------------------

export interface AuthUser {
  id: string;
  email: string;
  orgId: string;
  role: string;
}

function userFromToken(token: string): AuthUser | null {
  const payload = decodeJWTPayload(token);
  if (!payload) return null;
  return {
    id: payload.sub,
    email: payload.email,
    orgId: payload.org_id,
    role: payload.role,
  };
}

// ---------------------------------------------------------------------------
// Auth context
// ---------------------------------------------------------------------------

interface AuthContextValue {
  user: AuthUser | null;
  isAuthenticated: boolean;
  login: (accessToken: string, refreshToken: string) => void;
  // Async since the v0.4.1 trust patch: signing out now waits for the server
  // to revoke the token. A caller that navigates away should await it, so the
  // request is not abandoned mid-flight.
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export { AuthContext };

// ---------------------------------------------------------------------------
// AuthProvider
// ---------------------------------------------------------------------------

interface AuthProviderProps {
  children: ReactNode;
}

export function AuthProvider({ children }: AuthProviderProps) {
  const [user, setUser] = useState<AuthUser | null>(() => {
    const token = getToken();
    if (token && !isTokenExpired(token)) {
      return userFromToken(token);
    }
    return null;
  });

  const handleLogin = useCallback((accessToken: string, refreshToken: string) => {
    setToken(accessToken);
    setRefreshToken(refreshToken);
    setUser(userFromToken(accessToken));
  }, []);

  const handleLogout = useCallback(async () => {
    await logout();
    setUser(null);
  }, []);

  // Periodically check token expiry
  useEffect(() => {
    const interval = setInterval(() => {
      const token = getToken();
      if (token && isTokenExpired(token)) {
        // Not awaited: this is a timer, there is nobody to report to, and
        // handleLogout swallows its own network failure. See logout()'s note
        // on why the revocation attempt cannot succeed on this path.
        void handleLogout();
      }
    }, 60_000);
    return () => clearInterval(interval);
  }, [handleLogout]);

  const value: AuthContextValue = {
    user,
    isAuthenticated: user !== null,
    login: handleLogin,
    logout: handleLogout,
  };

  return createElement(AuthContext.Provider, { value }, children);
}

// ---------------------------------------------------------------------------
// useAuth hook
// ---------------------------------------------------------------------------

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return ctx;
}
