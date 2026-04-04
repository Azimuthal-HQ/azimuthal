import { useState, useMemo } from 'react';
import { ChevronRight, ChevronDown, FileText, Edit, Plus } from 'lucide-react';
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
import { useToast } from '../../components/ui/toast';
import { cn } from '../../lib/utils';
import { createWikiPage } from '../../lib/api';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface WikiNode {
  id: string;
  title: string;
  children?: WikiNode[];
}

interface WikiPageData {
  id: string;
  title: string;
  author: string;
  lastEdited: string;
  content: string;
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const INITIAL_PAGE_TREE: WikiNode[] = [
  {
    id: 'getting-started',
    title: 'Getting Started',
    children: [
      { id: 'installation', title: 'Installation Guide' },
      { id: 'first-steps', title: 'First Steps' },
    ],
  },
  {
    id: 'architecture',
    title: 'Architecture',
    children: [
      { id: 'system-overview', title: 'System Overview' },
      { id: 'database-schema', title: 'Database Schema' },
    ],
  },
  { id: 'api-reference', title: 'API Reference' },
  { id: 'contributing', title: 'Contributing' },
];

const INITIAL_PAGES: Record<string, WikiPageData> = {
  'getting-started': {
    id: 'getting-started',
    title: 'Getting Started',
    author: 'Alice Chen',
    lastEdited: '2026-03-25',
    content: `# Getting Started

Welcome to the **Azimuthal** documentation. This guide will walk you through the basics of setting up and using the platform.

## Prerequisites

- Go 1.22 or later
- PostgreSQL 15+
- Node.js 20+ (for the frontend build)
- Docker (optional, for local services)

## Quick Start

Clone the repository and install dependencies:

\`\`\`bash
git clone https://github.com/Azimuthal-HQ/azimuthal.git
cd azimuthal
make setup
\`\`\`

Then start the development server:

\`\`\`bash
make dev
\`\`\`

The application will be available at [http://localhost:8080](http://localhost:8080).

## Next Steps

- Read the [Installation Guide](/wiki/installation) for a detailed setup walkthrough.
- Check out [First Steps](/wiki/first-steps) to create your first space.
`,
  },
  installation: {
    id: 'installation',
    title: 'Installation Guide',
    author: 'Bob Martinez',
    lastEdited: '2026-03-22',
    content: `# Installation Guide

This page covers all the ways to install and run Azimuthal.

## Docker Compose (Recommended)

The fastest way to get running locally:

\`\`\`bash
docker compose -f build/docker-compose.dev.yml up -d
make migrate
make dev
\`\`\`

## Manual Installation

1. Install Go 1.22+
2. Install PostgreSQL and create a database
3. Set environment variables (see \`.env.example\`)
4. Run migrations: \`make migrate\`
5. Start the server: \`go run ./cmd/server\`

## Configuration

All configuration can be set via environment variables or a \`config.yaml\` file. See the [System Overview](/wiki/system-overview) for details.
`,
  },
  'first-steps': {
    id: 'first-steps',
    title: 'First Steps',
    author: 'Charlie Osei',
    lastEdited: '2026-03-20',
    content: `# First Steps

After installation, here is what to do next.

## Create an Organization

Navigate to Settings and create your first organization. This is the top-level container for all your spaces.

## Create a Space

Spaces are where work happens. You can create three types:

- **Service Desk** -- for tracking support tickets
- **Wiki** -- for documentation and knowledge bases
- **Project** -- for agile project management

## Invite Team Members

Go to Organization Settings to invite your team. You can assign roles like Admin, Member, or Viewer.
`,
  },
  architecture: {
    id: 'architecture',
    title: 'Architecture',
    author: 'Dana Kim',
    lastEdited: '2026-03-18',
    content: `# Architecture

Azimuthal is a monolithic Go application with a React frontend, compiled into a single binary.

## Key Components

| Component | Description |
|-----------|-------------|
| \`cmd/server\` | Single binary entrypoint |
| \`internal/core\` | All domain logic |
| \`internal/db\` | Database layer (sqlc + goose) |
| \`web/\` | React frontend (embedded) |

## Design Principles

- **Single binary** -- everything ships together
- **Interface-driven** -- all major subsystems use Go interfaces
- **Soft deletes** -- user data is never permanently removed
- **Audit trail** -- all mutations are logged
`,
  },
  'system-overview': {
    id: 'system-overview',
    title: 'System Overview',
    author: 'Dana Kim',
    lastEdited: '2026-03-18',
    content: `# System Overview

The system consists of the following major layers:

## HTTP Layer

All requests enter through a chi router configured in \`internal/core/api\`. Middleware handles authentication, RBAC checks, request logging, and rate limiting.

## Domain Layer

Business logic lives in domain packages under \`internal/core/\`. Each domain (tickets, wiki, projects) is self-contained with its own types and service layer.

## Data Layer

Database access uses sqlc-generated query functions with goose migrations. All writes go through transactions.
`,
  },
  'database-schema': {
    id: 'database-schema',
    title: 'Database Schema',
    author: 'Alice Chen',
    lastEdited: '2026-03-15',
    content: `# Database Schema

PostgreSQL is the only supported database. Migrations are managed with goose.

## Core Tables

- \`users\` -- user accounts
- \`organizations\` -- top-level tenants
- \`spaces\` -- workspaces within organizations
- \`tickets\` -- service desk items
- \`wiki_pages\` -- documentation pages
- \`project_items\` -- backlog items

All tables include \`created_at\`, \`updated_at\`, and \`deleted_at\` columns for soft deletes.
`,
  },
  'api-reference': {
    id: 'api-reference',
    title: 'API Reference',
    author: 'Eve Johnson',
    lastEdited: '2026-03-12',
    content: `# API Reference

The REST API is available at \`/api/v1/\`. All endpoints require authentication via Bearer token.

## Authentication

\`\`\`
POST /api/v1/auth/login
POST /api/v1/auth/refresh
\`\`\`

## Tickets

\`\`\`
GET    /api/v1/tickets
POST   /api/v1/tickets
GET    /api/v1/tickets/:id
PATCH  /api/v1/tickets/:id
\`\`\`

## Wiki

\`\`\`
GET    /api/v1/wiki/pages
POST   /api/v1/wiki/pages
GET    /api/v1/wiki/pages/:id
PUT    /api/v1/wiki/pages/:id
\`\`\`
`,
  },
  contributing: {
    id: 'contributing',
    title: 'Contributing',
    author: 'Bob Martinez',
    lastEdited: '2026-03-10',
    content: `# Contributing

We welcome contributions from everyone. Here is how to get started.

## Development Workflow

1. Fork the repository
2. Create a feature branch
3. Write tests first (TDD)
4. Implement the feature
5. Run \`make pre-push\` to verify all checks pass
6. Open a pull request

## Code Style

- Go code follows standard \`gofmt\` formatting
- All exported functions need godoc comments
- Every error must be handled -- no discarding error returns
- Wrap errors with context using \`fmt.Errorf\`

## Testing

We target 80% code coverage minimum. Run tests with:

\`\`\`bash
make test
\`\`\`
`,
  },
};

// ---------------------------------------------------------------------------
// Helper: flatten tree for parent selector
// ---------------------------------------------------------------------------

function flattenTree(nodes: WikiNode[], depth: number = 0): { id: string; title: string; depth: number }[] {
  const result: { id: string; title: string; depth: number }[] = [];
  for (const node of nodes) {
    result.push({ id: node.id, title: node.title, depth });
    if (node.children) {
      result.push(...flattenTree(node.children, depth + 1));
    }
  }
  return result;
}

// ---------------------------------------------------------------------------
// Tree node component
// ---------------------------------------------------------------------------

interface TreeNodeProps {
  node: WikiNode;
  depth: number;
  activeId: string;
  expanded: Set<string>;
  onSelect: (id: string) => void;
  onToggle: (id: string) => void;
}

function TreeNode({ node, depth, activeId, expanded, onSelect, onToggle }: TreeNodeProps) {
  const hasChildren = node.children && node.children.length > 0;
  const isExpanded = expanded.has(node.id);
  const isActive = activeId === node.id;

  return (
    <div>
      <button
        type="button"
        onClick={() => {
          onSelect(node.id);
          if (hasChildren) onToggle(node.id);
        }}
        className={cn(
          'flex w-full items-center gap-1.5 rounded-[var(--radius-md)] px-2 py-1.5 text-left text-[var(--text-sm)] transition-colors',
          isActive
            ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)] font-medium'
            : 'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
        )}
        style={{ paddingLeft: `${depth * 16 + 8}px` }}
      >
        {hasChildren ? (
          isExpanded ? (
            <ChevronDown className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
          )
        ) : (
          <FileText className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
        )}
        <span className="truncate">{node.title}</span>
      </button>

