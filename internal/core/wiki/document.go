package wiki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Errors returned by the document surface.
var (
	// ErrDraftNotFound is returned when the caller holds no draft on a page.
	ErrDraftNotFound = errors.New("no draft on this page")

	// ErrUnknownPreservedContent is returned when a document carries a
	// preservation placeholder with no original behind it. The document was
	// prepared against a different version of the page than the one being
	// published, so the placeholder cannot be resolved — and writing a
	// placeholder into storage as though it were content is not an option.
	ErrUnknownPreservedContent = errors.New("document refers to preserved content that cannot be resolved")

	// ErrBaseVersionUnavailable is returned when the version a draft was started
	// from has no recoverable document. Without it, the preserved content in the
	// draft cannot be resolved.
	ErrBaseVersionUnavailable = errors.New("the version this draft was started from is no longer available")

	// ErrImageNotOnPage is returned when an image node names an attachment that
	// does not belong to the page being published.
	ErrImageNotOnPage = errors.New("image does not belong to this page")

	// ErrImageNotAnImage is returned when an image node names an attachment whose
	// stored bytes are not a supported image.
	ErrImageNotAnImage = errors.New("attachment is not a supported image")

	// ErrMalformedDocument is returned when the caller's document is not a
	// well-formed ProseMirror document — valid JSON, but not an object, or not
	// rooted at type "doc", or carrying a node with no type, or with a
	// "content" member that is not an array.
	//
	// ONE SENTINEL RATHER THAN ONE PER SHAPE, applied at the call to
	// doc.Validate rather than inside doc itself. Three of the four refusals
	// already carry an exported sentinel and the fourth does not, so a handler
	// switch that enumerated them would have answered 400 for three shapes of
	// the same mistake and 500 for the fourth — which is the defect this fixes,
	// reintroduced one case smaller (known-issues #24). Wrapping the whole
	// validation is exhaustive by construction, including for any refusal
	// doc.Validate learns later, and it keeps the classification out of a
	// package ADR-0012 governs.
	ErrMalformedDocument = errors.New("document is malformed")
)

// SourceFormat describes where a page's editable document came from.
const (
	// SourceFormatDocument means the page already holds a stored document.
	SourceFormatDocument = "document"

	// SourceFormatMarkdown means the page has only ever held markdown, and the
	// document being opened was converted from it on the way out. Nothing is
	// written until the author publishes.
	SourceFormatMarkdown = "markdown"
)

// DocumentStore is the persistence the document surface needs. Deliberately
// narrow, and satisfied by *generated.Queries directly.
type DocumentStore interface {
	GetPageByID(ctx context.Context, id uuid.UUID) (generated.Page, error)
	GetPageRevisionDocument(ctx context.Context, arg generated.GetPageRevisionDocumentParams) (generated.GetPageRevisionDocumentRow, error)
	UpsertPageDraft(ctx context.Context, arg generated.UpsertPageDraftParams) (generated.PageDraft, error)
	GetPageDraft(ctx context.Context, arg generated.GetPageDraftParams) (generated.PageDraft, error)
	DeletePageDraft(ctx context.Context, arg generated.DeletePageDraftParams) (int64, error)
	ListPageDraftsForAuthorInSpace(ctx context.Context, arg generated.ListPageDraftsForAuthorInSpaceParams) ([]generated.ListPageDraftsForAuthorInSpaceRow, error)
}

// PublishPageTxInput carries one publish through the transactional seam.
type PublishPageTxInput struct {
	PageID   uuid.UUID
	AuthorID uuid.UUID
	Title    string
	// Content is the markdown projection, for the generated search_vector and
	// for legacy readers. Derived from Doc, never authoritative.
	Content string
	Doc     json.RawMessage
	// BaseVersion guards the update. Ignored when Overwrite is set.
	BaseVersion int32
	// Overwrite drops the version guard: the explicit arm of the conflict
	// dialogue, reachable only after the conflict has been reported and the
	// author has chosen to overwrite it in those words.
	Overwrite bool
	// TagIDs are the org tags the document's inline `#tag` tokens resolved to.
	// They are ADDED to the page's tags, never made to be its whole set: the
	// page-level list is the authority and an inline token is a shortcut, so
	// deleting the last `#foo` from a body must not silently untag the page.
	//
	// They travel inside the transaction because a published version whose tag
	// association is missing is a visible defect, while the tag ROWS they refer
	// to are created outside it — an unused tag row is indistinguishable from
	// one somebody stopped using.
	TagIDs []uuid.UUID
}

