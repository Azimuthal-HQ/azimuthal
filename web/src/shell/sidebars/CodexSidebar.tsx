import { useMemo } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Clock, FileText, PenLine, Plus, Search, Star } from 'lucide-react';
import { cn } from '../../lib/utils';
import { useWikiPages, type Space, type WikiPage } from '../../lib/api';
import { spacePath } from '../modules';
import { SidebarChrome, SidebarNavItem, useSidebarIsCollapsed } from './SidebarChrome';
import { SpacePicker } from '../SpacePicker';
import { useSidebarCollapsed } from '../hooks/useSidebarCollapsed';

interface TreeNode {
  page: WikiPage;
  depth: number;
}

/** flattenTree orders pages parent-first with depth, tolerating orphaned parents. */
function flattenTree(pages: WikiPage[]): TreeNode[] {
  const byParent = new Map<string | null, WikiPage[]>();
  const ids = new Set(pages.map((p) => p.id));
  for (const page of pages) {
    const parent = page.parent_id !== null && ids.has(page.parent_id) ? page.parent_id : null;
    const bucket = byParent.get(parent);
    if (bucket) bucket.push(page);
    else byParent.set(parent, [page]);
  }
  const out: TreeNode[] = [];
  const walk = (parent: string | null, depth: number) => {
    for (const page of byParent.get(parent) ?? []) {
      out.push({ page, depth });
      walk(page.id, depth + 1);
    }
  };
  walk(null, 0);
  return out;
}

/**
 * CodexSidebar is the wiki module's left panel (ADR-0005 point 6): a fixed
 * header region — space picker, search, Recent, Starred, Drafts — above the
 * page tree in its own scroll container, so a large wiki can never push the
 * fixed items out of reach.
 */
export function CodexSidebar({ space, spaceId }: { space: Space | undefined; spaceId: string }) {
  const [collapsed] = useSidebarCollapsed();
  return (
    <SidebarChrome
      testId="space-sidebar"
      module="codex"
      header={<SpacePicker module="codex" currentSpace={space} collapsed={collapsed} />}
      settingsTo={spacePath('codex', spaceId, 'settings')}
      scrollableNav={false}
    >
      {/* Fixed region: never scrolls away */}
      <div className="shrink-0">
        <SearchThisWikiRow spaceId={spaceId} />
        <nav className="mt-[var(--space-2)] flex flex-col gap-[2px]">
          <SidebarNavItem to={spacePath('codex', spaceId, 'recent')} icon={Clock} label="Recent" />
          <SidebarNavItem to={spacePath('codex', spaceId, 'starred')} icon={Star} label="Starred" />
          <SidebarNavItem to={spacePath('codex', spaceId, 'drafts')} icon={PenLine} label="Drafts" />
        </nav>
      </div>

      <PageTree spaceId={spaceId} />
    </SidebarChrome>
  );
}

function SearchThisWikiRow({ spaceId }: { spaceId: string }) {
  const collapsed = useSidebarIsCollapsed();
  const navigate = useNavigate();
  return (
    <button
      type="button"
      onClick={() => navigate(spacePath('codex', spaceId, 'search'))}
      title="Search this wiki"
      className={cn(
        'flex w-full items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-2)]',
        'h-8 border border-[var(--color-border)] bg-[var(--color-bg)]',
        'text-[var(--text-xs)] text-[var(--color-text-muted)]',
        'hover:border-[var(--color-primary)] transition-colors duration-150',
        collapsed && 'justify-center border-transparent bg-transparent',
      )}
    >
      <Search className="h-3.5 w-3.5 shrink-0" />
      {!collapsed && 'Search this wiki'}
    </button>
  );
}

function PageTree({ spaceId }: { spaceId: string }) {
  const collapsed = useSidebarIsCollapsed();
  const { pageId } = useParams<{ pageId: string }>();
  const pagesQuery = useWikiPages(spaceId);
  const nodes = useMemo(() => flattenTree(pagesQuery.data ?? []), [pagesQuery.data]);

  if (collapsed) return null;

  return (
    <>
      <div className="mt-[var(--space-3)] flex shrink-0 items-center border-t border-[var(--color-border)] px-[var(--space-3)] pb-[var(--space-1)] pt-[var(--space-3)]">
        <p className="text-[10px] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
          Pages
        </p>
        <Link
          to={spacePath('codex', spaceId)}
          aria-label="All pages"
          className="ml-auto text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
        >
          <Plus className="h-3.5 w-3.5" />
        </Link>
      </div>
      <div data-testid="codex-page-tree" className="min-h-0 flex-1 overflow-y-auto pr-[var(--space-1)]">
        {nodes.length === 0 ? (
          <p className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
            No pages yet.
          </p>
        ) : (
          nodes.map(({ page, depth }) => (
            <Link
              key={page.id}
              to={spacePath('codex', spaceId, `pages/${page.id}`)}
              data-sidebar-tree-depth={depth}
              title={page.title}
              style={{ paddingLeft: `calc(var(--space-3) + ${depth} * var(--space-4))` }}
              className={cn(
                'flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)] py-[6px] pr-[var(--space-2)]',
                'text-[var(--text-sm)] transition-colors duration-150',
                page.id === pageId
                  ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                  : 'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
              )}
            >
              <FileText className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate">{page.title}</span>
            </Link>
          ))
        )}
      </div>
    </>
  );
}
