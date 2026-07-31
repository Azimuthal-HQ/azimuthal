import { Children, isValidElement, type ReactNode } from 'react';
import { Route } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { App } from '../../../App';
import { RequireAuth } from '../../../components/auth/RequireAuth';
import { AppShell } from '../../../shell/AppShell';
import { RequirePortalSession } from '../../../components/portal/RequirePortalSession';
import { PortalLayout } from '../PortalLayout';
import { PortalNotFoundPage } from '../PortalNotFoundPage';

/**
 * A drift guard on the router itself.
 *
 * `App.tsx` is imported by nothing but `main.tsx`, no frontend route test
 * exists, and a route's WRAPPER is invisible in review — moving one line up or
 * down inside `<Routes>` changes which guard it inherits and changes nothing
 * that looks like behaviour. For the portal that is not a cosmetic difference:
 *
 *  - a portal route declared inside `<RequireAuth><AppShell/></RequireAuth>`
 *    redirects an external requester with no account to `/login` and, on the
 *    way, renders the space switcher and module tabs for containers they must
 *    never learn exist;
 *  - a portal subtree without its own `path="*"` hands every unknown
 *    `/portal/...` URL — a truncated magic link, a stale bookmark — to the
 *    SHELL's catch-all, which lives inside the shell route and therefore does
 *    exactly the same thing.
 *
 * Both are the zero-context guarantee broken by the router rather than by a
 * component, so no page-level test can see them. This one reads the declared
 * tree directly. `App` uses no hooks, so it can be called as a plain function
 * and inspected without rendering anything.
 */

interface RouteRow {
  /** The full path this route matches, parents joined in. */
  path: string;
  /**
   * True for a pathless LAYOUT route — `<Route element={<Guard/>}>` with no
   * path of its own. It contributes no segment, so it shares its parent's
   * computed path and must not be mistaken for a route declared at that path.
   * Conflating the two is what made "is the index route guarded?" answer yes:
   * `RequirePortalSession` is itself pathless and sits at the same path.
   */
  isLayout: boolean;
  /** Is `RequireAuth` (the internal auth wall) an ancestor or self? */
  insideAuthWall: boolean;
  /** Is `AppShell` (the internal chrome) an ancestor or self? */
  insideShell: boolean;
  /** Is `RequirePortalSession` an ancestor or self? */
  insidePortalGuard: boolean;
}

function joinPath(parent: string, child: string | undefined): string {
  if (child === undefined) return parent; // a layout route contributes no segment
  if (child.startsWith('/')) return child;
  return `${parent.replace(/\/$/, '')}/${child}`;
}

/**
 * Is `type` used at or below this element?
 *
 * It descends BOTH `children` and `element`, because a Route's component hangs
 * off `element` — a walker that only followed `children` would report that the
 * app renders no pages at all.
 */
function uses(node: ReactNode, type: unknown): boolean {
  return Children.toArray(node).some((child) => {
    if (!isValidElement(child)) return false;
    if (child.type === type) return true;
    const props = child.props as { children?: ReactNode; element?: ReactNode };
    return uses(props.children, type) || uses(props.element, type);
  });
}

function walk(
  node: ReactNode,
  parentPath: string,
  inherited: Omit<RouteRow, 'path' | 'isLayout'>,
  out: RouteRow[],
): void {
  Children.toArray(node).forEach((child) => {
    if (!isValidElement(child)) return;

    if (child.type !== Route) {
      // A fragment or the <Routes> wrapper: descend without consuming a segment.
      walk((child.props as { children?: ReactNode }).children, parentPath, inherited, out);
      return;
    }

    const props = child.props as {
      path?: string;
      index?: boolean;
      element?: ReactNode;
      children?: ReactNode;
    };
    const path = props.index ? parentPath : joinPath(parentPath, props.path);
    const here: Omit<RouteRow, 'path' | 'isLayout'> = {
      insideAuthWall: inherited.insideAuthWall || uses(props.element, RequireAuth),
      insideShell: inherited.insideShell || uses(props.element, AppShell),
      insidePortalGuard:
        inherited.insidePortalGuard || uses(props.element, RequirePortalSession),
    };
    out.push({ path, isLayout: props.path === undefined && !props.index, ...here });
    if (props.children) walk(props.children, path, here, out);
  });
}

