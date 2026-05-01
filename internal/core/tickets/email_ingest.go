package tickets

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"

	"github.com/google/uuid"
)

// InboundEmail represents a parsed inbound email that can be converted to a ticket.
type InboundEmail struct {
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// ParseInboundEmail parses a raw RFC 2822 email from the reader and extracts
// the sender, subject, and plain-text body.
func ParseInboundEmail(r io.Reader) (*InboundEmail, error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return nil, fmt.Errorf("parsing email: %w", err)
	}

	from := msg.Header.Get("From")
	subject := msg.Header.Get("Subject")

	body, err := extractBody(msg)
	if err != nil {
		return nil, fmt.Errorf("extracting email body: %w", err)
	}

	if from == "" || subject == "" {
		return nil, ErrEmailParseFailure
	}

	return &InboundEmail{
		From:    from,
		Subject: subject,
		Body:    body,
	}, nil
}

// extractBody reads the body from a mail.Message. It handles both plain text
// messages and multipart/alternative MIME messages, preferring text/plain.
func extractBody(msg *mail.Message) (string, error) {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Fall back to reading the whole body as text.
		b, readErr := io.ReadAll(msg.Body)
		if readErr != nil {
			return "", fmt.Errorf("reading email body: %w", readErr)
		}
		return strings.TrimSpace(string(b)), nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", fmt.Errorf("multipart email missing boundary")
		}
		return extractMultipartBody(msg.Body, boundary)
	}

	b, err := io.ReadAll(msg.Body)
	if err != nil {
		return "", fmt.Errorf("reading email body: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// extractMultipartBody reads a multipart message and returns the text/plain
// part. If no text/plain part is found, returns the first part's content.
func extractMultipartBody(r io.Reader, boundary string) (string, error) {
	mr := multipart.NewReader(r, boundary)
	var fallback string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading multipart: %w", err)
		}

		partType := part.Header.Get("Content-Type")
		b, readErr := io.ReadAll(part)
		if readErr != nil {
			return "", fmt.Errorf("reading part: %w", readErr)
		}

		content := strings.TrimSpace(string(b))
		if strings.HasPrefix(partType, "text/plain") {
			return content, nil
		}
		if fallback == "" {
			fallback = content
		}
	}

	return fallback, nil
}

// CreateFromEmail creates a new ticket from a parsed inbound email.
// The reporter is looked up by email address; if not found, reporterID must be
// provided as a fallback (e.g. a system user for external reporters).
func (s *TicketService) CreateFromEmail(ctx context.Context, email *InboundEmail, spaceID uuid.UUID, reporterID uuid.UUID) (*Ticket, error) {
	if email == nil {
		return nil, ErrEmailParseFailure
	}

	return s.Create(ctx, CreateTicketParams{
		SpaceID:     spaceID,
		Title:       email.Subject,
		Description: email.Body,
		Priority:    PriorityMedium,
		ReporterID:  reporterID,
	})
}
