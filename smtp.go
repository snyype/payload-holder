package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
)

// Mail provider for the mail payload sends itself — today only the master password reset code
// (payload ssh forgot). payload settings smtp sets it up: Mailtrap's sending API (an API token) by
// default, or any SMTP server. vault/smtp.json keeps the settings in plain text and the password or
// token sealed with this computer's secret store (DPAPI on Windows), like the GitHub token. It is
// not synced, so each computer sets up its own.

const (
	providerMailtrap = "mailtrap"
	providerSMTP     = "smtp"
)

// Mailtrap's sending API. With an inbox ID the sandbox API is used instead, which catches the mail
// in that Mailtrap inbox rather than delivering it.
var (
	mailtrapSendURL    = "https://send.api.mailtrap.io/api/send"
	mailtrapSandboxURL = "https://sandbox.api.mailtrap.io/api/send/"
)

type smtpConfig struct {
	Provider  string    `json:"provider,omitempty"` // "mailtrap" or "smtp" ("" reads as smtp)
	InboxID   string    `json:"inbox_id,omitempty"` // Mailtrap sandbox inbox; empty = real sending
	Host      string    `json:"host,omitempty"`
	Port      int       `json:"port,omitempty"`
	Security  string    `json:"security,omitempty"` // "starttls", "tls" or "none"
	Username  string    `json:"username,omitempty"`
	Password  string    `json:"password,omitempty"` // SMTP password or Mailtrap API token; sealed, base64
	Scheme    string    `json:"scheme,omitempty"`
	FromEmail string    `json:"from_email"`
	FromName  string    `json:"from_name,omitempty"`
	Updated   time.Time `json:"updated,omitzero"`
}

func smtpPath() string { return filepath.Join(storeDir(), "vault", "smtp.json") }

// loadSMTP returns the mail settings, or nil when none are set up.
func loadSMTP() (*smtpConfig, error) {
	data, err := os.ReadFile(smtpPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c smtpConfig
	err = json.Unmarshal(data, &c)
	if c.Provider == "" {
		c.Provider = providerSMTP
	}
	if err != nil || c.FromEmail == "" ||
		(c.Provider == providerSMTP && (c.Host == "" || c.Port <= 0)) ||
		(c.Provider == providerMailtrap && c.Password == "") {
		return nil, fmt.Errorf("%s is damaged — run payload settings smtp to set it up again", smtpPath())
	}
	return &c, nil
}

func saveSMTP(c *smtpConfig) error {
	c.Updated = time.Now().UTC()
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(smtpPath()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(smtpPath(), append(data, '\n'), 0o600)
}

func (c *smtpConfig) setPassword(pw string) error {
	sealed, err := sealString(pw)
	if err != nil {
		return err
	}
	c.Password, c.Scheme = sealed, secretScheme
	return nil
}

func (c *smtpConfig) password() (string, error) {
	if c.Password == "" {
		return "", nil
	}
	if c.Scheme != secretScheme {
		return "", errors.New("the mail password or token was saved on another system — run payload settings smtp to enter it again")
	}
	sealed, err := base64.StdEncoding.DecodeString(c.Password)
	if err != nil {
		return "", err
	}
	pw, err := openSecret(sealed)
	if err != nil {
		return "", fmt.Errorf("could not decrypt the mail password or token (%v) — run payload settings smtp to enter it again", err)
	}
	return string(pw), nil
}

func (c *smtpConfig) securityLabel() string {
	switch c.Security {
	case "tls":
		return "SSL/TLS"
	case "none":
		return "no encryption"
	}
	return "STARTTLS"
}

// summary is the provider in a few words, e.g. "Mailtrap sending API" or "smtp.gmail.com:587 · STARTTLS".
func (c *smtpConfig) summary() string {
	if c.Provider == providerMailtrap {
		if c.InboxID != "" {
			return "Mailtrap sandbox · inbox " + c.InboxID
		}
		return "Mailtrap sending API"
	}
	return fmt.Sprintf("%s:%d · %s", c.Host, c.Port, c.securityLabel())
}

// --- sending ------------------------------------------------------------------------------------

type mailMessage struct {
	To, Subject, Text, HTML string
}

// send delivers m through Mailtrap's API or the SMTP server.
func (c *smtpConfig) send(m mailMessage) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("bad recipient %q", m.To)
	}
	if c.Provider == providerMailtrap {
		return c.sendMailtrap(to, m)
	}
	return c.sendSMTP(to, m)
}

