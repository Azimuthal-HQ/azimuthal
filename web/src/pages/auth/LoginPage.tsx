import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Logo } from '../../components/layout/Logo';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Card, CardContent, CardHeader } from '../../components/ui/card';
import { useAuth } from '../../lib/auth';
import { cn } from '../../lib/utils';

/** Full-page login form with centered card layout. */
export function LoginPage() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const { login } = useAuth();
  const navigate = useNavigate();

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      // Mock login: in a real app this would call the API
      // Simulate a short delay then store a fake JWT
      await new Promise((resolve) => setTimeout(resolve, 400));

      const mockPayload = btoa(
        JSON.stringify({ sub: 'u_1', exp: Math.floor(Date.now() / 1000) + 86400, iat: Math.floor(Date.now() / 1000), email, org_id: 'org_1', role: 'admin' }),
      );
      const mockToken = `eyJhbGciOiJIUzI1NiJ9.${mockPayload}.mock-signature`;
      const mockRefresh = 'mock-refresh-token';

      login(mockToken, mockRefresh);
      navigate('/');
    } catch {
      setError('Invalid email or password. Please try again.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div
      className={cn(
        'flex min-h-screen items-center justify-center',
        'bg-[var(--color-bg)] px-4',
      )}
    >
      <Card className="w-full max-w-sm">
        <CardHeader className="items-center space-y-4 pb-2">
          <Logo size={48} showText />
          <h1
            className="text-[var(--text-xl)] font-semibold text-[var(--color-text)]"
          >
            Sign in to Azimuthal
          </h1>
        </CardHeader>

        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            {error && (
              <p className="rounded-[var(--radius-md)] bg-[var(--color-danger)]/10 px-3 py-2 text-[var(--text-sm)] text-[var(--color-danger)]">
                {error}
              </p>
            )}

            <div className="space-y-2">
              <label
                htmlFor="email"
                className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
              >
                Email
              </label>
              <Input
                id="email"
                type="email"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                autoComplete="email"
              />
            </div>

            <div className="space-y-2">
              <label
                htmlFor="password"
                className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
              >
                Password
              </label>
              <Input
                id="password"
                type="password"
                placeholder="Enter your password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                autoComplete="current-password"
              />
            </div>

            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? 'Signing in...' : 'Sign in'}
            </Button>
          </form>

          <p className="mt-6 text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
            Don&apos;t have an account?{' '}
            <Link
              to="/signup"
              className="font-medium text-[var(--color-primary)] hover:text-[var(--color-primary-hover)]"
            >
              Sign up
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