// DocumentTxStore is the transactional seam for publishing.
//
// Publish is three writes that have to be one: the page row, its history row,
// and the clearing of the draft that was just published. A crash between them
// leaves a page whose history is missing a version, or a draft that reappears
// as unpublished work the author already published. Neither is recoverable by
// retrying, so this follows shared-surfaces convention B — the atomicity is the
// contract, not a sequencing preference.
type DocumentTxStore interface {
	PublishPageTx(ctx context.Context, in PublishPageTxInput) (generated.Page, error)
}

// UploadImageInput carries one editor image upload.
type UploadImageInput struct {
	OrgID      uuid.UUID
	PageID     uuid.UUID
	Filename   string
	UploadedBy uuid.UUID
	Content    io.Reader
	// Size is the declared content length, for the ceiling check.
	Size int64
}

// PageImage is a stored image as a document refers to it. A document carries the
// attachment id and never a URL: the URL a reader needs depends on whether they
// reached the page through the space or through a share, and baking one into the
// document would make it wrong for one of them.
type PageImage struct {
	AttachmentID uuid.UUID `json:"attachment_id"`
	Filename     string    `json:"filename"`
	ContentType  string    `json:"content_type"`
	SizeBytes    int64     `json:"size_bytes"`
}

// ImageStore is the document surface's window onto attachments. Narrow on
// purpose, and in this package's own types, so the domain package does not depend
// on the attachment surface to describe what it needs from it.
type ImageStore interface {
	// PageImageContentType returns the sniffed content type of an attachment
	// belonging to the given page. Any error means the reference cannot be
	// resolved on this page, which the caller reports as ErrImageNotOnPage —
	// distinguishing the cases would leak whether an attachment id exists
	// somewhere the caller cannot see.
	PageImageContentType(ctx context.Context, pageID, attachmentID uuid.UUID) (string, error)

	// UploadPageImage stores an image on a page, sniffing its type from the bytes
	// rather than believing the client.
	UploadPageImage(ctx context.Context, in UploadImageInput) (PageImage, error)
}

// ErrImageStorageUnavailable is returned when the deployment has no object
// store, so an image can neither be stored nor verified.
var ErrImageStorageUnavailable = errors.New("image storage is unavailable")

// UnavailableImageStore stands in when the deployment has no object store.
//
// It exists so [ImageStore] can stay a required dependency. The alternative —
// letting it be nil — is the "dark harness" failure CLAUDE.md section 2
// describes: a nil collaborator makes the whole document surface answer as
// disabled, including the text editing that has nothing to do with storage, and
// it does so silently. Refusing images loudly while the editor keeps working is
// both the more useful production behaviour and the one that cannot be mistaken
// for coverage in a test.
type UnavailableImageStore struct{}

// PageImageContentType always fails: without a store there are no bytes to sniff,
// and a document's image reference cannot be confirmed.
func (UnavailableImageStore) PageImageContentType(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "", ErrImageStorageUnavailable
}

// UploadPageImage always fails.
func (UnavailableImageStore) UploadPageImage(context.Context, UploadImageInput) (PageImage, error) {
	return PageImage{}, ErrImageStorageUnavailable
}

// UploadImage stores an image on a page for the editor to reference.
func (s *DocumentService) UploadImage(ctx context.Context, in UploadImageInput) (PageImage, error) {
	// The page must exist and be live before anything is written: an upload aimed
	// at a deleted page would leave an object nobody can ever reach.
	if _, err := s.page(ctx, in.PageID); err != nil {
		return PageImage{}, err
	}
	image, err := s.images.UploadPageImage(ctx, in)
	if err != nil {
		return PageImage{}, fmt.Errorf("uploading page image: %w", err)
	}
	return image, nil
}

// TagResolver turns a document's inline tag labels into org tag ids, creating
// tags that do not exist yet.
//
// Narrow on purpose: the document surface needs exactly one thing from the tag
// model, and stating it as one method keeps this package from depending on the
// tag domain's shape.
type TagResolver interface {
	ResolveForPublish(ctx context.Context, orgID uuid.UUID, labels []string) ([]uuid.UUID, error)
}