const routes: RouteRow[] = [];
walk(App(), '', { insideAuthWall: false, insideShell: false, insidePortalGuard: false }, routes);

const portalRoutes = routes.filter((r) => r.path.startsWith('/portal'));

describe('the router itself', () => {
  it('the walker actually sees the tree, and the auth wall it is asserting about', () => {
    // Anti-vacuity. If the walk silently returned nothing, or if `insideShell`
    // were false for everything, every assertion below would pass while proving
    // nothing at all. These are routes that MUST be behind the wall.
    expect(routes.length).toBeGreaterThan(20);
    const views = routes.find((r) => r.path === '/views');
    expect(views).toBeDefined();
    expect(views?.insideAuthWall).toBe(true);
    expect(views?.insideShell).toBe(true);

    const login = routes.find((r) => r.path === '/login');
    expect(login?.insideAuthWall).toBe(false);
  });

  it('declares the portal subtree OUTSIDE RequireAuth and OUTSIDE AppShell', () => {
    expect(portalRoutes.length).toBeGreaterThan(0);
    expect(
      portalRoutes.filter((r) => r.insideAuthWall).map((r) => r.path),
      'a portal route behind RequireAuth sends an external requester to /login',
    ).toEqual([]);
    expect(
      portalRoutes.filter((r) => r.insideShell).map((r) => r.path),
      'a portal route inside AppShell renders internal chrome to an external requester',
    ).toEqual([]);
  });

  it('declares its OWN catch-all, so no /portal URL reaches the shell 404', () => {
    const catchAll = routes.find((r) => r.path === '/portal/:portalKey/*');
    expect(catchAll, 'the portal subtree must declare path="*"').toBeDefined();
    expect(catchAll?.insideAuthWall).toBe(false);
    expect(catchAll?.insideShell).toBe(false);
    // And it renders the portal's own not-found page, not the shell's.
    expect(uses(App(), PortalNotFoundPage)).toBe(true);
  });

  it('declares the magic-link route at the exact path the backend emails', () => {
    // internal/core/portal/service.go builds
    // {APP_BASE_URL}/portal/{portalKey}/signin/{rawToken}. No server route
    // matches it — the SPA handler serves index.html — so this declaration is
    // the only thing that makes an emailed link resolve. Links already in
    // customers' inboxes cannot be reissued if this shape changes.
    const redeem = routes.find((r) => r.path === '/portal/:portalKey/signin/:linkToken');
    expect(redeem, 'the emailed magic-link path must exist verbatim').toBeDefined();
    expect(redeem?.insidePortalGuard, 'redeeming is how a session is obtained').toBe(false);
  });

  it('puts every request route behind RequirePortalSession, and the sign-in routes in front of it', () => {
    const guarded = [
      '/portal/:portalKey/requests',
      '/portal/:portalKey/requests/new',
      '/portal/:portalKey/requests/:reference',
    ];
    for (const path of guarded) {
      const row = routes.find((r) => r.path === path);
      expect(row, `${path} must be declared`).toBeDefined();
      expect(row?.insidePortalGuard, `${path} must sit behind RequirePortalSession`).toBe(true);
    }

    // The sign-in index and the redeem route must NOT be guarded: a requester
    // reaching them has no session yet by definition. Pathless layout routes
    // are excluded — RequirePortalSession is itself one, and it shares the
    // subtree's path.
    for (const path of ['/portal/:portalKey', '/portal/:portalKey/signin/:linkToken']) {
      const rows = routes.filter((r) => r.path === path && !r.isLayout);
      expect(rows.length, `${path} must be declared`).toBeGreaterThan(0);
      for (const row of rows) {
        expect(row.insidePortalGuard, `${path} must not require a session`).toBe(false);
      }
    }
  });

  it('frames the subtree with PortalLayout rather than a shell layout', () => {
    const parent = routes.find((r) => r.path === '/portal/:portalKey');
    expect(parent).toBeDefined();
    expect(uses(App(), PortalLayout)).toBe(true);
  });
});
