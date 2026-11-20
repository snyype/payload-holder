package main

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// HTML emails payload sends. Email clients ignore <style> blocks and modern CSS unevenly, so the
// layout is nested tables with inline styles, 560px wide, and every message also has a plain-text
// part (buildMIME / Mailtrap's "text").

type mailLayout struct {
	Preheader string      // the preview line inbox lists show after the subject
	Eyebrow   string      // small label above the heading
	Heading   string      // plain text
	Intro     string      // plain text
	Block     string      // raw HTML placed under the intro (already escaped)
	Details   [][2]string // label/value rows, plain text
	Note      string      // plain text, in a soft callout
	NoteTone  string      // "warn" or "" (neutral)
}

const (
	mailFont = `-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif`
	mailMono = `'SF Mono',SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace`
	mailInk  = "#0B1220"
	mailBody = "#475569"
	mailSoft = "#94A3B8"
	mailLine = "#E2E8F0"
	mailBg   = "#F1F5F9"
	mailMint = "#10B981"
)

func renderMail(l mailLayout) string {
	e := html.EscapeString
	var b strings.Builder
	w := func(s string, a ...any) { fmt.Fprintf(&b, s, a...) }

	w(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	w(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	w(`<meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light">`)
	w(`<title>%s</title></head>`, e(l.Heading))
	w(`<body style="margin:0;padding:0;background:%s;-webkit-font-smoothing:antialiased;">`, mailBg)
	// Preheader: shown in the inbox list, hidden in the message.
	w(`<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;mso-hide:all;">%s%s</div>`,
		e(l.Preheader), strings.Repeat("&#8199;&#847; ", 40))

	w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:%s;">`, mailBg)
	w(`<tr><td align="center" style="padding:40px 16px;">`)
	w(`<table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:560px;">`)

	// Brand row.
	w(`<tr><td style="padding:0 4px 20px 4px;">`)
	w(`<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>`)
	w(`<td width="28" height="28" align="center" valign="middle" bgcolor="%s" style="width:28px;height:28px;background:%s;border-radius:8px;font:700 15px/28px %s;color:#052E1F;">&#8250;</td>`, mailMint, mailMint, mailMono)
	w(`<td style="padding-left:10px;font:700 17px/28px %s;color:%s;letter-spacing:-0.2px;">payload</td>`, mailFont, mailInk)
	w(`</tr></table></td></tr>`)

	// Card.
	w(`<tr><td bgcolor="#FFFFFF" style="background:#FFFFFF;border:1px solid %s;border-radius:16px;overflow:hidden;">`, mailLine)
	w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0">`)
	w(`<tr><td height="4" bgcolor="%s" style="height:4px;line-height:4px;font-size:0;background:%s;background-image:linear-gradient(90deg,#059669,#34D399,#67E8F9);border-radius:16px 16px 0 0;">&nbsp;</td></tr>`, mailMint, mailMint)
	w(`<tr><td style="padding:40px 44px 8px 44px;">`)
	if l.Eyebrow != "" {
		w(`<div style="font:600 12px/16px %s;letter-spacing:1.4px;text-transform:uppercase;color:%s;margin-bottom:12px;">%s</div>`, mailFont, mailMint, e(l.Eyebrow))
	}
	w(`<h1 style="margin:0 0 14px 0;font:700 26px/34px %s;color:%s;letter-spacing:-0.4px;">%s</h1>`, mailFont, mailInk, e(l.Heading))
	w(`<p style="margin:0;font:400 15px/24px %s;color:%s;">%s</p>`, mailFont, mailBody, e(l.Intro))
	w(`</td></tr>`)

	if l.Block != "" {
		w(`<tr><td style="padding:28px 44px 4px 44px;">%s</td></tr>`, l.Block)
	}

	if len(l.Details) > 0 {
		w(`<tr><td style="padding:28px 44px 0 44px;">`)
		w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="border-top:1px solid %s;">`, mailLine)
		for _, d := range l.Details {
			w(`<tr><td style="padding:12px 0;border-bottom:1px solid %s;font:500 13px/20px %s;color:%s;width:38%%;">%s</td>`, mailLine, mailFont, mailSoft, e(d[0]))
			w(`<td align="right" style="padding:12px 0;border-bottom:1px solid %s;font:600 13px/20px %s;color:%s;">%s</td></tr>`, mailLine, mailFont, mailInk, e(d[1]))
		}
		w(`</table></td></tr>`)
	}

	if l.Note != "" {
		bg, border, ink := "#F8FAFC", mailLine, mailBody
		if l.NoteTone == "warn" {
			bg, border, ink = "#FFFBEB", "#FDE68A", "#92400E"
		}
		w(`<tr><td style="padding:28px 44px 0 44px;">`)
		w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0"><tr>`)
		w(`<td bgcolor="%s" style="background:%s;border:1px solid %s;border-radius:12px;padding:14px 16px;font:400 13px/20px %s;color:%s;">%s</td>`, bg, bg, border, mailFont, ink, e(l.Note))
		w(`</tr></table></td></tr>`)
	}

	w(`<tr><td style="padding:36px 44px 40px 44px;font:400 13px/20px %s;color:%s;">— payload</td></tr>`, mailFont, mailSoft)
	w(`</table></td></tr>`)

	// Footer.
	w(`<tr><td align="center" style="padding:24px 24px 0 24px;font:400 12px/18px %s;color:%s;">`, mailFont, mailSoft)
	w(`Sent by the payload CLI on your computer. You're receiving this because this address is linked to your payload login.`)
	w(`</td></tr>`)
	w(`</table></td></tr></table></body></html>`)
	return b.String()
}

// codeBlock is the big one-time code panel.
func codeBlock(code, caption string) string {
	return fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0"><tr>`+
		`<td align="center" bgcolor="%s" style="background:%s;border-radius:14px;padding:30px 16px 26px 16px;">`+
		`<div style="font:700 38px/44px %s;letter-spacing:12px;color:#34D399;padding-left:12px;">%s</div>`+
		`<div style="margin-top:12px;font:500 12px/16px %s;letter-spacing:0.4px;color:#94A3B8;">%s</div>`+
		`</td></tr></table>`,
		mailInk, mailInk, mailMono, html.EscapeString(code), mailFont, html.EscapeString(caption))
}