// DocumentService owns the ProseMirror-native document surface: opening a page
// for editing, autosaving a per-user draft, and publishing.
//
// Every collaborator is a required constructor argument rather than a With*
// option. A missing option would leave the surface answering "not enabled" in
// every test that touched it, which reads as coverage; a missing argument does
// not compile.
type DocumentService struct {
	store  DocumentStore
	tx     DocumentTxStore
	images ImageStore
	tags   TagResolver
}

// NewDocumentService creates a DocumentService.
func NewDocumentService(store DocumentStore, tx DocumentTxStore, images ImageStore, tagResolver TagResolver) *DocumentService {
	return &DocumentService{store: store, tx: tx, images: images, tags: tagResolver}
}

// DraftDocument is a caller's unpublished edit of a page.
type DraftDocument struct {
	Title       string          `json:"title"`
	Doc         json.RawMessage `json:"doc"`
	BaseVersion int32           `json:"base_version"`
	UpdatedAt   time.Time       `json:"updated_at"`
	// Stale is true when the page has been published past the version this
	// draft was started from. The draft is still the author's work and is not
	// touched; publishing it is what will hit the conflict.
	Stale bool `json:"stale"`
}

// EditableDocument is what the editor opens: the published document, made safe
// for ProseMirror, plus the caller's own draft if they hold one.
type EditableDocument struct {
	PageID uuid.UUID `json:"page_id"`
	Title  string    `json:"title"`
	// Doc is the published document with every type outside the editor's schema
	// replaced by a preservation placeholder.
	Doc json.RawMessage `json:"doc"`
	// BaseVersion is the published version Doc belongs to, and what a later
	// publish must carry back.
	BaseVersion int32 `json:"base_version"`
	// SourceFormat says whether the page already held a document or whether one
	// was converted from its markdown for this response.
	SourceFormat string `json:"source_format"`
	// PreservedIDs lists the placeholder ids in Doc, in document order. The
	// editor keeps this to work out what it has lost if ProseMirror drops one.
	PreservedIDs []string       `json:"preserved_ids"`
	Draft        *DraftDocument `json:"draft"`
}

// OpenDocument returns the page's editable document for one caller.
//
// A page that has only ever held markdown is converted here and NOT written
// back: opening a page is a read. The conversion is deterministic, so publish
// can re-derive exactly this document to resolve the preservation ids it
// handed out.
func (s *DocumentService) OpenDocument(ctx context.Context, pageID, authorID uuid.UUID) (EditableDocument, error) {
	page, err := s.page(ctx, pageID)
	if err != nil {
		return EditableDocument{}, err
	}

	base, format, err := documentOfPage(page)
	if err != nil {
		return EditableDocument{}, err
	}
	shielded, err := doc.Shield(base)
	if err != nil {
		return EditableDocument{}, fmt.Errorf("preparing the document for editing: %w", err)
	}

	out := EditableDocument{
		PageID:       page.ID,
		Title:        page.Title,
		Doc:          shielded.Document,
		BaseVersion:  page.Version,
		SourceFormat: format,
		PreservedIDs: shielded.Order,
	}
	if out.PreservedIDs == nil {
		out.PreservedIDs = []string{}
	}

	draft, err := s.Draft(ctx, pageID, authorID)
	switch {
	case err == nil:
		draft.Stale = draft.BaseVersion != page.Version
		out.Draft = &draft
	case errors.Is(err, ErrDraftNotFound):
	default:
		return EditableDocument{}, err
	}
	return out, nil
}

