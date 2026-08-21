import { useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Logo } from '../../components/layout/Logo';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Card, CardContent, CardHeader } from '../../components/ui/card';
import { useForgotPassword } from '../../lib/api';
import { cn } from '../../lib/utils';

/**
 * ForgotPasswordPage (D1): self-service password reset. Public — no account is
 * signed in here. The confirmation is DELIBERATELY the same whether or not the
 * address is known: the endpoint is not an account-existence oracle, and neither
 * is this page. If a mail relay is configured the link is emailed; if not, the
 * administrator issues one instead (see docs/self-hosting.md).
 */
export function ForgotPasswordPage() {
  const [email, setEmail] = useState('');
  const [submitted, setSubmitted] = useState(false);
  const forgot = useForgotPassword();

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    // Fire and forget: the response is identical for known and unknown
    // addresses, so there is nothing to branch on. Even a transport error is not
    // surfaced differently — that too would leak.
    forgot.mutate(email.trim());
    setSubmitted(true);
  }

  return (
    <div className={cn('flex min-h-screen items-center justify-center', 'bg-[var(--color-bg)] px-4')}>
      <Card className="w-full max-w-sm">
        <CardHeader className="items-center space-y-4 pb-2">
          <Logo size={48} showText />
          <h1 className="text-[var(--text-xl)] font-semibold text-[var(--color-text)]">Reset your password</h1>
        </CardHeader>

        <CardContent>
          {submitted ? (
            <div className="space-y-4 text-center" data-testid="forgot-password-sent">
              <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
                If an account exists for that address, a reset link is on its way. It works once and
                expires shortly. If you don’t receive it, ask your administrator to issue one.
              </p>
              <Link
                to="/login"
                className="inline-block text-[var(--text-sm)] font-medium text-[var(--color-primary)] hover:text-[var(--color-primary-hover)]"
              >
                Back to sign in
              </Link>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-4">
              <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
                Enter your email address and we’ll send a link to set a new password.
              </p>
              <div className="space-y-2">
                <label htmlFor="email" className="block text-[var(--text-sm)] font-medium text-[var(--color-text)]">
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
                  data-testid="forgot-password-email"
                />
              </div>
              <Button type="submit" className="w-full" disabled={forgot.isPending || !email.trim()} data-testid="forgot-password-submit">
                Send reset link
              </Button>
              <p className="text-center text-[var(--text-sm)]">
                <Link to="/login" className="font-medium text-[var(--color-primary)] hover:text-[var(--color-primary-hover)]">
                  Back to sign in
                </Link>
              </p>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
