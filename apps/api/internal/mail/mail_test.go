package mail

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAndDir(t *testing.T) {
	dir := t.TempDir()
	d := Dir{Path: dir, From: "I Am Here <no-reply@example.org>"}
	if err := d.Send(context.Background(), Message{To: "a@example.org", Subject: "Réinitialiser · ئاوات", Text: "Hi\nlink", HTML: "<p>Hi</p>"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.eml"))
	if len(files) != 1 {
		t.Fatalf("%v", files)
	}
	b, _ := os.ReadFile(files[0])
	m := string(b)
	for _, want := range []string{"From: I Am Here <no-reply@example.org>\r\n", "To: a@example.org\r\n", "Subject: =?utf-8?q?", "multipart/alternative",
		"Content-Type: text/plain; charset=utf-8", "Content-Type: text/html; charset=utf-8", "Message-ID: <", "@example.org>"} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q in:\n%s", want, m)
		}
	}
}

func TestNewSMTP(t *testing.T) {
	for _, bad := range []string{"", "http://x", "smtp://", "mailhost:25"} {
		if _, err := NewSMTP(bad, "a@b.org"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := NewSMTP("smtp://u:p@h:587", "not an address"); err == nil {
		t.Error("bad From accepted")
	}
	s, err := NewSMTP("smtps://u:p@mail.example.org", "I Am Here <a@example.org>")
	if err != nil || s.addr != "mail.example.org:465" || !s.implicit {
		t.Fatalf("%+v %v", s, err)
	}
}

// A server that does not offer STARTTLS never gets the credentials.
func TestSMTPRefusesPlaintext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		var seen strings.Builder
		c.Write([]byte("220 test ESMTP\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			seen.WriteString(line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				c.Write([]byte("250-test\r\n250 AUTH PLAIN\r\n"))
			case strings.HasPrefix(line, "QUIT"):
				c.Write([]byte("221 bye\r\n"))
				got <- seen.String()
				return
			default:
				c.Write([]byte("250 ok\r\n"))
			}
		}
		got <- seen.String()
	}()
	s, _ := NewSMTP("smtp://user:secret@"+ln.Addr().String(), "a@example.org")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = s.Send(ctx, Message{To: "b@example.org", Subject: "x", Text: "y"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("want a STARTTLS refusal, got %v", err)
	}
	select {
	case seen := <-got:
		if strings.Contains(seen, "AUTH") || strings.Contains(seen, "secret") {
			t.Fatalf("credentials sent in clear text:\n%s", seen)
		}
	case <-time.After(3 * time.Second):
	}
}