// Draft returns the caller's own draft of a page, or ErrDraftNotFound.
//
// Every read here is keyed by author: a draft is visible to nobody but the
// person who wrote it, and that is a property of the query rather than a filter
// applied afterwards.
func (s *DocumentService) Draft(ctx context.Context, pageID, authorID uuid.UUID) (DraftDocument, error) {
	row, err := s.store.GetPageDraft(ctx, generated.GetPageDraftParams{
		PageID:   pageID,
		AuthorID: authorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DraftDocument{}, ErrDraftNotFound
	}
	if err != nil {
		return DraftDocument{}, fmt.Errorf("getting page draft: %w", err)
	}
	return DraftDocument{
		Title:       row.Title,
		Doc:         row.Doc,
		BaseVersion: row.BaseVersion,
		UpdatedAt:   row.UpdatedAt.Time,
	}, nil
}

// SaveDraftInput carries one autosave.
type SaveDraftInput struct {
	PageID      uuid.UUID
	AuthorID    uuid.UUID
	Title       string
	Doc         json.RawMessage
	BaseVersion int32
}

// SaveDraft stores the caller's draft, replacing any previous one.
//
// It checks the document's shape and nothing else. In particular it does not
// resolve the draft's preservation placeholders: autosave has to be silent and
// frequent, and a failure here would surface as an unexplained error while
// somebody is typing. Resolution — and the refusal to lose preserved content —
// belongs to publish, which is the moment anything becomes visible to a reader.
func (s *DocumentService) SaveDraft(ctx context.Context, in SaveDraftInput) (DraftDocument, error) {
	if err := doc.Validate(in.Doc); err != nil {
		return DraftDocument{}, fmt.Errorf("%w: draft: %w", ErrMalformedDocument, err)
	}
	if in.BaseVersion < 1 {
		return DraftDocument{}, fmt.Errorf("%w: base_version must be the version the draft was started from", ErrVersionConflict)
	}

	row, err := s.store.UpsertPageDraft(ctx, generated.UpsertPageDraftParams{
		PageID:      in.PageID,
		AuthorID:    in.AuthorID,
		Title:       in.Title,
		Doc:         in.Doc,
		BaseVersion: in.BaseVersion,
	})
	if err != nil {
		return DraftDocument{}, fmt.Errorf("saving page draft: %w", err)
	}
	return DraftDocument{
		Title:       row.Title,
		Doc:         row.Doc,
		BaseVersion: row.BaseVersion,
		UpdatedAt:   row.UpdatedAt.Time,
	}, nil
}

// DiscardDraft removes the caller's draft. It reports ErrDraftNotFound when
// there was none, so a confirmed destructive action cannot report success for
// something it did not do.
func (s *DocumentService) DiscardDraft(ctx context.Context, pageID, authorID uuid.UUID) error {
	removed, err := s.store.DeletePageDraft(ctx, generated.DeletePageDraftParams{
		PageID:   pageID,
		AuthorID: authorID,
	})
	if err != nil {
		return fmt.Errorf("discarding page draft: %w", err)
	}
	if removed == 0 {
		return ErrDraftNotFound
	}
	return nil
}

// DraftSummary is one row of the Codex Drafts view.
type DraftSummary struct {
	PageID      uuid.UUID `json:"page_id"`
	PageTitle   string    `json:"page_title"`
	DraftTitle  string    `json:"draft_title"`
	BaseVersion int32     `json:"base_version"`
	PageVersion int32     `json:"page_version"`
	Path        string    `json:"path"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Stale is true when the page moved on since the draft was started.
	Stale bool `json:"stale"`
}

// DraftsInSpace lists the pages in one space on which the caller holds a draft.
func (s *DocumentService) DraftsInSpace(ctx context.Context, spaceID, authorID uuid.UUID) ([]DraftSummary, error) {
	rows, err := s.store.ListPageDraftsForAuthorInSpace(ctx, generated.ListPageDraftsForAuthorInSpaceParams{
		AuthorID: authorID,
		SpaceID:  spaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing page drafts: %w", err)
	}
	out := make([]DraftSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, DraftSummary{
			PageID:      row.PageID,
			PageTitle:   row.PageTitle,
			DraftTitle:  row.DraftTitle,
			BaseVersion: row.BaseVersion,
			PageVersion: row.PageVersion,
			Path:        row.Path,
			UpdatedAt:   row.UpdatedAt.Time,
			Stale:       row.BaseVersion != row.PageVersion,
		})
	}
	return out, nil
}

// PublishInput carries one publish.
type PublishInput struct {
	PageID uuid.UUID
	// OrgID is the org the page belongs to. It is here for one reason: tags are
	// org-scoped, and the inline `#tag` tokens in a body are resolved against
	// the org's tag table at publish.
	OrgID       uuid.UUID
	AuthorID    uuid.UUID
	Title       string
	Doc         json.RawMessage
	BaseVersion int32
	// AcknowledgedLostIDs are the preservation ids the author has deliberately
	// deleted. Deleting an inert preserved block is a legitimate edit, so it has
	// to be possible — but it has to be SAID, because the alternative is that a
	// schema-level drop is indistinguishable from an intentional deletion.
	AcknowledgedLostIDs []string
	// Overwrite publishes over a page that has moved on since BaseVersion. Only
	// reachable after the conflict has been reported.
	Overwrite bool
}

// LostContentDetail reports the preserved content a publish would have removed.
type LostContentDetail struct {
	PageID uuid.UUID `json:"page_id"`
	// LostIDs are the preservation ids that are gone and unacknowledged.
	LostIDs []string `json:"lost_ids"`
	// Lost describes each one, so the author is told what would be lost rather
	// than being shown an opaque identifier.
	Lost    []LostContentItem `json:"lost"`
	Message string            `json:"message"`
}

// LostContentItem describes one piece of preserved content that went missing.
type LostContentItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Text string `json:"text"`
}

