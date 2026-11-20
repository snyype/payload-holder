package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResetCode(t *testing.T) {
	for i := 0; i < 50; i++ {
		c := newResetCode()
		if len(c) != 6 || strings.Trim(c, "0123456789") != "" {
			t.Fatalf("bad code %q", c)
		}
	}
	if got := normalizeCode(" 123 - 456 "); got != "123456" {
		t.Fatalf("normalizeCode = %q", got)
	}
}

func TestResetCodeCheck(t *testing.T) {
	check := resetCodeCheck("123456", time.Now().Add(time.Minute))
	if check("123 456") != nil {
		t.Fatal("right code (with a space) rejected")
	}
	if check("123456") != nil {
		t.Fatal("revalidating the accepted code must pass")
	}

	// Wrong guesses run out; the same wrong guess twice counts once.
	check = resetCodeCheck("123456", time.Now().Add(time.Minute))
	_ = check("000000")
	if err := check("000000"); err == nil || !strings.Contains(err.Error(), "4 tries left") {
		t.Fatalf("repeat guess: %v", err)
	}
	for _, g := range []string{"000001", "000002", "000003", "000004"} {
		_ = check(g)
	}
	if err := check("123456"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("right code after too many tries: %v", err)
	}

	expired := resetCodeCheck("123456", time.Now().Add(-time.Second))
	if err := expired("123456"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired: %v", err)
	}
}

func TestSMTPConfigRoundTrip(t *testing.T) {
	t.Setenv("PAYLOAD_STORE", t.TempDir())
	if c, err := loadSMTP(); c != nil || err != nil {
		t.Fatalf("nothing expected, got %v %v", c, err)
	}
	c := &smtpConfig{Provider: providerMailtrap, FromEmail: "no-reply@example.com", FromName: "payload"}
	if err := c.setPassword("tok-secret-123"); err != nil {
		t.Fatal(err)
	}
	if err := saveSMTP(c); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(smtpPath()); strings.Contains(string(data), "tok-secret-123") {
		t.Fatal("the token must be stored encrypted")
	}
	got, err := loadSMTP()
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	if pw, err := got.password(); err != nil || pw != "tok-secret-123" {
		t.Fatalf("password() = %q, %v", pw, err)
	}

	// An SMTP config without a host is damaged.
	_ = os.WriteFile(smtpPath(), []byte(`{"provider":"smtp","from_email":"a@b.c"}`), 0o600)
	if _, err := loadSMTP(); err == nil {
		t.Fatal("damaged config accepted")
	}
}

func TestMailtrapSend(t *testing.T) {
	t.Setenv("PAYLOAD_STORE", t.TempDir())
	var gotAuth, gotPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		if strings.Contains(gotAuth, "bad") {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"errors":["Unauthorized"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"message_ids":["x"]}`))
	}))
	defer srv.Close()
	oldSend, oldSandbox := mailtrapSendURL, mailtrapSandboxURL
	mailtrapSendURL, mailtrapSandboxURL = srv.URL+"/api/send", srv.URL+"/api/send/"
	defer func() { mailtrapSendURL, mailtrapSandboxURL = oldSend, oldSandbox }()

	c := &smtpConfig{Provider: providerMailtrap, FromEmail: "no-reply@example.com", FromName: "payload"}
	_ = c.setPassword("good-token")
	m := resetCodeMail("me@example.com", "123456", "laptop", time.Now(), resetCodeValid)
	if err := c.send(m); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer good-token" || gotPath != "/api/send" {
		t.Fatalf("auth %q path %q", gotAuth, gotPath)
	}
	if body["subject"] != "Your payload reset code: 123 456" || !strings.Contains(body["html"].(string), "123 456") {
		t.Fatalf("body %v", body["subject"])
	}

	c.InboxID = "42"
	if err := c.send(m); err != nil || gotPath != "/api/send/42" {
		t.Fatalf("sandbox: %v %q", err, gotPath)
	}

	_ = c.setPassword("bad-token")
	if err := c.send(m); err == nil || !strings.Contains(err.Error(), "API token") {
		t.Fatalf("bad token: %v", err)
	}
}

func TestBuildMIME(t *testing.T) {
	from := &mail.Address{Name: "payload", Address: "no-reply@example.com"}
	to := &mail.Address{Address: "me@example.com"}
	raw := string(buildMIME(from, to, mailMessage{Subject: "Héllo", Text: "plain", HTML: "<p>html</p>"}))
	for _, want := range []string{`From: "payload" <no-reply@example.com>`, "To: <me@example.com>",
		"Subject: =?utf-8?q?H=C3=A9llo?=", "multipart/alternative", "text/plain", "text/html"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q in\n%s", want, raw)
		}
	}
	if strings.Index(raw, "text/plain") > strings.Index(raw, "text/html") {
		t.Fatal("plain text must come before HTML")
	}
}

func TestMailHTMLEscapes(t *testing.T) {
	m := resetCodeMail("me@example.com", "123456", `<script>x</script>`, time.Now(), resetCodeValid)
	if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "&lt;script&gt;") {
		t.Fatal("computer name not escaped")
	}
}
