// Package email provides the interface and implementations for sending email.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
)

// SMTP transport-security modes, matching the values internal/config validates
// for SMTP_TLS. They are plain strings rather than a shared type so this leaf
// package imports nothing from config; TestSMTPTLSModeConstantsAgree pins them
// to the config constants so the two cannot drift.
const (
	TLSNone     = "none"
	TLSStartTLS = "starttls"
	TLSImplicit = "implicit"
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

// SMTPSender delivers email via SMTP. It supports plaintext (for a local relay
// such as mailhog), STARTTLS, and implicit TLS, with optional username/password
// authentication — all operator-configured.
type SMTPSender struct {
	host     string
	port     int
	from     string
	username string
	password string
	// tlsMode is one of TLSNone, TLSStartTLS, TLSImplicit. The config layer has
	// already validated it, so an unrecognised value here is treated as
	// plaintext rather than re-validated.
	tlsMode string
}

// SMTPConfig groups the connection settings for NewSMTPSender.
type SMTPConfig struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
	// TLS is "none" (the default when empty), "starttls", or "implicit".
	TLS string
}

// NewSMTPSender creates an SMTPSender from cfg. An empty TLS mode means
// plaintext. The From address is used when Message.From is empty.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	mode := cfg.TLS
	if mode == "" {
		mode = TLSNone
	}
	return &SMTPSender{
		host:     cfg.Host,
		port:     cfg.Port,
		from:     cfg.From,
		username: cfg.Username,
		password: cfg.Password,
		tlsMode:  mode,
	}
}

// Send delivers msg via SMTP, following the configured transport mode.
func (s *SMTPSender) Send(_ context.Context, msg Message) error {
	from := msg.From
	if from == "" {
		from = s.from
	}
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	body := buildMIMEMessage(from, msg.To, msg.Subject, msg.Body)

	switch s.tlsMode {
	case TLSImplicit:
		return s.sendImplicitTLS(addr, from, msg.To, body)
	case TLSStartTLS:
		return s.sendStartTLS(addr, from, msg.To, body)
	default:
		return s.sendPlain(addr, from, msg.To, body)
	}
}

// tlsConfig is the client TLS configuration shared by the STARTTLS and implicit
// paths. ServerName pins verification to the configured host, and MinVersion is
// TLS 1.2 — verification is NEVER skipped (that would defeat the point of adding
// TLS at all, and gosec G402 would rightly refuse it).
func (s *SMTPSender) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName: s.host,
		MinVersion: tls.VersionTLS12,
	}
}

// auth returns the PLAIN authenticator when credentials are configured, or nil
// when they are not. net/smtp refuses PlainAuth over an unencrypted, non-
// localhost connection itself — which is the backstop for the auth-without-TLS
// configuration that config warns about but does not refuse.
func (s *SMTPSender) auth() smtp.Auth {
	if s.username == "" && s.password == "" {
		return nil
	}
	return smtp.PlainAuth("", s.username, s.password, s.host)
}

// sendPlain is the plaintext path. smtp.SendMail still opportunistically issues
// STARTTLS when the server advertises it, so "none" is "no TLS required" rather
// than "TLS forbidden".
func (s *SMTPSender) sendPlain(addr, from string, to []string, body []byte) error {
	// #nosec G402 -- plaintext SMTP is an explicit, operator-selected mode for a
	// local relay (mailhog) or a trusted-network internal relay; the encrypted
	// modes are starttls/implicit. SendMail upgrades to STARTTLS when offered.
	if err := smtp.SendMail(addr, s.auth(), from, to, body); err != nil {
		return fmt.Errorf("smtp send to %s: %w", strings.Join(to, ","), err)
	}
	return nil
}

// sendImplicitTLS wraps the connection in TLS from the first byte (port-465
// style) before speaking SMTP.
func (s *SMTPSender) sendImplicitTLS(addr, from string, to []string, body []byte) error {
	conn, err := tls.Dial("tcp", addr, s.tlsConfig())
	if err != nil {
		return fmt.Errorf("smtp tls dial %s: %w", addr, err)
	}
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client for %s: %w", addr, err)
	}
	return s.deliver(client, from, to, body)
}

// sendStartTLS connects in the clear and upgrades with STARTTLS before
// authenticating or sending. A server that does not offer STARTTLS fails here,
// which is correct: the operator asked for it explicitly.
func (s *SMTPSender) sendStartTLS(addr, from string, to []string, body []byte) error {
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	if err := client.StartTLS(s.tlsConfig()); err != nil {
		_ = client.Close()
		return fmt.Errorf("smtp starttls to %s: %w", addr, err)
	}
	return s.deliver(client, from, to, body)
}

// deliver runs the SMTP envelope + data exchange on an already-connected client,
// authenticating first when credentials are configured. Shared by the STARTTLS
// and implicit-TLS paths.
func (s *SMTPSender) deliver(client *smtp.Client, from string, to []string, body []byte) error {
	defer func() { _ = client.Close() }()

	if a := s.auth(); a != nil {
		if err := client.Auth(a); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from %s: %w", from, err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write to %s: %w", strings.Join(to, ","), err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
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