// spacedCode is "482913" as "482 913" for reading.
func spacedCode(code string) string {
	if len(code) == 6 {
		return code[:3] + " " + code[3:]
	}
	return code
}

func mailStamp(t time.Time) string { return t.Format("Mon 2 Jan 2006, 15:04 MST") }

func resetCodeMail(to, code, computer string, at time.Time, valid time.Duration) mailMessage {
	mins := int(valid.Minutes())
	l := mailLayout{
		Preheader: fmt.Sprintf("Your payload reset code is %s. It expires in %d minutes.", spacedCode(code), mins),
		Eyebrow:   "Master password reset",
		Heading:   "Your reset code",
		Intro:     "Someone — hopefully you — asked to reset the payload master password. Enter this code in your terminal to choose a new one.",
		Block:     codeBlock(spacedCode(code), fmt.Sprintf("Expires in %d minutes · works once", mins)),
		Details:   [][2]string{{"Computer", computer}, {"Requested", mailStamp(at)}, {"Sent to", to}},
		Note:      "Didn't ask for this? You can ignore this email — your master password stays the same. Never share this code; payload will never ask you for it outside your own terminal.",
		NoteTone:  "warn",
	}
	text := fmt.Sprintf(`payload — master password reset

Your reset code: %s

Enter it in your terminal (payload ssh forgot) to choose a new master password.
It expires in %d minutes and works once.

Computer:  %s
Requested: %s

Didn't ask for this? Ignore this email — your master password stays the same.
`, spacedCode(code), mins, computer, mailStamp(at))
	return mailMessage{To: to, Subject: "Your payload reset code: " + spacedCode(code), Text: text, HTML: renderMail(l)}
}

func passwordChangedMail(to, computer string, at time.Time) mailMessage {
	l := mailLayout{
		Preheader: "The payload master password was just reset with an emailed code.",
		Eyebrow:   "Security notice",
		Heading:   "Master password changed",
		Intro:     "The payload master password was reset with a code sent to this address. payload ssh and the env vault are unlocked on that computer for the next 10 hours.",
		Details:   [][2]string{{"Computer", computer}, {"Changed", mailStamp(at)}},
		Note:      "If this wasn't you, someone has access to that computer and to this mailbox. Change your email password, then run payload ssh settings to set a new master password.",
		NoteTone:  "warn",
	}
	text := fmt.Sprintf(`payload — master password changed

The payload master password was reset with a code sent to this address.

Computer: %s
Changed:  %s

If this wasn't you, change your email password, then run payload ssh settings to set a new master password.
`, computer, mailStamp(at))
	return mailMessage{To: to, Subject: "Your payload master password was changed", Text: text, HTML: renderMail(l)}
}

func testMail(to string, c *smtpConfig) mailMessage {
	now := time.Now()
	l := mailLayout{
		Preheader: "Your mail provider is set up — payload can email you a reset code.",
		Eyebrow:   "Mail settings",
		Heading:   "Email is working",
		Intro:     "payload can now email you a reset code if you ever forget your master password. Nothing else to do.",
		Details:   [][2]string{{"Provider", c.summary()}, {"From", c.FromEmail}, {"Computer", clientName()}, {"Sent", mailStamp(now)}},
		Note:      "Forgot your master password later? Run payload ssh forgot — the code goes to the email on your payload auth login.",
	}
	text := fmt.Sprintf(`payload — email is working

payload can now email you a reset code if you forget your master password.

Provider: %s
From:     %s
Computer: %s
Sent:     %s
`, c.summary(), c.FromEmail, clientName(), mailStamp(now))
	return mailMessage{To: to, Subject: "payload: email is working", Text: text, HTML: renderMail(l)}
}
