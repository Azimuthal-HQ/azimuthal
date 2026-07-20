import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useAcceptInvite,
  useInviteInspection,
} from '../../lib/api';
import { Logo } from '../../components/layout/Logo';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Input } from '../../components/ui/input';

/**
 * InviteAcceptPage (P2.5 W2): where an invite link lands. Public — the
 * token in the URL is the credential. A new email chooses a display name
 * and password and is signed in on the spot; an email with an existing
 * account confirms joining and then signs in with its own password —
 * acceptance never creates a second account.
 */
export function InviteAcceptPage() {
  const { token = '' } = useParams();
  const navigate = useNavigate();
  const inspection = useInviteInspection(token);
  const accept = useAcceptInvite();

  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [joinedExisting, setJoinedExisting] = useState<string | null>(null);

  const shell = (children: React.ReactNode) => (
    <main className="flex min-h-screen items-center justify-center bg-[var(--color-bg)] px-[var(--space-4)]">
      <div className="w-full max-w-md">
        <div className="mb-[var(--space-4)] flex justify-center"><Logo /></div>
        <Card>
          <CardContent className="p-[var(--space-6)]" data-testid="invite-accept-page">
            {children}
          </CardContent>
        </Card>
      </div>
    </main>
  );

  if (inspection.isLoading) {
    return shell(<p className="text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">Checking your invitation…</p>);
  }

  if (inspection.error || !inspection.data) {
    return shell(
      <div className="text-center">
        <h1 className="mb-[var(--space-2)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
          This invitation isn’t valid
        </h1>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          The link may be incomplete, or the invitation may have been replaced by a newer one.
          Ask the person who invited you to send a fresh link.
        </p>
      </div>,
    );
  }

  const inv = inspection.data;

  if (inv.state !== 'active') {
    const reason = {
      expired: 'This invitation has expired. Ask your admin to resend it — resending generates a fresh link.',
      revoked: 'This invitation has been revoked.',
      accepted: 'This invitation has already been used. If that was you, just sign in.',
    }[inv.state];
    return shell(
      <div className="text-center" data-testid="invite-dead-state">
        <h1 className="mb-[var(--space-2)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
          Invitation {inv.state}
        </h1>
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">{reason}</p>
        <Link to="/login" className="text-[var(--text-sm)] text-[var(--color-primary)] hover:underline">
          Go to sign in
        </Link>
      </div>,
    );
  }

  if (joinedExisting) {
    return shell(
      <div className="text-center" data-testid="invite-joined-existing">
        <h1 className="mb-[var(--space-2)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
          You’ve joined {joinedExisting}
        </h1>
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
          The organization was added to your existing account ({inv.email}). Sign in with your usual password.
        </p>
        <Button onClick={() => navigate('/login')} data-testid="invite-go-login">Sign in</Button>
      </div>,
    );
  }

  const submit = () => {
    setError(null);
    accept.mutate(
      inv.existing_account
        ? { token }
        : { token, display_name: displayName, password },
      {
        onSuccess: (res) => {
          if (res.existing_account) {
            setJoinedExisting(res.org_name);
          } else {
            navigate('/', { replace: true });
          }
        },
        onError: (err) => setError(friendlyErrorMessage(err, 'The invitation could not be accepted. Try again, or ask for a fresh link.')),
      },
    );
  };

  return shell(
    <div>
      <h1 className="mb-[var(--space-1)] text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
        Join {inv.org_name}
      </h1>
      <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
        You’ve been invited as <span className="text-[var(--color-text)]">{inv.email}</span>.
      </p>

      {inv.existing_account ? (
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]" data-testid="invite-existing-note">
          This email already has an Azimuthal account. Accepting adds {inv.org_name} to that
          account — you’ll keep your password and everything else.
        </p>
      ) : (
        <div className="mb-[var(--space-4)] space-y-[var(--space-3)]">
          <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
            Your name
            <Input
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="Ada Lovelace"
              data-testid="invite-display-name"
              className="mt-1"
            />
          </label>
          <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
            Choose a password
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="At least 8 characters"
              data-testid="invite-password"
              className="mt-1"
            />
          </label>
        </div>
      )}

      {error && <p className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-danger)]" data-testid="invite-accept-error">{error}</p>}

      <Button
        className={cn('w-full')}
        disabled={accept.isPending || (!inv.existing_account && (!displayName.trim() || password.length < 8))}
        onClick={submit}
        data-testid="invite-accept-submit"
      >
        {inv.existing_account ? `Join ${inv.org_name}` : 'Create account and join'}
      </Button>
    </div>,
  );
}
