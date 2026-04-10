import { useState, useMemo } from 'react';
import { useParams } from 'react-router-dom';
import { FileText, Edit, Plus, AlertCircle } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { cn } from '../../lib/utils';
import { useWikiPages, useCreateWikiPage } from '../../lib/api';

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** Two-panel wiki page with sidebar list and markdown content. */
export function WikiPage() {
  const { spaceId = '', pageId } = useParams<{ spaceId: string; pageId: string }>();
  const { data: pages, isLoading, error } = useWikiPages(spaceId);
  const createMutation = useCreateWikiPage(spaceId);

  const [activeId, setActiveId] = useState<string | null>(pageId ?? null);

  // New Page modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');

  const activePage = useMemo(() => {
    if (!pages || !activeId) return null;
    return pages.find((p) => p.id === activeId) ?? null;
  }, [pages, activeId]);

  // If we have pages but no active selection, select the first one
  useMemo(() => {
    if (pages && pages.length > 0 && !activeId) {
      setActiveId(pages[0].id);
    }
  }, [pages, activeId]);

  function resetForm() {
    setFormTitle('');
  }

  async function handleCreatePage() {
    const title = formTitle.trim();
    if (!title) return;

    const body = {
      title,
      content: '',
    };
    console.log('[WikiPage] Creating page:', JSON.stringify(body));

    try {
      const created = await createMutation.mutateAsync(body);
      setActiveId(created.id);
      setDialogOpen(false);
      resetForm();
    } catch (err) {
      console.error('[WikiPage] Create page error:', err);
    }
  }

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading wiki...
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          Failed to load wiki: {error.message}
        </p>
      </div>
    );
  }

  return (
    <div className="flex gap-6">
      {/* Sidebar */}
      <aside className="hidden w-60 shrink-0 lg:block">
        <div className="sticky top-4 space-y-1 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
          <h2 className="mb-2 px-2 text-[var(--text-xs)] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
            Pages
          </h2>
          {pages && pages.map((page) => (
            <button
              key={page.id}
              type="button"
              onClick={() => setActiveId(page.id)}
              className={cn(
                'flex w-full items-center gap-1.5 rounded-[var(--radius-md)] px-2 py-1.5 text-left text-[var(--text-sm)] transition-colors',
                activeId === page.id
                  ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)] font-medium'
                  : 'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
              )}
            >
              <FileText className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
              <span className="truncate">{page.title}</span>
            </button>
          ))}

          {pages && pages.length === 0 && (
            <p className="px-2 py-4 text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No pages yet.
            </p>
          )}

          {/* New Page button */}
          <button
            type="button"
            onClick={() => setDialogOpen(true)}
            className={cn(
              'flex w-full items-center gap-1.5 rounded-[var(--radius-md)] px-2 py-1.5 mt-2 text-left text-[var(--text-sm)]',
              'text-[var(--color-text-muted)] hover:text-[var(--color-primary)] hover:bg-[var(--color-surface-hover)] transition-colors',
              'border border-dashed border-[var(--color-border)]',
            )}
          >
            <Plus className="h-3.5 w-3.5 shrink-0" />
            <span>New Page</span>
          </button>
        </div>
      </aside>

      {/* Content */}
      <main className="min-w-0 flex-1">
        {activePage ? (
          <div className="space-y-4">
            <div className="flex items-start justify-between">
              <div>
                <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
                  {activePage.title}
                </h1>
                <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                  Last edited {(activePage.updated_at ?? '').slice(0, 10)}
                </p>
              </div>
              <Button variant="secondary" size="sm">
                <Edit className="mr-2 h-4 w-4" />
                Edit
              </Button>
            </div>

            {activePage.body ? (
              <article
                className={cn(
                  'prose max-w-none',
                  'prose-headings:text-[var(--color-text)] prose-p:text-[var(--color-text)]',
                  'prose-a:text-[var(--color-primary)] prose-strong:text-[var(--color-text)]',
                  'prose-code:text-[var(--color-primary)] prose-code:bg-[var(--color-surface-hover)] prose-code:rounded prose-code:px-1',
                  'prose-pre:bg-[var(--color-surface)] prose-pre:border prose-pre:border-[var(--color-border)]',
                  'prose-th:text-[var(--color-text-muted)] prose-td:text-[var(--color-text)]',
                  'prose-li:text-[var(--color-text)]',
                  'dark:prose-invert',
                )}
              >
                <ReactMarkdown>{activePage.body}</ReactMarkdown>
              </article>
            ) : (
              <div className="flex min-h-[300px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] bg-[var(--color-surface)]">
                <p className="text-[var(--text-lg)] text-[var(--color-text-muted)]">
                  This page is empty. Click Edit to start writing.
                </p>
              </div>
            )}
          </div>
        ) : (
          <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
            {pages && pages.length > 0 ? 'Select a page from the sidebar.' : 'No pages yet. Create one to get started.'}
          </div>
        )}
      </main>

      {/* New Page dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Page</DialogTitle>
            <DialogDescription>
              Create a new wiki page.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <label htmlFor="page-title" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Title
              </label>
              <Input
                id="page-title"
                placeholder="e.g. Getting Started Guide"
                value={formTitle}
                onChange={(e) => setFormTitle(e.target.value)}
                autoFocus
              />
            </div>

            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{createMutation.error.message}</p>
            )}
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreatePage} disabled={createMutation.isPending || !formTitle.trim()}>
              {createMutation.isPending ? 'Creating...' : 'Create Page'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
