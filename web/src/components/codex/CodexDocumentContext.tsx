/**
 * The document's own context: which space and page it belongs to, and the
 * pages it can refer to.
 *
 * Node views are rendered by ProseMirror, not by the React tree that owns the
 * editor, but `ReactNodeViewRenderer` re-parents them under the editor's React
 * root — so ordinary context reaches them. Cross-reference macros (page
 * include, children display, internal links) need to resolve a page id to a
 * title, and this is how they do it without each one fetching for itself.
 *
 * The page list is passed in rather than fetched here so a node view never
 * issues a request of its own: a document containing forty page-include macros
 * would otherwise make forty identical calls.
 */
import { createContext, useContext } from 'react';
import type { ReactNode } from 'react';

import type { WikiPage } from '../../lib/api';

export interface CodexDocumentContextValue {
  spaceId: string;
  /** The page being edited or read; empty while a page is being created. */
  pageId: string;
  /** Every page in the space, for resolving references to titles. */
  pages: WikiPage[];
}

const CodexDocumentContext = createContext<CodexDocumentContextValue>({
  spaceId: '',
  pageId: '',
  pages: [],
});

export function CodexDocumentProvider({
  value,
  children,
}: {
  value: CodexDocumentContextValue;
  children: ReactNode;
}) {
  return <CodexDocumentContext.Provider value={value}>{children}</CodexDocumentContext.Provider>;
}

export function useCodexDocumentContext(): CodexDocumentContextValue {
  return useContext(CodexDocumentContext);
}

/** The title of a page in this space, or a plain marker when it is unknown. */
export function usePageTitle(pageId: string): string | null {
  const { pages } = useCodexDocumentContext();
  if (!pageId) return null;
  return pages.find((p) => p.id === pageId)?.title ?? null;
}
