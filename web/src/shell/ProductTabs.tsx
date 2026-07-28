import { Link, useLocation } from 'react-router-dom';
import { Home } from 'lucide-react';
import { cn } from '../lib/utils';
import { MODULE_KEYS, MODULES } from './modules';

/**
 * ProductTabs is the top-bar product switcher (ADR-0005 point 1): Home,
 * Beacon, Codex, Vector. A module tab is active while the URL is inside
 * that module; Home covers the overview, home dashboards, and org-scoped
 * pages (settings, admin).
 */
export function ProductTabs() {
  const { pathname } = useLocation();

  // Saved views are org-scoped and cross-module, so they sit under Home rather
  // than becoming a fifth product tab — they are a destination, not a product.
  const isHomeActive =
    pathname === '/' ||
    pathname.startsWith('/home') ||
    pathname.startsWith('/settings') ||
    pathname.startsWith('/admin') ||
    pathname.startsWith('/search') ||
    pathname.startsWith('/views');

  const tabClass = (active: boolean) =>
    cn(
      'flex h-8 items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-3)]',
      'text-[var(--text-sm)] whitespace-nowrap transition-colors duration-150',
      active
        ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
        : 'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
    );

  return (
    <nav aria-label="Products" className="flex items-center gap-[var(--space-1)]">
      <Link to="/" data-testid="product-tab-home" className={tabClass(isHomeActive)}>
        <Home className="h-4 w-4" />
        <span className="hidden sm:inline">Home</span>
      </Link>
      {MODULE_KEYS.map((key) => {
        const def = MODULES[key];
        const active = pathname === `/${key}` || pathname.startsWith(`/${key}/`);
        return (
          <Link key={key} to={`/${key}`} data-testid={`product-tab-${key}`} className={tabClass(active)}>
            <def.icon className="h-4 w-4" />
            <span className="hidden sm:inline">{def.name}</span>
          </Link>
        );
      })}
    </nav>
  );
}
