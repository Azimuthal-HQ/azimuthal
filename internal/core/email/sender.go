// Package email provides the interface and implementations for sending email.
package email

import (
	"bytes"
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// Message represents an outbound email message.
type Message struct {
	From    string
	To      []string
	Subject string
	// Body is the HTML body of the email.
	Body string
}

// Sender is the interface for delivering email messages.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPSender delivers email via SMTP.
// For local development, use mailhog (no auth, no TLS required).
type SMTPSender struct {
	host string
	port int
	from string
}

// NewSMTPSender creates an SMTPSender that connects to the given host and port.
// The from address is used when the Message.From field is empty.
func NewSMTPSender(host string, port int, from string) *SMTPSender {
	return &SMTPSender{host: host, port: port, from: from}
}

// Send delivers msg via SMTP. It uses no authentication, which is appropriate
// for local relay (mailhog) and internal SMTP servers. For production use with
// auth, wrap this or extend with an authenticated variant.
func (s *SMTPSender) Send(_ context.Context, msg Message) error {
	from := msg.From
	if from == "" {
		from = s.from
	}

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	body := buildMIMEMessage(from, msg.To, msg.Subject, msg.Body)

	// #nosec G402 -- plain SMTP is intentional for local relay;
	// TLS is terminated upstream (e.g. by Caddy / load balancer).
	if err := smtp.SendMail(addr, nil, from, msg.To, body); err != nil {
		return fmt.Errorf("smtp send to %s: %w", strings.Join(msg.To, ","), err)
	}
	return nil
}

// buildMIMEMessage constructs a minimal MIME-formatted email body.
func buildMIMEMessage(from string, to []string, subject, htmlBody string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(htmlBody)
	return buf.Bytes()
}

// NoopSender discards all messages. Use in tests or when email is disabled.
type NoopSender struct{}

// Send discards the message and returns nil.
func (n *NoopSender) Send(_ context.Context, _ Message) error { return nil }
