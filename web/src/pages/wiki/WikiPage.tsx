import { useState, useMemo } from 'react';
import { ChevronRight, ChevronDown, FileText, Edit } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import { Button } from '../../components/ui/button';
import { cn } from '../../lib/utils';

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

const PAGE_TREE: WikiNode[] = [
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

const MOCK_PAGES: Record<string, WikiPageData> = {
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
  const [activeId, setActiveId] = useState('getting-started');
  const [expanded, setExpanded] = useState<Set<string>>(
    () => new Set(['getting-started', 'architecture']),
  );

  const activePage = useMemo(() => MOCK_PAGES[activeId], [activeId]);

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

  return (
    <div className="flex gap-6">
      {/* Sidebar */}
      <aside className="hidden w-60 shrink-0 lg:block">
        <div className="sticky top-4 space-y-1 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
          <h2 className="mb-2 px-2 text-[var(--text-xs)] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
            Pages
          </h2>
          {PAGE_TREE.map((node) => (
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
        </div>
      </aside>

      {/* Content */}
      <main className="min-w-0 flex-1">
        {activePage ? (
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
    </div>
  );
}
