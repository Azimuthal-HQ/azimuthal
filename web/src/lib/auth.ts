import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  createElement,
} from 'react';
import type { ReactNode } from 'react';

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

export function logout(): void {
  removeToken();
  removeRefreshToken();
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
  logout: () => void;
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

  const handleLogout = useCallback(() => {
    logout();
    setUser(null);
  }, []);

  // Periodically check token expiry
  useEffect(() => {
    const interval = setInterval(() => {
      const token = getToken();
      if (token && isTokenExpired(token)) {
        handleLogout();
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
