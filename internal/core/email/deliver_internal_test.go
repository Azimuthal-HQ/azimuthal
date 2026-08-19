package email

import (
	"bufio"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

// serverLog records what the fake SMTP server observed, so a test can assert the
// envelope exchange actually happened.
type serverLog struct {
	sawAuth  bool
	mailFrom string
	rcpt     []string
	body     []string
}

// fakeSMTPServer speaks just enough ESMTP to drive SMTPSender.deliver over a
// plaintext connection. It advertises AUTH PLAIN, accepts the credentials, and
// records the envelope. It is deliberately permissive — the subject under test
// is the client, not server validation.
func fakeSMTPServer(conn net.Conn, log *serverLog) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) {
		_, _ = w.WriteString(s)
		_ = w.Flush()
	}

	write("220 fake ESMTP\r\n")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				write("250 2.0.0 Ok: queued\r\n")
				continue
			}
			log.body = append(log.body, line)
			continue
		}

		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			write("250-fakehost\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(line, "AUTH"):
			log.sawAuth = true
			write("235 2.7.0 Authentication successful\r\n")
		case strings.HasPrefix(line, "MAIL FROM"):
			log.mailFrom = line
			write("250 2.1.0 Ok\r\n")
		case strings.HasPrefix(line, "RCPT TO"):
			log.rcpt = append(log.rcpt, line)
			write("250 2.1.5 Ok\r\n")
		case line == "DATA":
			inData = true
			write("354 End data with <CR><LF>.<CR><LF>\r\n")
		case line == "QUIT":
			write("221 2.0.0 Bye\r\n")
			return
		default:
			write("250 Ok\r\n")
		}
	}
}

// dialFake connects to a just-started fake server and returns a client whose
// server name is "localhost", which is what lets net/smtp permit PlainAuth over
// this unencrypted test connection (it allows plaintext auth to localhost).
func dialFake(t *testing.T, addr string) *smtp.Client {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial fake smtp: %v", err)
	}
	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		t.Fatalf("smtp client: %v", err)
	}
	return client
}

// TestSMTPSender_Deliver runs the envelope exchange against the fake server,
// with and without credentials, so both the auth and no-auth branches of
// deliver() are exercised and the message is seen to reach DATA.
func TestSMTPSender_Deliver(t *testing.T) {
	for _, tc := range []struct {
		name       string
		user, pass string
		wantAuth   bool
	}{
		{name: "with auth", user: "mailer", pass: "s3cret", wantAuth: true},
		{name: "no auth", wantAuth: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer func() { _ = ln.Close() }()

			log := &serverLog{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, aerr := ln.Accept()
				if aerr != nil {
					return
				}
				fakeSMTPServer(conn, log)
			}()

			s := &SMTPSender{host: "localhost", from: "from@example.com", username: tc.user, password: tc.pass}
			body := buildMIMEMessage("from@example.com", []string{"to@example.com"}, "Hi", "<p>body</p>")
			if err := s.deliver(dialFake(t, ln.Addr().String()), "from@example.com", []string{"to@example.com"}, body); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			<-done

			if log.sawAuth != tc.wantAuth {
				t.Errorf("server saw AUTH = %v, want %v", log.sawAuth, tc.wantAuth)
			}
			if !strings.Contains(log.mailFrom, "from@example.com") {
				t.Errorf("MAIL FROM = %q, want it to carry the sender", log.mailFrom)
			}
			if len(log.rcpt) != 1 || !strings.Contains(log.rcpt[0], "to@example.com") {
				t.Errorf("RCPT = %v, want the single recipient", log.rcpt)
			}
			joined := strings.Join(log.body, "\n")
			if !strings.Contains(joined, "Subject: Hi") {
				t.Errorf("DATA body did not carry the message: %q", joined)
			}
		})
	}
}
