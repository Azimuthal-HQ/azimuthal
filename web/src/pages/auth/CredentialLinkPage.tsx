import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useConsumeCredentialLink,
  useInspectCredentialLink,
  type CredentialPurpose,
} from '../../lib/api';
import { Logo } from '../../components/layout/Logo';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Input } from '../../components/ui/input';

/**
 * CredentialLinkPage (D1): where an internal-user credential link lands. Public —
 * possession of the token in the URL is the credential, exactly like the invite
 * and portal redemption pages. It inspects the link (without consuming it) to
 * learn its purpose, then renders the right thing:
 *
 *   - signin / password_reset: collect a new password. A sign-in link signs the
 *     user in on the spot; a reset asks them to sign in with the new password
 *     (every existing session was just revoked).
 *   - email_change: confirm the pending address. On confirm the account signs
 *     out everywhere (the token generation is bumped — the C.2-c fix) and the
 *     user signs back in with the new address.
 */
export function CredentialLinkPage() {
  const { token = '' } = useParams();
  const navigate = useNavigate();
  const inspection = useInspectCredentialLink(token);
  const consume = useConsumeCredentialLink();

  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<CredentialPurpose | null>(null);

  const shell = (children: React.ReactNode) => (
    <main className="flex min-h-screen items-center justify-center bg-[var(--color-bg)] px-[var(--space-4)]">
      <div className="w-full max-w-md">
        <div className="mb-[var(--space-4)] flex justify-center"><Logo /></div>
        <Card>
          <CardContent className="p-[var(--space-6)]" data-testid="credential-link-page">
            {children}
          </CardContent>
        </Card>
      </div>
    </main>
  );

  if (inspection.isLoading) {
    return shell(<p className="text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">Checking your link…</p>);
  }

  if (inspection.error || !inspection.data) {
    return shell(
      <div className="text-center" data-testid="credential-link-invalid">
        <h1 className="mb-[var(--space-2)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
          This link isn’t valid
        </h1>
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
          The link may be incomplete, already used, or expired. Ask your administrator for a fresh one,
          or use “Forgot password?” to request a new one.
        </p>
        <Link to="/login" className="text-[var(--text-sm)] text-[var(--color-primary)] hover:underline">
          Go to sign in
        </Link>
      </div>,
    );
  }

  // Redemption succeeded on a link that does not sign the user in.
  if (done === 'password_reset' || done === 'email_change') {
    const heading = done === 'password_reset' ? 'Password updated' : 'Email address updated';
    const note =
      done === 'password_reset'
        ? 'Your password has been set and every device has been signed out. Sign in with your new password.'
        : 'Your email address has been confirmed and every device has been signed out. Sign in with your new address.';
    return shell(
      <div className="text-center" data-testid="credential-link-done">
        <h1 className="mb-[var(--space-2)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">{heading}</h1>
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">{note}</p>
        <Button onClick={() => navigate('/login')} data-testid="credential-link-go-login">Sign in</Button>
      </div>,
    );
  }

  const { purpose, new_email: newEmail } = inspection.data;
  const needsPassword = purpose === 'signin' || purpose === 'password_reset';

  const submit = () => {
    setError(null);
    consume.mutate(
      { token, password: needsPassword ? password : undefined },
      {
        onSuccess: (res) => {
          if (res.purpose === 'signin') {
            // The hook stored the session; land in the app.
            navigate('/', { replace: true });
          } else {
            setDone(res.purpose);
          }
        },
        onError: (err) => setError(friendlyErrorMessage(err, 'This link could not be redeemed. Ask for a fresh one.')),
      },
    );
  };

  const copy = {
    signin: {
      heading: 'Set your password',
      lead: 'Your account is ready. Choose a password to finish setting it up and sign in.',
      cta: 'Set password and sign in',
    },
    password_reset: {
      heading: 'Choose a new password',
      lead: 'Set a new password for your account. This signs you out of every other device.',
      cta: 'Set new password',
    },
    email_change: {
      heading: 'Confirm your new email address',
      lead: `Confirm ${newEmail ?? 'this address'} for your account. Confirming signs you out of every device.`,
      cta: 'Confirm email address',
    },
  }[purpose];

  return shell(
    <div>
      <h1 className="mb-[var(--space-1)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
        {copy.heading}
      </h1>
      <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">{copy.lead}</p>

      {needsPassword && (
        <label className="mb-[var(--space-4)] block text-[var(--text-sm)] text-[var(--color-text)]">
          New password
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="At least 8 characters"
            data-testid="credential-link-password"
            className="mt-1"
          />
        </label>
      )}

      {error && (
        <p className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-danger)]" data-testid="credential-link-error">
          {error}
        </p>
      )}

      <Button
        className={cn('w-full')}
        disabled={consume.isPending || (needsPassword && password.length < 8)}
        onClick={submit}
        data-testid="credential-link-submit"
      >
        {copy.cta}
      </Button>
    </div>,
  );
}