// PublishResult is what a publish attempt produced. Exactly one of Page,
// Conflict and LostContent is set.
type PublishResult struct {
	Page        generated.Page
	Conflict    *ConflictDetail
	LostContent *LostContentDetail
}

// Publish replaces a page's published content with the given document, bumps
// the version, records a revision, and clears the caller's draft — all in one
// transaction.
func (s *DocumentService) Publish(ctx context.Context, in PublishInput) (PublishResult, error) {
	if strings.TrimSpace(in.Title) == "" {
		return PublishResult{}, ErrEmptyTitle
	}
	if err := doc.Validate(in.Doc); err != nil {
		return PublishResult{}, fmt.Errorf("%w: %w", ErrMalformedDocument, err)
	}

	page, err := s.page(ctx, in.PageID)
	if err != nil {
		return PublishResult{}, err
	}

	// Refusal 1: the page moved on under the author, and they have not said to
	// overwrite it. Reported through the same ConflictDetail shape the markdown
	// save path has always used, rather than a second conflict format.
	if !in.Overwrite && page.Version != in.BaseVersion {
		return PublishResult{Conflict: conflictFor(in.PageID, in.BaseVersion, page)}, nil
	}

	document, lost, err := s.resolvePreservedContent(ctx, page, in)
	if err != nil {
		return PublishResult{}, err
	}
	if lost != nil {
		return PublishResult{LostContent: lost}, nil
	}

	content, err := doc.ToMarkdown(document)
	if err != nil {
		return PublishResult{}, fmt.Errorf("projecting the document for search: %w", err)
	}

	// The tag ROWS are created here, outside the transaction, and the
	// ASSOCIATIONS are written inside it. See PublishPageTxInput.TagIDs for why
	// the split falls where it does.
	tagIDs, err := s.inlineTagIDs(ctx, in.OrgID, document)
	if err != nil {
		return PublishResult{}, err
	}

	return s.commit(ctx, in, document, content, tagIDs)
}

// inlineTagIDs resolves the document's inline `#tag` tokens to org tag ids.
//
// The restored document is walked, not the incoming one: a `#tag` inside
// content the author never saw resolved is not something they typed, and the
// restored document is what actually gets stored.
//
// A document with no inline tags returns early and never reaches the tag model
// — which is also what makes an OrgID-less caller harmless until it publishes a
// document that actually carries one.
func (s *DocumentService) inlineTagIDs(ctx context.Context, orgID uuid.UUID, document json.RawMessage) ([]uuid.UUID, error) {
	labels, err := doc.InlineTagLabels(document)
	if err != nil {
		return nil, fmt.Errorf("reading the document's inline tags: %w", err)
	}
	if len(labels) == 0 {
		return nil, nil
	}
	if orgID == uuid.Nil {
		// Tags are org-scoped, so there is nowhere to put these. Refusing is the
		// only honest answer: creating them under a zero org would fail the
		// foreign key, and dropping them silently would publish a page whose
		// visible `#tag` tokens aggregate to nothing.
		return nil, fmt.Errorf("publishing a document with inline tags needs an org id")
	}
	ids, err := s.tags.ResolveForPublish(ctx, orgID, labels)
	if err != nil {
		return nil, fmt.Errorf("resolving the document's inline tags: %w", err)
	}
	return ids, nil
}