// sendMailtrap sends m with Mailtrap's sending API (or the sandbox API when an inbox ID is set).
func (c *smtpConfig) sendMailtrap(to *mail.Address, m mailMessage) error {
	token, err := c.password()
	if err != nil {
		return err
	}
	type addr struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	body, _ := json.Marshal(struct {
		From     addr   `json:"from"`
		To       []addr `json:"to"`
		Subject  string `json:"subject"`
		Text     string `json:"text"`
		HTML     string `json:"html"`
		Category string `json:"category"`
	}{addr{c.FromEmail, c.FromName}, []addr{{Email: to.Address}}, m.Subject, m.Text, m.HTML, "payload"})

	endpoint := mailtrapSendURL
	if c.InboxID != "" {
		endpoint = mailtrapSandboxURL + url.PathEscape(c.InboxID)
	}
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "payload/"+version)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return errors.New(networkProblem(err))
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	var e struct {
		Errors []string `json:"errors"`
		Error  string   `json:"error"`
	}
	_ = json.Unmarshal(reply, &e)
	detail := firstNonEmpty(strings.Join(e.Errors, "; "), e.Error, strings.TrimSpace(string(reply)), resp.Status)
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("Mailtrap rejected the API token (%s) — check it under Settings → API Tokens, and that it can send from this domain", detail)
	case strings.Contains(strings.ToLower(detail), "domain"):
		return fmt.Errorf("Mailtrap refused the message: %s — the from address must be on a domain verified in Mailtrap (Sending Domains)", detail)
	}
	return fmt.Errorf("Mailtrap refused the message (%s): %s", resp.Status, detail)
}

// sendSMTP delivers m through the SMTP server.
func (c *smtpConfig) sendSMTP(to *mail.Address, m mailMessage) error {
	from, err := mail.ParseAddress(c.FromEmail)
	if err != nil {
		return fmt.Errorf("bad sender address %q — run payload settings smtp", c.FromEmail)
	}
	from.Name = c.FromName
	pw, err := c.password()
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tlsConf := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if c.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConf)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("could not reach %s: %v", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("%s did not answer as an SMTP server: %v", addr, err)
	}
	defer cl.Close()

	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%s does not offer STARTTLS — choose SSL/TLS (usually port 465) in payload settings smtp", addr)
		}
		if err := cl.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("STARTTLS failed: %v", err)
		}
	}
	if c.Username != "" && c.Security != "none" {
		if err := cl.Auth(pickSMTPAuth(cl, c.Username, pw, c.Host)); err != nil {
			return smtpAuthError(c, err)
		}
	}
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("the server refused the sender %s: %v", from.Address, err)
	}
	if err := cl.Rcpt(to.Address); err != nil {
		return fmt.Errorf("the server refused the recipient %s: %v", to.Address, err)
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMIME(from, to, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the server did not accept the message: %v", err)
	}
	_ = cl.Quit()
	return nil
}

// pickSMTPAuth uses PLAIN, or LOGIN when that is all the server offers (Microsoft 365).
func pickSMTPAuth(cl *smtp.Client, user, pass, host string) smtp.Auth {
	if ok, mechs := cl.Extension("AUTH"); ok {
		m := " " + strings.ToUpper(mechs) + " "
		if !strings.Contains(m, " PLAIN ") && strings.Contains(m, " LOGIN ") {
			return loginAuth{user, pass}
		}
	}
	return smtp.PlainAuth("", user, pass, host)
}

type loginAuth struct{ user, pass string }

func (a loginAuth) Start(s *smtp.ServerInfo) (string, []byte, error) {
	if !s.TLS {
		return "", nil, errors.New("unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:", "username":
		return []byte(a.user), nil
	case "password:", "password":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", from)
}

func smtpAuthError(c *smtpConfig, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unencrypted connection"):
		return errors.New("refusing to send the SMTP password without encryption — choose STARTTLS or SSL/TLS")
	case strings.HasPrefix(msg, "535"), strings.HasPrefix(msg, "534"), strings.Contains(strings.ToLower(msg), "authentication"):
		hint := ""
		if strings.Contains(c.Host, "gmail") || strings.Contains(c.Host, "google") {
			hint = " — Gmail needs an App Password (myaccount.google.com/apppasswords), not your Google password"
		}
		return fmt.Errorf("the SMTP server rejected the username or password (%s)%s", msg, hint)
	}
	return fmt.Errorf("SMTP login failed: %v", err)
}

// buildMIME is a multipart/alternative message: plain text first, HTML preferred.
func buildMIME(from, to *mail.Address, m mailMessage) []byte {
	boundary := "payload-" + randomHex(12)
	host := "payload.local"
	if _, d, ok := strings.Cut(from.Address, "@"); ok {
		host = d
	}
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+randomHex(16)+"@"+host+">")
	h("MIME-Version", "1.0")
	h("X-Mailer", "payload/"+version)
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	part := func(ctype, body string) {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&b)
		_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
		_ = qp.Close()
		b.WriteString("\r\n")
	}
	part("text/plain", m.Text)
	part("text/html", m.HTML)
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes()
}

