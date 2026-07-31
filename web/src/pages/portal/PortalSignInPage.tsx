import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { Mail } from 'lucide-react';
import { friendlyErrorMessage, useRequestPortalLink } from '../../lib/api';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Field, FieldLabel } from '../../components/ui/field';
import { Input } from '../../components/ui/input';
import { EmptyState } from '../../shell/EmptyState';

/**
 * Where a requester asks for a sign-in link.
 *
 * TWO THINGS HERE ARE SECURITY BEHAVIOUR, not copywriting.
 *
 * The response NEVER discloses the link. `requestLinkResponse` carries
 * `magic_link_url` in development and test configurations, and rendering it
 * would hand a sign-in credential to whoever typed an address into an
 * unauthenticated form — an authentication bypass reachable by anyone who
 * knows a customer's email. This component does not read that field at all,
 * which is a stronger guarantee than remembering not to display it.
 *
 * The confirmation NEVER claims the address was found. The endpoint answers
 * 202 identically for a known address, an unknown one and a deactivated one
 * (`portal.Service.RequestLink`) precisely so the form cannot be used to
 * enumerate a company's customers. Copy that said "we've emailed you" would
 * undo that server-side decision in the browser, so the terminal state is
 * phrased conditionally.
 */
export function PortalSignInPage() {
  const { portalKey = '' } = useParams();
  const requestLink = useRequestPortalLink(portalKey);

  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const address = email.trim();
    if (!address) return;
    setError(null);
    requestLink.mutate(
      { email: address, name: name.trim() || undefined },
      {
        // The response is deliberately uninformative and is not read here.
        onSuccess: () => setSentTo(address),
        onError: (err: unknown) =>
          setError(
            friendlyErrorMessage(
              err,
              'The sign-in link could not be requested just now. Check the address and try again.',
            ),
          ),
      },
    );
  };

  if (sentTo) {
    return (
      <Card>
        <CardContent className="p-[var(--space-6)]" data-testid="portal-link-sent">
          <EmptyState
            icon={Mail}
            title="Check your inbox"
            description={`If ${sentTo} can raise requests here, a sign-in link is on its way. The link works once and expires shortly — request another if it does.`}
            className="border-none py-[var(--space-6)]"
            action={
              <Button
                variant="outline"
                onClick={() => {
                  setSentTo(null);
                  setError(null);
                }}
                data-testid="portal-link-again"
              >
                Use a different address
              </Button>
            }
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardContent className="p-[var(--space-6)]" data-testid="portal-signin-page">
        <h2 className="mb-[var(--space-1)] text-[var(--text-lg)] font-semibold text-[var(--color-text)]">
          Sign in to track your requests
        </h2>
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Enter your email and we’ll send you a sign-in link. There’s no password to remember.
        </p>

        <form onSubmit={submit} noValidate>
          <Field>
            <FieldLabel htmlFor="portal-email">Email address</FieldLabel>
            <Input
              id="portal-email"
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
              data-testid="portal-email"
            />
          </Field>

          <Field>
            <FieldLabel htmlFor="portal-name" optional>
              Your name
            </FieldLabel>
            <Input
              id="portal-name"
              autoComplete="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="How we should address you"
              data-testid="portal-name-input"
            />
          </Field>

          {error && (
            <p
              className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-danger)]"
              data-testid="portal-signin-error"
            >
              {error}
            </p>
          )}

          <Button
            type="submit"
            className="w-full"
            disabled={requestLink.isPending || !email.trim()}
            data-testid="portal-signin-submit"
          >
            {requestLink.isPending ? 'Sending…' : 'Email me a sign-in link'}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