// commit runs the publish transaction and turns a lost race into the same
// conflict the pre-check would have reported.
//
// The race is real and the reason the version guard lives in the UPDATE: two
// publishes can both pass the check in [DocumentService.Publish] and then both
// try to write, and the second one has to be told it conflicted rather than
// silently overwriting.
func (s *DocumentService) commit(ctx context.Context, in PublishInput, document json.RawMessage, content string, tagIDs []uuid.UUID) (PublishResult, error) {
	published, err := s.tx.PublishPageTx(ctx, PublishPageTxInput{
		PageID:      in.PageID,
		AuthorID:    in.AuthorID,
		Title:       in.Title,
		Content:     content,
		Doc:         document,
		BaseVersion: in.BaseVersion,
		Overwrite:   in.Overwrite,
		TagIDs:      tagIDs,
	})
	if errors.Is(err, ErrVersionConflict) {
		current, getErr := s.page(ctx, in.PageID)
		if getErr != nil {
			return PublishResult{}, getErr
		}
		return PublishResult{Conflict: conflictFor(in.PageID, in.BaseVersion, current)}, nil
	}
	if err != nil {
		return PublishResult{}, fmt.Errorf("publishing page: %w", err)
	}
	return PublishResult{Page: published}, nil
}

// resolvePreservedContent puts the preserved originals back into the incoming
// document and applies the three refusals that stand between an edit and stored
// content. It returns the document to store, or a non-nil report of preserved
// content that would be lost.
//
// The order of these refusals is the contract, so they stay in one place: an
// unresolvable placeholder is a client or session defect and must be answered
// before anything is judged "lost", and nothing is written at all until every
// image reference has been confirmed.
func (s *DocumentService) resolvePreservedContent(ctx context.Context, page generated.Page, in PublishInput) (json.RawMessage, *LostContentDetail, error) {
	base, err := s.baseDocument(ctx, page, in.BaseVersion)
	if err != nil {
		return nil, nil, err
	}
	shielded, err := doc.Shield(base)
	if err != nil {
		return nil, nil, fmt.Errorf("re-deriving the document being edited: %w", err)
	}
	restored, err := doc.Restore(in.Doc, shielded)
	if err != nil {
		return nil, nil, fmt.Errorf("restoring preserved content: %w", err)
	}

	// Refusal 2: a placeholder with nothing behind it. Storing it verbatim would
	// turn a display stand-in into the content itself.
	if len(restored.Unresolved) > 0 {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownPreservedContent, strings.Join(restored.Unresolved, ", "))
	}

	// Refusal 3: preserved content is gone and nobody said to remove it. This is
	// the ADR-0012 failure caught before it commits.
	if lost := unacknowledged(restored.Dropped, in.AcknowledgedLostIDs); len(lost) > 0 {
		return nil, lostContentFor(in.PageID, lost, shielded), nil
	}

	// Refusal 4: an image node pointing somewhere it should not.
	if err := s.checkImages(ctx, in.PageID, restored.Document); err != nil {
		return nil, nil, err
	}
	return restored.Document, nil, nil
}

// RestoreInput carries one restore of an earlier version.
type RestoreInput struct {
	PageID   uuid.UUID
	OrgID    uuid.UUID
	AuthorID uuid.UUID
	// Version is the revision whose content is being restored.
	Version int32
	// BaseVersion is the version the caller believes is current, and it is what
	// the ordinary publish version guard is applied against. A restore gets no
	// exemption from it: if somebody else published while this one was being
	// considered, the answer is the same conflict dialogue as any other edit.
	BaseVersion int32
	// AcknowledgedLostIDs answers the lost-preserved-content refusal, exactly as
	// it does for a manual edit — see [DocumentService.RestoreRevision].
	AcknowledgedLostIDs []string
	Overwrite           bool
}

// RestoreResult is what a restore attempt produced.
type RestoreResult struct {
	PublishResult
	// NoOp is true when the requested version's content is already the page's
	// content. Restoring the current version produces no new version and says
	// so, rather than adding an entry to the history in which nothing changed.
	NoOp bool
	// Message is the sentence to show for a no-op.
	Message string
}