// --- payload settings smtp ----------------------------------------------------------------------

type smtpPreset struct {
	key, label, host string
	port             int
	security, user   string
	hint             string
}

// smtpPresets are the "Other" choices; each prefills the SMTP form.
var smtpPresets = []smtpPreset{
	{"gmail", "Gmail / Google Workspace", "smtp.gmail.com", 587, "starttls", "", "Use an App Password (myaccount.google.com/apppasswords), not your Google password."},
	{"outlook", "Outlook / Microsoft 365", "smtp.office365.com", 587, "starttls", "", "Your mailbox needs SMTP AUTH turned on (Microsoft 365 admin center)."},
	{"zoho", "Zoho Mail", "smtp.zoho.com", 465, "tls", "", "Use an app-specific password if two-factor sign-in is on."},
	{"sendgrid", "SendGrid", "smtp.sendgrid.net", 587, "starttls", "apikey", `The username is literally "apikey"; the password is your API key.`},
	{"mailgun", "Mailgun", "smtp.mailgun.org", 587, "starttls", "", "Use the SMTP credentials from your Mailgun domain settings."},
	{"ses", "Amazon SES", "email-smtp.us-east-1.amazonaws.com", 587, "starttls", "", "Change the region in the host if needed; use SES SMTP credentials, not IAM keys."},
	{"custom", "Any other SMTP server", "", 587, "starttls", "", ""},
}

func smtpCmd(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "":
		smtpSettingsUI()
	case "status":
		smtpPrintStatus(mustSMTP())
	case "test":
		to := ""
		if len(args) > 1 {
			to = args[1]
		}
		smtpSendTest(mustSMTP(), to)
	case "remove", "drop":
		c, err := loadSMTP()
		if err != nil {
			fail("%v", err)
		}
		if c == nil {
			info("No mail provider is set up.")
			return
		}
		if err := os.Remove(smtpPath()); err != nil {
			fail("could not remove %s: %v", smtpPath(), err)
		}
		success("Mail provider removed %s", styleMuted.Render("("+c.summary()+")"))
	default:
		fail("unknown smtp command %q — use: payload settings smtp [status | test [email] | remove]", sub)
	}
}

func mustSMTP() *smtpConfig {
	c, err := loadSMTP()
	if err != nil {
		fail("%v", err)
	}
	if c == nil {
		fail("no mail provider is set up — run: payload settings smtp")
	}
	return c
}

func smtpPrintStatus(c *smtpConfig) {
	row := func(label, value string) { fmt.Printf("  %-9s %s\n", styleMuted.Render(label), value) }
	secret := "password"
	if c.Provider == providerMailtrap {
		row("provider", styleTitle.Render("Mailtrap")+styleMuted.Render(" · "+c.summary()))
		secret = "token"
	} else {
		row("server", styleTitle.Render(c.Host)+styleMuted.Render(fmt.Sprintf(":%d · %s", c.Port, c.securityLabel())))
		if c.Username != "" {
			row("user", c.Username)
		}
	}
	from := c.FromEmail
	if c.FromName != "" {
		from = c.FromName + " <" + c.FromEmail + ">"
	}
	row("from", from)
	if c.Password != "" {
		row(secret, fmt.Sprintf("encrypted (%s) in %s", c.Scheme, smtpPath()))
	}
}

func smtpSettingsUI() {
	if !interactive {
		fail("payload settings smtp needs a terminal — or use: payload settings smtp status | test | remove")
	}
	c, err := loadSMTP()
	if err != nil {
		warn("%v", err)
		c = nil
	}
	fmt.Println(styleBadge.Render("mail settings") + "  " + styleMuted.Render("used to email you a reset code if you forget the master password"))
	if c == nil {
		fmt.Println(styleMuted.Render("  No mail provider is set up."))
		smtpConfigure(nil)
		return
	}
	smtpPrintStatus(c)
	choice := ""
	if huh.NewSelect[string]().Title("Mail provider").
		Options(huh.NewOption("Edit the settings", "edit"),
			huh.NewOption("Send a test email", "test"),
			huh.NewOption("Switch to another provider", "new"),
			huh.NewOption("Remove the mail provider", "remove"),
			huh.NewOption("Cancel", "")).
		Height(selectHeight(5, "")).Value(&choice).WithTheme(theme()).Run() != nil || choice == "" {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return
	}
	switch choice {
	case "edit":
		smtpConfigure(c)
	case "new":
		smtpConfigure(nil)
	case "test":
		smtpSendTest(c, "")
	case "remove":
		if !confirmTUI("Remove the mail provider?", "payload won't be able to email you a master password reset code until you set one up again.", "Remove") {
			fmt.Println(styleMuted.Render("Nothing changed."))
			return
		}
		if err := os.Remove(smtpPath()); err != nil {
			fail("could not remove %s: %v", smtpPath(), err)
		}
		success("Mail provider removed")
	}
}

