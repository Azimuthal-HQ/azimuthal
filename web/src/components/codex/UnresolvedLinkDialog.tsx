/**
 * What happens when a reader clicks a link to a page that does not exist yet.
 *
 * ## Why this is a dialogue and not a button
 *
 * The obvious implementation creates the page and navigates to it. That is
 * wrong in one specific and likely case: the link was written weeks ago, and
 * somebody has since created a page by that name. Blind-creating would make a
 * second page with the same title, leave the link pointing at the empty one,
 * and split the content in two — quietly, and in a way nobody notices until
 * they search for it.
 *
 * So when a page of that name already exists, this offers BOTH, with the
 * existing page first. Only when there is genuinely no match is creating the
 * single obvious action.
 *
 * ## What "resolve in place" means
 *
 * Creating a page here does not rewrite the document. The link is unresolved
 * until the next publish, which is deliberate: a reader is not editing, and
 * rewriting a published document from a reading surface would be a write
 * nobody asked for. The author's next edit resolves it — the `[[…]]` input
 * rule finds the page that now exists — and publishing stores the resolved
 * form. Until then the link navigates to the page it just created, so it
 * behaves as resolved even though it is not yet stored that way.
 */
import { useState } from 'react';
import { FileText, Plus } from 'lucide-react';
import { useNavigate } from 'react-router-dom';

import { friendlyErrorMessage, useCreateWikiPage } from '../../lib/api';
import type { WikiPage } from '../../lib/api';
import { Button } from '../ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../ui/dialog';
import { findPageByTitle } from './pageSearch';

interface UnresolvedLinkDialogProps {
  /** The title the author wrote inside `[[…]]`. */
  targetTitle: string;
  spaceId: string;
  pages: WikiPage[];
  onClose: () => void;
}

export function UnresolvedLinkDialog({
  targetTitle,
  spaceId,
  pages,
  onClose,
}: UnresolvedLinkDialogProps) {
  const navigate = useNavigate();
  const createPage = useCreateWikiPage(spaceId);
  const [error, setError] = useState<string | null>(null);

  // The same lookup the `[[…]]` input rule uses, so "already exists" means the
  // same thing in both places. A second, looser match here would offer to link
  // to a page the editor would not have resolved to.
  const existing = findPageByTitle(pages, targetTitle);

  async function handleCreate() {
    setError(null);
    try {
      const page = await createPage.mutateAsync({ title: targetTitle, content: '' });
      navigate(`/codex/${spaceId}/pages/${page.id}`);
      onClose();
    } catch (err) {
      setError(friendlyErrorMessage(err, 'That page could not be created.'));
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-testid="codex-unresolved-link-dialog">
        <DialogHeader>
          <DialogTitle>“{targetTitle}” has not been written yet</DialogTitle>
          <DialogDescription>
            {existing
              ? 'A page with this title already exists in this space. Open it rather than creating a second one — two pages with the same name split the content between them.'
              : 'This link names a page that does not exist. Creating it now takes you straight to a blank page with this title.'}
          </DialogDescription>
        </DialogHeader>

        {error && (
          <p data-testid="codex-unresolved-link-error" className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {error}
          </p>
        )}

        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onClose}>
            Leave it for now
          </Button>

          {existing && (
            <Button
              data-testid="codex-unresolved-link-open"
              onClick={() => {
                navigate(`/codex/${spaceId}/pages/${existing.id}`);
                onClose();
              }}
            >
              <FileText className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
              Open “{existing.title}”
            </Button>
          )}

          <Button
            variant={existing ? 'outline' : 'default'}
            data-testid="codex-unresolved-link-create"
            disabled={createPage.isPending}
            onClick={() => void handleCreate()}
          >
            <Plus className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
            {createPage.isPending ? 'Creating…' : 'Create the page'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