      {hasChildren && isExpanded && (
        <div>
          {node.children!.map((child) => (
            <TreeNode
              key={child.id}
              node={child}
              depth={depth + 1}
              activeId={activeId}
              expanded={expanded}
              onSelect={onSelect}
              onToggle={onToggle}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** Two-panel wiki page with sidebar tree and markdown content. */
export function WikiPage() {
  const { toast } = useToast();
  const [pageTree, setPageTree] = useState(INITIAL_PAGE_TREE);
  const [pages, setPages] = useState(INITIAL_PAGES);
  const [activeId, setActiveId] = useState('getting-started');
  const [expanded, setExpanded] = useState<Set<string>>(
    () => new Set(['getting-started', 'architecture']),
  );

  // New Page modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formParent, setFormParent] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const activePage = useMemo(() => pages[activeId], [pages, activeId]);
  const flatPages = useMemo(() => flattenTree(pageTree), [pageTree]);

  function resetForm() {
    setFormTitle('');
    setFormParent('');
    setSubmitting(false);
  }

  function handleToggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  // Check if active page is in "new empty page" mode
  const isNewEmptyPage = activePage && activePage.content === '';

  async function handleCreatePage() {
    const title = formTitle.trim();
    if (!title) return;

    setSubmitting(true);

    const slug = title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

    try {
      const apiCall = createWikiPage('default', {
        title,
        body: '',
        parent_id: formParent || undefined,
      });
      const timeout = new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error('timeout')), 3000),
      );
      await Promise.race([apiCall, timeout]);

      toast({ title: 'Page created', variant: 'success' });
    } catch {
      // Mock mode fallback — add locally
      const newId = slug || `page-${Date.now()}`;

      // Add to pages data
      const newPageData: WikiPageData = {
        id: newId,
        title,
        author: 'You',
        lastEdited: new Date().toISOString().slice(0, 10),
        content: '',
      };
      setPages((prev) => ({ ...prev, [newId]: newPageData }));

      // Add to tree
      const newNode: WikiNode = { id: newId, title };
      if (formParent) {
        // Add as child of parent
        setPageTree((prev) => addChildToTree(prev, formParent, newNode));
        setExpanded((prev) => new Set([...prev, formParent]));
      } else {
        // Add at top level
        setPageTree((prev) => [...prev, newNode]);
      }

      // Navigate to the new page
      setActiveId(newId);
      toast({ title: 'Mock mode — backend not connected', variant: 'warning' });
    } finally {
      setSubmitting(false);
      setDialogOpen(false);
      resetForm();
    }
  }

  return (
    <div className="flex gap-6">
      {/* Sidebar */}
      <aside className="hidden w-60 shrink-0 lg:block">
        <div className="sticky top-4 space-y-1 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
          <h2 className="mb-2 px-2 text-[var(--text-xs)] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
            Pages
          </h2>
          {pageTree.map((node) => (
            <TreeNode
              key={node.id}
              node={node}
              depth={0}
              activeId={activeId}
              expanded={expanded}
              onSelect={setActiveId}
              onToggle={handleToggle}
            />
          ))}

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
        {isNewEmptyPage ? (
          /* New empty page — edit mode */
          <div className="space-y-4">
            <div className="flex items-start justify-between">
              <div>
                <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
                  {activePage.title}
                </h1>
                <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                  By {activePage.author} &middot; Created {activePage.lastEdited}
                </p>
              </div>
              <Button variant="default" size="sm">
                <Edit className="mr-2 h-4 w-4" />
                Edit
              </Button>
            </div>
            <div className="flex min-h-[300px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] bg-[var(--color-surface)]">
              <p className="text-[var(--text-lg)] text-[var(--color-text-muted)]">
                Start writing...
              </p>
            </div>
          </div>
        ) : activePage ? (
          <div className="space-y-4">
            {/* Page header */}
            <div className="flex items-start justify-between">
              <div>
                <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
                  {activePage.title}
                </h1>
                <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                  By {activePage.author} &middot; Last edited {activePage.lastEdited}
                </p>
              </div>
              <Button variant="secondary" size="sm">
                <Edit className="mr-2 h-4 w-4" />
                Edit
              </Button>
            </div>

            {/* Markdown content */}
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
              <ReactMarkdown>{activePage.content}</ReactMarkdown>
            </article>
          </div>
        ) : (
          <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
            Select a page from the sidebar.
          </div>
        )}
      </main>

      {/* New Page dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Page</DialogTitle>
            <DialogDescription>
              Create a new wiki page. You can nest it under an existing page.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-2">
            {/* Title */}
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

            {/* Parent Page */}
            <div className="space-y-2">
              <label htmlFor="page-parent" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Parent Page <span className="text-[var(--color-text-muted)] font-normal">(optional)</span>
              </label>
              <select
                id="page-parent"
                value={formParent}
                onChange={(e) => setFormParent(e.target.value)}
                className={cn(
                  'flex h-9 w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
                  'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
                )}
              >
                <option value="">No parent (top level)</option>
                {flatPages.map((p) => (
                  <option key={p.id} value={p.id}>
                    {'\u00A0\u00A0'.repeat(p.depth)}{p.title}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreatePage} disabled={submitting || !formTitle.trim()}>
              {submitting ? 'Creating...' : 'Create Page'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Tree mutation helper
// ---------------------------------------------------------------------------

/** Recursively adds a child node under the specified parent ID. */
function addChildToTree(tree: WikiNode[], parentId: string, child: WikiNode): WikiNode[] {
  return tree.map((node) => {
    if (node.id === parentId) {
      return { ...node, children: [...(node.children ?? []), child] };
    }
    if (node.children) {
      return { ...node, children: addChildToTree(node.children, parentId, child) };
    }
    return node;
  });
}
