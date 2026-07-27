import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SharedEntityPage } from '../SharedEntityPage';

// The shared page is the only consumer of the attachment download route, and
// it decides how to present an attachment from `content_type` — the type the
// UPLOADER declared. The server decides independently, by sniffing the object's
// bytes (internal/core/attachments/serve.go). The two can disagree, so the
// page must never make its download list the complement of its preview filter:
// an attachment matching neither would have no link and a broken <img>, i.e.
// no way to reach the file at all.
//
// Fixtures live inside the factory because vi.mock hoists above file scope.
vi.mock('react-router-dom', () => ({
  useParams: () => ({ entityType: 'page', entityId: 'page-1' }),
}));

vi.mock('../../../lib/api', () => ({
  useMe: vi.fn(() => ({ data: { org_id: 'org-1' } })),
  useSharedEntity: vi.fn(() => ({
    data: { title: 'Shared page', body: 'body', share: { audience: 'org' } },
    isLoading: false,
    isError: false,
  })),
  useSharedAttachments: vi.fn(() => ({
    data: [
      { id: 'a-png', filename: 'figure.png', content_type: 'image/png' },
      // Declared image/*, but NOT a type the server streams inline — it comes
      // back as application/octet-stream + Content-Disposition: attachment.
      { id: 'a-svg', filename: 'diagram.svg', content_type: 'image/svg+xml' },
      { id: 'a-pdf', filename: 'report.pdf', content_type: 'application/pdf' },
      { id: 'a-txt', filename: 'notes.txt', content_type: 'text/plain' },
    ],
  })),
  sharedAttachmentURL: (_org: string, _t: string, _e: string, attID: string) =>
    `/api/v1/attachments/${attID}`,
  friendlyErrorMessage: (e: unknown) => String(e),
}));

vi.mock('../../../components/ShareBadge', () => ({
  ShareBadge: () => <span data-testid="share-badge" />,
}));

describe('SharedEntityPage attachments', () => {
  // The regression. Before the download list was made unconditional, an SVG
  // sat in the image bucket (its declared type starts with "image/") and was
  // excluded from the link list, so the only affordance was an <img> the
  // server now refuses to serve inline.
  it('gives every attachment a reachable link, including declared-image types the server will not inline', () => {
    render(<SharedEntityPage />);

    for (const [filename, id] of [
      ['figure.png', 'a-png'],
      ['diagram.svg', 'a-svg'],
      ['report.pdf', 'a-pdf'],
      ['notes.txt', 'a-txt'],
    ]) {
      const link = screen.getByRole('link', { name: filename });
      expect(link).toHaveAttribute('href', `/api/v1/attachments/${id}`);
    }
  });

  // The preview filter matches the server's inline allow-list exactly, so the
  // page does not emit an <img> for bytes the server will send as a download.
  it('previews only the raster types the server streams inline', () => {
    render(<SharedEntityPage />);

    const previews = screen.getAllByRole('img');
    expect(previews).toHaveLength(1);
    expect(previews[0]).toHaveAttribute('alt', 'figure.png');

    // Specifically: no <img> for the SVG. An SVG is scriptable, which is why
    // the server refuses to stream it inline in the first place.
    expect(screen.queryByAltText('diagram.svg')).toBeNull();
    expect(screen.queryByAltText('report.pdf')).toBeNull();
  });
});
