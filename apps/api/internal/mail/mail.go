// Package mail sends the few emails the service needs (email confirmation,
// password reset) through SMTP, or writes them to a directory in development.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message is a plain-text email with an optional HTML alternative.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages. Enabled reports whether email can reach people at
// all (the web app hides "forgot password" otherwise).
type Sender interface {
	Send(ctx context.Context, m Message) error
	Enabled() bool
}

// Off sends nothing.
type Off struct{}

func (Off) Send(context.Context, Message) error { return errors.New("mail: not configured") }
func (Off) Enabled() bool                       { return false }

// Dir writes each message as an .eml file (development and tests).
type Dir struct {
	Path string
	From string
}

func (d Dir) Enabled() bool { return true }

func (d Dir) Send(_ context.Context, m Message) error {
	if err := os.MkdirAll(d.Path, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.eml", time.Now().UnixNano(), randHex(4))
	return os.WriteFile(filepath.Join(d.Path, name), build(d.From, m), 0o600)
}

// SMTP sends through a mail server: smtp://user:pass@host:587 (STARTTLS,
// required) or smtps://user:pass@host:465 (TLS from the start). Credentials
// never travel unencrypted.
type SMTP struct {
	addr, host string
	implicit   bool
	auth       smtp.Auth
	from       string
}

// NewSMTP parses an smtp:// or smtps:// URL.
func NewSMTP(rawURL, from string) (*SMTP, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Hostname() == "" {
		return nil, errors.New("mail: IAH_SMTP_URL must look like smtp://user:password@host:587 or smtps://user:password@host:465")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("mail: IAH_MAIL_FROM is not an email address: %v", err)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"smtp": "587", "smtps": "465"}[u.Scheme]
	}
	s := &SMTP{addr: net.JoinHostPort(u.Hostname(), port), host: u.Hostname(), implicit: u.Scheme == "smtps", from: from}
	if u.User != nil {
		pw, _ := u.User.Password()
		s.auth = smtp.PlainAuth("", u.User.Username(), pw, u.Hostname())
	}
	return s, nil
}

func (s *SMTP) Enabled() bool { return true }

func (s *SMTP) Send(ctx context.Context, m Message) error {
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	tlsCfg := &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}
	if s.implicit {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", s.addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", s.addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if !s.implicit {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("mail: server does not offer STARTTLS; refusing to send credentials in clear text")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if s.auth != nil {
		if err := c.Auth(s.auth); err != nil {
			return err
		}
	}
	from, _ := mail.ParseAddress(s.from)
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(build(s.from, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// build renders a MIME message (UTF-8, quoted-printable, text + HTML).
func build(from string, m Message) []byte {
	var b bytes.Buffer
	boundary := "iah-" + randHex(12)
	host := "iamhere"
	if a, err := mail.ParseAddress(from); err == nil {
		if _, h, ok := strings.Cut(a.Address, "@"); ok {
			host = h
		}
	}
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	hdr("From", from)
	hdr("To", m.To)
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", time.Now().UTC().Format(time.RFC1123Z))
	hdr("Message-ID", fmt.Sprintf("<%s@%s>", randHex(16), host))
	hdr("MIME-Version", "1.0")
	hdr("Auto-Submitted", "auto-generated")
	part := func(ctype, body string) {
		fmt.Fprintf(&b, "Content-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", ctype)
		qp := quotedprintable.NewWriter(&b)
		_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
		_ = qp.Close()
		b.WriteString("\r\n")
	}
	if m.HTML == "" {
		part("text/plain", m.Text)
		return b.Bytes()
	}
	hdr("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	part("text/plain", m.Text)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	part("text/html", m.HTML)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