// RestoreRevision republishes an earlier version's content as a NEW version.
//
// # History is never rewritten
//
// Restoring version 3 onto a page at version 7 produces version 8 whose content
// is version 3's. Versions 4 to 7 stay in the history exactly as they were. That
// is the same append-only philosophy the audit log follows, and it means a
// restore is itself undoable — by restoring version 7.
//
// # A restore is an ordinary publish, and gets no bypass
//
// It goes through [DocumentService.Publish] rather than around it, so every
// refusal that stands between an edit and stored content stands here too:
//
//   - the version guard, so a restore cannot silently overwrite somebody else's
//     publish that landed while this one was being decided;
//   - the lost-preserved-content refusal, which is the important one. An older
//     version will usually NOT contain preserved unknown content that the
//     current version has — that is what makes it older. Restoring it therefore
//     removes that content, and ADR-0012 says a removal must be acknowledged,
//     not inferred. So the same `acknowledged_lost_ids` confirmation fires, with
//     the same list of what would go, as if the author had deleted the blocks by
//     hand. Giving restore an exemption would make it the one way to lose
//     preserved content without being asked.
//
// # A markdown-era revision
//
// A revision written before the document editor carries `doc IS NULL` and only
// markdown. It is converted with the same deterministic [doc.FromMarkdown] the
// editor's own on-ramp uses, and then published as a document — so restoring an
// old markdown version onto a document-backed page leaves the page
// document-backed, with `content` re-derived from it. This composes with D54
// rather than working around it: D54 is about the *markdown save* route writing
// `content` without `doc`, and this path writes both.
func (s *DocumentService) RestoreRevision(ctx context.Context, in RestoreInput) (RestoreResult, error) {
	page, err := s.page(ctx, in.PageID)
	if err != nil {
		return RestoreResult{}, err
	}

	revision, err := s.store.GetPageRevisionDocument(ctx, generated.GetPageRevisionDocumentParams{
		PageID:  in.PageID,
		Version: in.Version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RestoreResult{}, fmt.Errorf("%w: version %d", ErrRevisionNotFound, in.Version)
	}
	if err != nil {
		return RestoreResult{}, fmt.Errorf("getting the revision to restore: %w", err)
	}

	document := revision.Doc
	if len(document) == 0 {
		document, err = doc.FromMarkdown(revision.Content)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("converting the revision being restored: %w", err)
		}
	}

	// Restoring what is already there is a no-op with a sentence, not an empty
	// version. The comparison is against the page's *derived* document so that a
	// legacy markdown page — where both sides come out of FromMarkdown — answers
	// the same way a document-backed one does.
	current, _, err := documentOfPage(page)
	if err != nil {
		return RestoreResult{}, err
	}
	if page.Title == revision.Title && bytes.Equal(current, document) {
		return RestoreResult{
			NoOp:    true,
			Message: fmt.Sprintf("Version %d is already this page's content, so there was nothing to restore.", in.Version),
		}, nil
	}

	published, err := s.Publish(ctx, PublishInput{
		PageID:              in.PageID,
		OrgID:               in.OrgID,
		AuthorID:            in.AuthorID,
		Title:               revision.Title,
		Doc:                 document,
		BaseVersion:         in.BaseVersion,
		AcknowledgedLostIDs: in.AcknowledgedLostIDs,
		Overwrite:           in.Overwrite,
	})
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{PublishResult: published}, nil
}

// page loads a live page, mapping absence to ErrPageNotFound.
func (s *DocumentService) page(ctx context.Context, pageID uuid.UUID) (generated.Page, error) {
	page, err := s.store.GetPageByID(ctx, pageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return generated.Page{}, ErrPageNotFound
	}
	if err != nil {
		return generated.Page{}, fmt.Errorf("getting page: %w", err)
	}
	return page, nil
}