// smtpConfigure asks which provider to use — Mailtrap (the default) or another SMTP server — and
// its settings (prefilled from old when editing), saves them and offers a test email. It reports
// whether settings were saved.
func smtpConfigure(old *smtpConfig) bool {
	kind := providerMailtrap
	if old != nil {
		kind = old.Provider
	} else if huh.NewSelect[string]().Title("How should payload send email?").
		Options(huh.NewOption("Mailtrap — API token (recommended)", providerMailtrap),
			huh.NewOption("Other — Gmail, Outlook, SendGrid… or any SMTP server", providerSMTP)).
		Value(&kind).WithTheme(theme()).Run() != nil {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return false
	}
	if kind == providerMailtrap {
		return mailtrapConfigure(old)
	}
	return smtpServerConfigure(old)
}

var errEmailFormat = errors.New("enter an email address, like you@example.com")

func validEmail(v string) error {
	if a, err := mail.ParseAddress(strings.TrimSpace(v)); err != nil || a.Name != "" {
		return errEmailFormat
	}
	return nil
}

// mailtrapConfigure asks for the Mailtrap API token and sender.
func mailtrapConfigure(old *smtpConfig) bool {
	c := smtpConfig{Provider: providerMailtrap, FromName: "payload"}
	if old != nil && old.Provider == providerMailtrap {
		c = *old
	}
	var token string
	tokenDesc := "Mailtrap → Settings → API Tokens (with access to your sending domain). Stored encrypted on this computer."
	if c.Password != "" {
		tokenDesc = "Leave empty to keep the saved token."
	}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Mailtrap API token").Description(tokenDesc).EchoMode(huh.EchoModePassword).
				Value(&token).Validate(func(v string) error {
				if strings.TrimSpace(v) == "" && c.Password == "" {
					return errors.New("the API token is required")
				}
				return nil
			}),
			huh.NewInput().Title("Sandbox inbox ID (optional)").
				Description("Leave empty to deliver for real. With an inbox ID the email is caught in that Mailtrap sandbox inbox instead.").
				Value(&c.InboxID).Validate(func(v string) error {
				if v = strings.TrimSpace(v); v != "" {
					if _, err := strconv.ParseUint(v, 10, 64); err != nil {
						return errors.New("the inbox ID is a number (it's in the inbox's URL)")
					}
				}
				return nil
			}),
		).Title("Mailtrap"),
		huh.NewGroup(
			huh.NewInput().Title("From address").
				Description("Must be on a domain verified in Mailtrap (Sending Domains).").
				Placeholder("no-reply@yourdomain.com").Value(&c.FromEmail).Validate(validEmail),
			huh.NewInput().Title("From name").Description("Shown as the sender.").Value(&c.FromName),
		).Title("Sender"),
	).WithTheme(theme()).WithShowHelp(true).Run()
	if err != nil {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return false
	}
	c.InboxID = strings.TrimSpace(c.InboxID)
	c.FromEmail = strings.TrimSpace(c.FromEmail)
	c.FromName = strings.TrimSpace(c.FromName)
	c.Host, c.Port, c.Security, c.Username = "", 0, "", ""
	if t := strings.TrimSpace(token); t != "" {
		if err := c.setPassword(t); err != nil {
			fail("%v", err)
		}
	}
	if err := saveSMTP(&c); err != nil {
		fail("could not save %s: %v", smtpPath(), err)
	}
	success("Mailtrap saved %s", styleMuted.Render("("+c.summary()+") in "+smtpPath()))
	offerTestMail(&c)
	return true
}

