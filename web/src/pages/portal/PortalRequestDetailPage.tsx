import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { AlertCircle, SearchX } from 'lucide-react';
import {
  friendlyErrorMessage,
  usePortalRequest,
  useReplyToPortalRequest,
  type PortalMessage,
} from '../../lib/api';
import { getPortalSession } from '../../lib/portalSession';
import { cn } from '../../lib/utils';
import { Markdown } from '../../components/Markdown';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { EmptyState } from '../../shell/EmptyState';
import { PortalStatus } from '../../components/portal/PortalStatus';
import { portalRequestsHref } from './portalLinks';

/**
 * One request and its public conversation.
 *
 * WHAT IS SHOWN IS WHAT THE WIRE CARRIES. `requestView` is summary, status,
 * two timestamps and an optional description; `messageView` is an author
 * label, a body, a timestamp and `from_requester`. There is no space, no
 * ticket number, no assignee and no internal comment — the portal's comment
 * read is its own SQL statement carrying a literal `visibility = 'public'`, so
 * an internal note cannot arrive here to be filtered out in the browser.
 *
 * `description` is `omitempty` on the wire, so it is ABSENT rather than empty
 * when there is none — `?? ''` is the difference between a fallback and a
 * crash on `.trim()`.
 *
 * MARKDOWN IS RENDERED WITHOUT `rehype-raw`. Every body on this page is
 * authored by an external customer or shown to one, and there is no sanitiser
 * anywhere in this codebase. `WikiPage.tsx` enables raw HTML for legacy wiki
 * content at its own call site; routing external text through that path would
 * be stored XSS aimed straight at the agent who opens the ticket. The plain
 * `<Markdown>` form escapes embedded HTML by default, which is the whole point
 * of the shared component.
 */
export function PortalRequestDetailPage() {
  const { portalKey = '', reference = '' } = useParams();
  const email = getPortalSession(portalKey)?.requester.email ?? '';
  const detail = usePortalRequest(portalKey, email, reference);
  const reply = useReplyToPortalRequest(portalKey, email, reference);

  const [body, setBody] = useState('');
  const [replyError, setReplyError] = useState<string | null>(null);

  const sendReply = (e: React.FormEvent) => {
    e.preventDefault();
    const text = body.trim();
    if (!text || reply.isPending) return;
    setReplyError(null);
    reply.mutate(
      { body: text },
      {
        onSuccess: () => setBody(''),
        onError: (err: unknown) =>
          setReplyError(
            friendlyErrorMessage(err, 'Your reply could not be sent. Try again in a moment.'),
          ),
      },
    );
  };

  const back = (
    <Link
      to={portalRequestsHref(portalKey)}
      className="text-[var(--text-sm)] text-[var(--color-primary)] hover:underline"
      data-testid="portal-detail-back"
    >
      ← All your requests
    </Link>
  );

  // Error before loading before empty before content — the order SearchBody
  // fixed. A failed fetch rendered as "no messages" would tell a customer the
  // team has said nothing, which is a different and worse claim.
  if (detail.error) {
    return (
      <div data-testid="portal-request-detail-page">
        <div className="mb-[var(--space-3)]">{back}</div>
        <EmptyState
          icon={AlertCircle}
          title="This request couldn’t be loaded"
          description={friendlyErrorMessage(
            detail.error,
            'We couldn’t open that request. It may have been closed, or the link may be out of date.',
          )}
          className="bg-[var(--color-surface)]"
          action={
            <Button
              variant="outline"
              onClick={() => void detail.refetch()}
              data-testid="portal-detail-retry"
            >
              Try again
            </Button>
          }
        />
      </div>
    );
  }

  if (detail.isLoading || !detail.data) {
    return (
      <div data-testid="portal-request-detail-page">
        <div className="mb-[var(--space-3)]">{back}</div>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading this request…</p>
      </div>
    );
  }

  const { request, messages } = detail.data;

  return (
    <div data-testid="portal-request-detail-page">
      <div className="mb-[var(--space-3)]">{back}</div>

      <Card className="mb-[var(--space-4)]">
        <CardContent className="p-[var(--space-6)]">
          <div className="flex items-start justify-between gap-[var(--space-3)]">
            <h2 className="text-[var(--text-xl)] font-semibold text-[var(--color-text)]">
              {request.summary}
            </h2>
            <PortalStatus status={request.status} />
          </div>
          <p className="mt-[var(--space-1)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
            Raised {new Date(request.created_at).toLocaleDateString()} · Updated{' '}
            {new Date(request.updated_at).toLocaleDateString()}
          </p>

          <div className="mt-[var(--space-4)]">
            <Markdown
              testId="portal-request-description"
              fallback={
                <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]">
                  No further details were given.
                </p>
              }
            >
              {request.description ?? ''}
            </Markdown>
          </div>
        </CardContent>
      </Card>

      <h3 className="mb-[var(--space-2)] text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
        Conversation
      </h3>

      {messages.length === 0 ? (
        <EmptyState
          icon={SearchX}
          title="No replies yet"
          description="When someone answers your request, their reply appears here. You can add more detail below at any time."
          className="mb-[var(--space-4)] bg-[var(--color-surface)]"
        />
      ) : (
        <ul className="mb-[var(--space-4)] flex flex-col gap-[var(--space-2)]">
          {messages.map((message) => (
            <li key={message.id}>
              <MessageBubble message={message} />
            </li>
          ))}
        </ul>
      )}

      <Card>
        <CardContent className="p-[var(--space-4)]">
          <form onSubmit={sendReply} noValidate>
            <label
              htmlFor="portal-reply"
              className="mb-[var(--space-2)] block text-[var(--text-sm)] font-medium text-[var(--color-text)]"
            >
              Add a reply
            </label>
            <textarea
              id="portal-reply"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={4}
              placeholder="Anything else we should know?"
              data-testid="portal-reply-body"
              className="w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-input)] px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]"
            />
            {replyError && (
              <p
                className="mt-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-danger)]"
                data-testid="portal-reply-error"
              >
                {replyError}
              </p>
            )}
            <div className="mt-[var(--space-3)]">
              <Button
                type="submit"
                disabled={!body.trim() || reply.isPending}
                data-testid="portal-reply-submit"
              >
                {reply.isPending ? 'Sending…' : 'Send reply'}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}

/**
 * One message in the thread.
 *
 * `author` may be the empty string — the wire field is a display name and an
 * account need not have one. The fallback is chosen from `from_requester`,
 * which is exactly what that flag is on the wire for, and it is deliberately
 * generic: naming the space, the team or the product here would put back the
 * container identity that the rest of this surface exists to withhold.
 */
function MessageBubble({ message }: { message: PortalMessage }) {
  const mine = message.from_requester;
  const author = message.author?.trim() || (mine ? 'You' : 'The support team');
  return (
    <Card
      className={cn(mine && 'border-[var(--color-primary)]')}
      data-testid="portal-message"
      data-from-requester={mine ? 'true' : 'false'}
    >
      <CardContent className="p-[var(--space-4)]">
        <p className="mb-[var(--space-2)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
          <span className="font-medium text-[var(--color-text)]">{author}</span> ·{' '}
          {new Date(message.created_at).toLocaleDateString()}
        </p>
        <Markdown>{message.body ?? ''}</Markdown>
      </CardContent>
    </Card>
  );
}