// baseDocument recovers the document a draft was started from.
//
// For the ordinary path that is the page as it stands. For an overwrite after a
// conflict it is the page as it stood at BaseVersion, recovered from history —
// because the preservation ids in the incoming document were handed out against
// THAT document, and resolving them against the current one would splice the
// wrong bytes into somebody's page.
func (s *DocumentService) baseDocument(ctx context.Context, page generated.Page, baseVersion int32) (json.RawMessage, error) {
	if page.Version == baseVersion {
		base, _, err := documentOfPage(page)
		return base, err
	}

	revision, err := s.store.GetPageRevisionDocument(ctx, generated.GetPageRevisionDocumentParams{
		PageID:  page.ID,
		Version: baseVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: version %d", ErrBaseVersionUnavailable, baseVersion)
	}
	if err != nil {
		return nil, fmt.Errorf("getting the revision a draft was started from: %w", err)
	}
	if len(revision.Doc) > 0 {
		return revision.Doc, nil
	}
	converted, err := doc.FromMarkdown(revision.Content)
	if err != nil {
		return nil, fmt.Errorf("converting the revision a draft was started from: %w", err)
	}
	return converted, nil
}

// checkImages resolves every image node's attachment against the page.
//
// The upload path sniffs the bytes, but the generic attachment endpoint can put
// any file on a page, so an image node could still name a spreadsheet. This is
// where a document's image references are made true: the attachment must be on
// THIS page and must actually be an image.
func (s *DocumentService) checkImages(ctx context.Context, pageID uuid.UUID, document json.RawMessage) error {
	ids, err := doc.ImageAttachmentIDs(document)
	if err != nil {
		return fmt.Errorf("reading the document's image references: %w", err)
	}
	for _, id := range ids {
		attachmentID, parseErr := uuid.Parse(id)
		if parseErr != nil {
			return fmt.Errorf("%w: %q is not an attachment id", ErrImageNotOnPage, id)
		}
		contentType, err := s.images.PageImageContentType(ctx, pageID, attachmentID)
		if err != nil {
			// Deliberately one condition. "No such attachment" and "that
			// attachment belongs to another entity" are the same answer from a
			// document's point of view, and telling them apart would report
			// whether an id exists somewhere the author cannot see.
			return fmt.Errorf("%w: %s", ErrImageNotOnPage, attachmentID)
		}
		if !doc.SupportedImageType(contentType) {
			return fmt.Errorf("%w: %s", ErrImageNotAnImage, contentType)
		}
	}
	return nil
}

// documentOfPage returns a page's editable document and where it came from.
func documentOfPage(page generated.Page) (json.RawMessage, string, error) {
	if len(page.Doc) > 0 {
		return page.Doc, SourceFormatDocument, nil
	}
	converted, err := doc.FromMarkdown(page.Content)
	if err != nil {
		return nil, "", fmt.Errorf("converting the page's markdown: %w", err)
	}
	return converted, SourceFormatMarkdown, nil
}

// conflictFor builds the shared 409 body.
func conflictFor(pageID uuid.UUID, expected int32, current generated.Page) *ConflictDetail {
	return &ConflictDetail{
		PageID:          pageID,
		ExpectedVersion: expected,
		CurrentPage:     current,
		Message: fmt.Sprintf(
			"This page was published again while you were editing — it is now at version %d, "+
				"and your draft started from version %d. Reload to see the new version (your draft is kept), "+
				"or publish anyway to replace it.",
			current.Version, expected),
	}
}

// lostContentFor describes the preserved content a publish would remove.
func lostContentFor(pageID uuid.UUID, lost []string, shielded doc.Shielded) *LostContentDetail {
	items := make([]LostContentItem, 0, len(lost))
	for _, id := range lost {
		item := LostContentItem{ID: id}
		if original, ok := shielded.Captured[id]; ok {
			item.Name = doc.PreservedName(original)
			item.Text = doc.PlainText(original)
		}
		items = append(items, item)
	}
	return &LostContentDetail{
		PageID:  pageID,
		LostIDs: lost,
		Lost:    items,
		Message: fmt.Sprintf(
			"Publishing would remove %d block(s) of preserved content that this editor cannot display. "+
				"That content is still in the published page. Reload the page to get it back, "+
				"or confirm the removal if you meant to delete it.",
			len(lost)),
	}
}

// unacknowledged returns the dropped ids the author did not declare.
func unacknowledged(dropped, acknowledged []string) []string {
	if len(dropped) == 0 {
		return nil
	}
	declared := make(map[string]bool, len(acknowledged))
	for _, id := range acknowledged {
		declared[id] = true
	}
	out := make([]string, 0, len(dropped))
	for _, id := range dropped {
		if !declared[id] {
			out = append(out, id)
		}
	}
	return out
}