// smtpServerConfigure asks for an SMTP server's settings, starting from a preset for a new one.
func smtpServerConfigure(old *smtpConfig) bool {
	c := smtpConfig{Provider: providerSMTP, Port: 587, Security: "starttls", FromName: "payload"}
	hint := ""
	if old != nil && old.Provider == providerSMTP {
		c = *old
	} else {
		presetKey := "gmail"
		opts := make([]huh.Option[string], 0, len(smtpPresets))
		for _, p := range smtpPresets {
			opts = append(opts, huh.NewOption(p.label, p.key))
		}
		if huh.NewSelect[string]().Title("Which email provider?").
			Description("Prefills the server settings — you can change them next.").
			Options(opts...).Height(selectHeight(len(opts), "")).Value(&presetKey).
			WithTheme(theme()).Run() != nil {
			fmt.Println(styleMuted.Render("Nothing changed."))
			return false
		}
		for _, p := range smtpPresets {
			if p.key == presetKey {
				c.Host, c.Port, c.Security, c.Username, hint = p.host, p.port, p.security, p.user, p.hint
			}
		}
	}

	port := strconv.Itoa(c.Port)
	var pw string
	pwDesc := "App password or API key. Stored encrypted on this computer."
	if hint != "" {
		pwDesc = hint + " Stored encrypted on this computer."
	}
	if c.Password != "" {
		pwDesc = "Leave empty to keep the saved password."
	}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("SMTP server").Placeholder("smtp.example.com").Value(&c.Host).Validate(func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errors.New("the server is required")
				}
				return nil
			}),
			huh.NewInput().Title("Port").Description("587 for STARTTLS · 465 for SSL/TLS").Value(&port).Validate(func(v string) error {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n < 1 || n > 65535 {
					return errors.New("enter a port number, like 587")
				}
				return nil
			}),
			huh.NewSelect[string]().Title("Encryption").
				Options(huh.NewOption("STARTTLS (usually port 587)", "starttls"),
					huh.NewOption("SSL/TLS (usually port 465)", "tls"),
					huh.NewOption("None — local relay only, no login", "none")).
				Value(&c.Security),
		).Title("Server"),
		huh.NewGroup(
			huh.NewInput().Title("Username").Description("Usually your full email address. Leave empty if the server needs no login.").Value(&c.Username),
			huh.NewInput().Title("Password").Description(pwDesc).EchoMode(huh.EchoModePassword).Value(&pw),
		).Title("Login"),
		huh.NewGroup(
			huh.NewInput().Title("From address").Description("The address the email comes from — most providers require your own.").
				Placeholder("you@example.com").Value(&c.FromEmail).Validate(validEmail),
			huh.NewInput().Title("From name").Description("Shown as the sender.").Value(&c.FromName),
		).Title("Sender"),
	).WithTheme(theme()).WithShowHelp(true).Run()
	if err != nil {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return false
	}

	c.Host = strings.TrimSpace(c.Host)
	c.Port, _ = strconv.Atoi(strings.TrimSpace(port))
	c.Username = strings.TrimSpace(c.Username)
	c.FromEmail = strings.TrimSpace(c.FromEmail)
	c.FromName = strings.TrimSpace(c.FromName)
	c.InboxID = ""
	if c.Security == "none" && c.Username != "" {
		warn("Without encryption payload won't send a username and password — the login is only used with STARTTLS or SSL/TLS")
	}
	switch {
	case pw != "":
		if err := c.setPassword(pw); err != nil {
			fail("%v", err)
		}
	case c.Username == "":
		c.Password, c.Scheme = "", ""
	}
	if err := saveSMTP(&c); err != nil {
		fail("could not save %s: %v", smtpPath(), err)
	}
	success("SMTP provider saved %s", styleMuted.Render("("+c.summary()+") in "+smtpPath()))
	offerTestMail(&c)
	return true
}

func offerTestMail(c *smtpConfig) {
	send := true
	if huh.NewConfirm().Title("Send a test email now?").Affirmative("Send").Negative("Skip").
		Value(&send).WithTheme(theme()).Run() == nil && send {
		smtpSendTest(c, "")
	}
}

// smtpSendTest sends the test email to to, or to the payload auth email (asking when there is none).
func smtpSendTest(c *smtpConfig, to string) {
	if to == "" {
		if a, err := loadAuth(); err == nil {
			to = a.Email
		}
	}
	if to == "" {
		if !interactive {
			fail("give an address: payload settings smtp test you@example.com")
		}
		to = c.FromEmail
		if huh.NewInput().Title("Send the test email to").Value(&to).Validate(validEmail).WithTheme(theme()).Run() != nil {
			return
		}
	}
	fmt.Println(styleMuted.Render("  Sending a test email to " + to + " via " + c.summary() + "…"))
	if err := c.send(testMail(to, c)); err != nil {
		fail("%v", err)
	}
	success("Test email sent to %s — check your inbox (and spam folder)", styleTitle.Render(to))
}
