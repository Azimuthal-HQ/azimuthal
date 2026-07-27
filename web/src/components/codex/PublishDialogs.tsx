/**
 * The three refusals a publish can come back with, each given a real
 * treatment rather than an error toast.
 *
 * PR #73 made the order of those refusals the contract, and two of them are
 * states an author can resolve. A toast saying "conflict" throws away the
 * server's own account of what happened and leaves the author with no move.
 *
 * ## On showing the server's message verbatim
 *
 * `shared-surfaces.md` section 2 says no raw backend string reaches a user,
 * and `friendlyErrorMessage` enforces it by passing through only
 * `VALIDATION_ERROR`, `CONFLICT` and `GONE`. These two 409 bodies carry no
 * code at all — they are not error envelopes — so `friendlyErrorMessage`
 * would swallow them into a fallback.
 *
 * They are a deliberate exception, and the reason is in the handler:
 * "Both 409 bodies are prose written for a person … this text IS the dialogue
 * the author reads" (`internal/core/api/wiki/document_handler.go:218`). The
 * strings are written for this dialog, they name the versions and counts
 * involved, and restating them client-side would mean maintaining the same
 * sentence in two languages and letting them drift. So `detail.message` is
 * shown as given — from these two typed errors only, never from an APIError.
 */
import { AlertTriangle, RotateCcw, ShieldAlert } from 'lucide-react';

import type { CodexPublishConflict, CodexPublishLostContent } from '../../lib/api';
import { Button } from '../ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../ui/dialog';
import { preservedSummary } from './preserved';

// ---------------------------------------------------------------------------
// Version conflict — the page moved on under the author
// ---------------------------------------------------------------------------

interface PublishConflictDialogProps {
  detail: CodexPublishConflict;
  /** Reload the page's published document. The draft is kept either way. */
  onReload: () => void;
  /** Publish over the newer version. */
  onOverwrite: () => void;
  onCancel: () => void;
  busy?: boolean;
}

export function PublishConflictDialog({
  detail,
  onReload,
  onOverwrite,
  onCancel,
  busy,
}: PublishConflictDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent data-testid="codex-conflict-dialog">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <RotateCcw className="h-4 w-4 text-[var(--color-warning)]" aria-hidden="true" />
            This page changed while you were editing
          </DialogTitle>
          <DialogDescription data-testid="codex-conflict-message">
            {detail.message}
          </DialogDescription>
        </DialogHeader>

        <div className="rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] px-3 py-2 text-[var(--text-sm)]">
          <p className="text-[var(--color-text-muted)]">
            Your draft started from version{' '}
            <strong className="text-[var(--color-text)]">{detail.expected_version}</strong>. The
            page is now at version{' '}
            <strong className="text-[var(--color-text)]">{detail.current_page.version}</strong>.
          </p>
        </div>

        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            Keep editing
          </Button>
          <Button variant="secondary" onClick={onReload} disabled={busy} data-testid="codex-conflict-reload">
            Reload the new version
          </Button>
          {/* Worded as what it does, not as the easy way out: this discards
              somebody else's published work. */}
          <Button onClick={onOverwrite} disabled={busy} data-testid="codex-conflict-overwrite">
            {busy ? 'Publishing…' : 'Replace it with mine'}
          </Button>
        </DialogFooter>

        <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
          Reloading keeps your draft — you will be shown the newer page and can re-apply your
          changes. Replacing publishes your version over theirs; their edits stay in the page
          history.
        </p>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Preserved content would be lost — the ADR-0012 catastrophe, caught
// ---------------------------------------------------------------------------

interface LostContentDialogProps {
  detail: CodexPublishLostContent;
  /** Republish with these ids in `acknowledged_lost_ids`. */
  onConfirm: () => void;
  onCancel: () => void;
  busy?: boolean;
}

export function LostContentDialog({ detail, onConfirm, onCancel, busy }: LostContentDialogProps) {
  const count = detail.lost.length || detail.lost_ids.length;

  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent data-testid="codex-lost-content-dialog">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlert className="h-4 w-4 text-[var(--color-danger)]" aria-hidden="true" />
            Publishing will permanently remove preserved content
          </DialogTitle>
          <DialogDescription data-testid="codex-lost-content-count">
            {/* The count is the server's, not a count of what this client
                happens to have loaded — shared-surfaces.md section 6. It is
                computed by comparing the document being published against the
                one the draft started from. */}
            This will permanently remove {count} preserved item{count === 1 ? '' : 's'}:
          </DialogDescription>
        </DialogHeader>

        <ul
          data-testid="codex-lost-content-list"
          className="max-h-56 space-y-1.5 overflow-y-auto rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] p-2"
        >
          {(detail.lost.length ? detail.lost : detail.lost_ids.map((id) => ({ id, name: '', text: '' }))).map(
            (item) => (
              <li
                key={item.id}
                data-testid="codex-lost-content-item"
                className="flex gap-2 text-[var(--text-sm)] text-[var(--color-text)]"
              >
                <AlertTriangle
                  className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[var(--color-warning)]"
                  aria-hidden="true"
                />
                <span className="min-w-0 break-words">{preservedSummary(item.name, item.text)}</span>
              </li>
            ),
          )}
        </ul>

        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {detail.message}
        </p>

        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onCancel} disabled={busy} data-testid="codex-lost-content-cancel">
            Cancel — keep this content
          </Button>
          <Button
            variant="destructive"
            onClick={onConfirm}
            disabled={busy}
            data-testid="codex-lost-content-confirm"
          >
            {busy ? 'Publishing…' : `Remove ${count} item${count === 1 ? '' : 's'} and publish`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// The placeholder that cannot be resolved — a 422, not a choice
// ---------------------------------------------------------------------------

interface UnresolvablePreservedDialogProps {
  /** The server's VALIDATION_ERROR prose, which is written for a person. */
  message: string;
  /** Reload the document and start again from the published version. */
  onReload: () => void;
  onCancel: () => void;
}

/**
 * The 422: the document carries a preservation placeholder with no original
 * behind it, so the edit cannot be matched to the version it started from.
 *
 * Deliberately offers no "publish anyway". PR #73 draws this line and it is
 * the right one — a 409 is a state the author can resolve, a 422 means the
 * request does not add up, and offering a choice that cannot work would send
 * them round a loop. The one real move is to reload, which is offered here
 * with the draft explicitly accounted for.
 */
export function UnresolvablePreservedDialog({
  message,
  onReload,
  onCancel,
}: UnresolvablePreservedDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent data-testid="codex-unresolvable-dialog">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlert className="h-4 w-4 text-[var(--color-danger)]" aria-hidden="true" />
            This edit does not match the page it started from
          </DialogTitle>
          <DialogDescription data-testid="codex-unresolvable-message">{message}</DialogDescription>
        </DialogHeader>

        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Nothing has been published and nothing in the page has changed. Reloading fetches the
          current published version so you can re-apply your changes to it.
        </p>

        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onCancel}>
            Keep editing
          </Button>
          <Button onClick={onReload} data-testid="codex-unresolvable-reload">
            Reload the page
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
