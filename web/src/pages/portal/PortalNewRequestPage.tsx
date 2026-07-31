import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { friendlyErrorMessage, useSubmitPortalRequest } from '../../lib/api';
import { getPortalSession } from '../../lib/portalSession';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Field, FieldHint, FieldLabel } from '../../components/ui/field';
import { Input } from '../../components/ui/input';
import { portalRequestHref, portalRequestsHref } from './portalLinks';

/**
 * Compose a new request.
 *
 * The form has exactly the two fields the server accepts. `portal.NewRequest`
 * is deliberately narrower than `tickets.CreateTicketParams` — no priority (a
 * requester does not set their own urgency), no assignee, no labels, no due
 * date — and the handler rejects an unknown key with 400 rather than ignoring
 * it, so the request body is built literally as `{ summary, description }` and
 * nothing is spread into it.
 */
export function PortalNewRequestPage() {
  const { portalKey = '' } = useParams();
  const navigate = useNavigate();
  const email = getPortalSession(portalKey)?.requester.email ?? '';
  const submitRequest = useSubmitPortalRequest(portalKey, email);

  const [summary, setSummary] = useState('');
  const [description, setDescription] = useState('');
  const [error, setError] = useState<string | null>(null);

  const canSubmit = summary.trim().length > 0 && !submitRequest.isPending;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setError(null);
    submitRequest.mutate(
      { summary: summary.trim(), description: description.trim() },
      {
        onSuccess: (created) => {
          const reference = referenceOf(created);
          navigate(
            reference ? portalRequestHref(portalKey, reference) : portalRequestsHref(portalKey),
            { replace: true },
          );
        },
        onError: (err: unknown) =>
          setError(
            friendlyErrorMessage(
              err,
              'Your request could not be sent just now. Try again in a moment.',
            ),
          ),
      },
    );
  };

  return (
    <Card>
      <CardContent className="p-[var(--space-6)]" data-testid="portal-new-request-page">
        <h2 className="mb-[var(--space-1)] text-[var(--text-lg)] font-semibold text-[var(--color-text)]">
          Raise a request
        </h2>
        <p className="mb-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Tell us what you need. You’ll be able to follow it and reply here.
        </p>

        <form onSubmit={submit} noValidate>
          <Field>
            <FieldLabel htmlFor="portal-summary">Summary</FieldLabel>
            <Input
              id="portal-summary"
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
              placeholder="A one-line description"
              data-testid="portal-summary"
            />
          </Field>

          <Field>
            <FieldLabel htmlFor="portal-description" optional>
              Details
            </FieldLabel>
            <textarea
              id="portal-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={8}
              placeholder="Anything that helps us understand the request"
              data-testid="portal-description"
              className="w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-input)] px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]"
            />
            <FieldHint>Markdown is supported.</FieldHint>
          </Field>

          {error && (
            <p
              className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-danger)]"
              data-testid="portal-new-request-error"
            >
              {error}
            </p>
          )}

          <div className="flex items-center gap-[var(--space-2)]">
            <Button type="submit" disabled={!canSubmit} data-testid="portal-new-request-submit">
              {submitRequest.isPending ? 'Sending…' : 'Send request'}
            </Button>
            <Button variant="ghost" asChild>
              <Link to={portalRequestsHref(portalKey)}>Cancel</Link>
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

/**
 * The new request's reference, if the response carried one.
 *
 * Read structurally rather than by type assertion so that a response shape
 * change degrades to "go to the list" instead of navigating to
 * `…/requests/undefined`.
 */
function referenceOf(created: unknown): string | null {
  if (created && typeof created === 'object' && 'reference' in created) {
    const reference = (created as { reference?: unknown }).reference;
    if (typeof reference === 'string' && reference.trim()) return reference;
  }
  return null;
}
