import { describe, expect, it } from 'vitest';
import {
  portalNewRequestHref,
  portalRedeemHref,
  portalRequestHref,
  portalRequestsHref,
  portalSignInHref,
} from '../portalLinks';

// The portal's href rule, made mechanical.
//
// An external requester must learn nothing about the container their request
// lives in, and a link is the easiest place to leak one: a single
// `/beacon/${spaceId}/tickets/${id}` names the module, the space and the
// internal ticket at once. These builders are the only place a portal URL is
// composed, so testing them tests the rule rather than one call site.

const KEY = 'pk_7f3a9c2e1b';
const REFERENCE = '6f1d8a20-4c11-4c9b-9f2e-7b3d5e0a1c44';

// Every builder, with the arguments a real caller passes. Kept as a table so
// that a builder added later without a test shows up as a compile-time gap
// here rather than as an untested href in production.
const BUILDERS: Array<[string, string]> = [
  ['portalSignInHref', portalSignInHref(KEY)],
  ['portalRedeemHref', portalRedeemHref(KEY, 'raw-link-token')],
  ['portalRequestsHref', portalRequestsHref(KEY)],
  ['portalNewRequestHref', portalNewRequestHref(KEY)],
  ['portalRequestHref', portalRequestHref(KEY, REFERENCE)],
];

describe('portalLinks', () => {
  it('composes the exact routes the app declares', () => {
    expect(portalSignInHref(KEY)).toBe('/portal/pk_7f3a9c2e1b');
    expect(portalRequestsHref(KEY)).toBe('/portal/pk_7f3a9c2e1b/requests');
    expect(portalNewRequestHref(KEY)).toBe('/portal/pk_7f3a9c2e1b/requests/new');
    expect(portalRequestHref(KEY, REFERENCE)).toBe(
      `/portal/pk_7f3a9c2e1b/requests/${REFERENCE}`,
    );
  });

  it('builds the magic-link path the BACKEND emails, segment for segment', () => {
    // internal/core/portal/service.go composes
    // {APP_BASE_URL}/portal/{portalKey}/signin/{rawToken} — a PATH, not a
    // ?token= query — and no server route matches it; the SPA handler serves
    // index.html. This shape is a contract with links already sitting in
    // customers' inboxes, so it is pinned literally.
    expect(portalRedeemHref(KEY, 'raw-link-token')).toBe(
      '/portal/pk_7f3a9c2e1b/signin/raw-link-token',
    );
  });

  it('every builder stays inside /portal/ and never reaches into the product', () => {
    for (const [name, href] of BUILDERS) {
      expect(href, name).toMatch(/^\/portal\//);
      expect(href, name).not.toContain('/beacon/');
      expect(href, name).not.toContain('/codex/');
      expect(href, name).not.toContain('/vector/');
      expect(href, name).not.toContain('/spaces/');
      expect(href, name).not.toContain('/admin/');
      expect(href, name).not.toContain('/login');
    }
  });

  it('never emits the literal "undefined" for a missing field', () => {
    // The classic template-literal-over-a-missing-field bug: `${key}` on an
    // absent value produces a URL that looks real and 404s. useParams hands
    // back `string | undefined`, so this is one destructured default away at
    // every call site.
    const missing = undefined as unknown as string;
    const hrefs = [
      portalSignInHref(missing),
      portalRedeemHref(missing, 'tok'),
      portalRedeemHref(KEY, missing),
      portalRequestsHref(missing),
      portalNewRequestHref(missing),
      portalRequestHref(missing, REFERENCE),
      portalRequestHref(KEY, missing),
      // The already-stringified form, which is what actually arrives when the
      // value passed through one template before reaching the builder.
      portalRequestHref(KEY, 'undefined'),
      portalSignInHref(''),
      portalRequestHref(KEY, '   '),
    ];
    for (const href of hrefs) {
      expect(href).not.toContain('undefined');
      expect(href).not.toContain('null');
      expect(href).toMatch(/^\/portal/);
    }
  });

  it('degrades a missing reference to the list, not to a dead URL', () => {
    expect(portalRequestHref(KEY, undefined as unknown as string)).toBe(
      '/portal/pk_7f3a9c2e1b/requests',
    );
  });

  it('encodes a segment rather than letting it break out of the path', () => {
    expect(portalRequestHref(KEY, '../../beacon/space-1')).not.toContain('/beacon/');
    expect(portalSignInHref('a/b')).toBe('/portal/a%2Fb');
  });
});
