package main

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
)

// payload ssh forgot: resets a forgotten master password with a one-time code emailed to the address
// on the payload auth login, through the mail provider from payload settings smtp.
//
// The master password is only kept as a hash, so it can't be sent — the code lets you choose a new
// one instead. The code lives only in this process's memory: it works once, for resetCodeValid, and
// for resetCodeTries wrong guesses. Resetting changes nothing else: the env vault and logins aren't
// encrypted with the master password.

const (
	resetCodeValid = 15 * time.Minute
	resetCodeTries = 5
)

// newResetCode is six random digits.
func newResetCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		fail("no randomness available: %v", err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// normalizeCode drops the spaces and dashes people type or paste with a code.
func normalizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
}

// resetCodeCheck returns a validator for the code form: right code within the time and tries left.
func resetCodeCheck(code string, expires time.Time) func(string) error {
	tries, last, ok := 0, "", ""
	return func(v string) error {
		v = normalizeCode(v)
		if v != "" && v == ok {
			return nil // already accepted (huh validates again when the field loses focus)
		}
		switch {
		case time.Now().After(expires):
			return errors.New("the code has expired — press Ctrl+C and run payload ssh forgot for a new one")
		case tries >= resetCodeTries:
			return errors.New("too many wrong codes — press Ctrl+C and run payload ssh forgot for a new one")
		case v == "":
			return errors.New("enter the 6-digit code from the email")
		}
		if v != last { // count each different guess once
			last = v
			tries++
		}
		if subtle.ConstantTimeCompare([]byte(v), []byte(code)) != 1 {
			if left := resetCodeTries - tries; left > 0 {
				return fmt.Errorf("wrong code — %d tries left", left)
			}
			return errors.New("too many wrong codes — press Ctrl+C and run payload ssh forgot for a new one")
		}
		ok = v
		return nil
	}
}

// resetEmail is the address the code goes to: the email on the payload auth login.
func resetEmail() (string, error) {
	a, err := loadAuth()
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("not logged in")
	}
	if err != nil {
		return "", err
	}
	if a.Email == "" {
		return "", errors.New("no email on the login")
	}
	return a.Email, nil
}

func sshForgotCmd() {
	if !interactive {
		fail("payload ssh forgot needs a terminal")
	}
	l, err := loadSSHLock()
	if err != nil {
		fail("%v", err)
	}
	if l == nil {
		info("No master password is set — payload ssh and the env vault are already open.")
		return
	}
	fmt.Println(styleSSHBadge.Render("forgot master password"))

	// 1. Where to send it: the email from payload auth login.
	to, err := resetEmail()
	if err != nil {
		switch err.Error() {
		case "not logged in":
			warn("To email you a reset code, payload needs your email address from %s.", styleTitle.Render("payload auth login"))
			fmt.Println(styleMuted.Render("  Log in with GitHub first:  ") + styleCode.Render("payload auth login"))
		case "no email on the login":
			warn("Your payload login has no email address — GitHub didn't share a verified primary email.")
			fmt.Println(styleMuted.Render("  Add or verify one at https://github.com/settings/emails, then run ") + styleCode.Render("payload auth login"))
		default:
			warn("Could not read your payload login: %v", err)
		}
		fmt.Println(styleMuted.Render("  Then run ") + styleCode.Render("payload ssh forgot") + styleMuted.Render(" again."))
		os.Exit(1)
	}

	// 2. How to send it: the mail provider from payload settings smtp.
	c, err := loadSMTP()
	if err != nil {
		warn("%v", err)
		c = nil
	}
	if c == nil {
		warn("No mail provider is set up, so payload can't email you a reset code.")
		fmt.Println(styleMuted.Render("  Set one up once (Mailtrap or any SMTP server) and payload will send the code to " + to + "."))
		setup := true
		if huh.NewConfirm().Title("Set up a mail provider now?").
			Description("You can also do it later with payload settings smtp.").
			Affirmative("Set up").Negative("Not now").Value(&setup).WithTheme(theme()).Run() != nil || !setup {
			fmt.Println(styleMuted.Render("  When you want to send a reset code: ") + styleCode.Render("payload settings smtp") +
				styleMuted.Render(", then ") + styleCode.Render("payload ssh forgot"))
			return
		}
		if !smtpConfigure(nil) {
			return
		}
		if c, err = loadSMTP(); err != nil || c == nil {
			fail("the mail provider was not saved")
		}
	}

	// 3. Send the code.
	send := true
	if huh.NewConfirm().Title("Email a reset code to "+to+"?").
		Description("Sent via "+c.summary()+". The code works once, for 15 minutes.").
		Affirmative("Send code").Negative("Cancel").Value(&send).WithTheme(theme()).Run() != nil || !send {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return
	}
	code, now := newResetCode(), time.Now()
	expires := now.Add(resetCodeValid)
	fmt.Println(styleMuted.Render("  Sending…"))
	if err := c.send(resetCodeMail(to, code, clientName(), now, resetCodeValid)); err != nil {
		fail("could not send the reset code: %v\n  Check the mail settings with: payload settings smtp", err)
	}
	success("Code sent to %s %s", styleTitle.Render(to), styleMuted.Render("· check your spam folder if it's not there in a minute"))

	// 4. Check it, then choose a new master password.
	var entered string
	if huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Reset code").
			Description(fmt.Sprintf("The 6 digits from the email · expires at %s", expires.Format("15:04"))).
			Placeholder("123 456").CharLimit(12).Value(&entered).Validate(resetCodeCheck(code, expires)),
	)).WithTheme(theme()).Run() != nil {
		fmt.Println(styleMuted.Render("Nothing changed."))
		return
	}
	pw, ok := newSSHMasterPassword()
	if !ok {
		fmt.Println(styleMuted.Render("Nothing changed — the code is used up; run payload ssh forgot for a new one."))
		return
	}
	if err := setSSHMasterPassword(pw); err != nil {
		fail("could not save the master password: %v", err)
	}
	unlocked = true
	success("Master password reset %s", styleMuted.Render("· unlocked for 10 hours"))

	if err := c.send(passwordChangedMail(to, clientName(), time.Now())); err != nil {
		warn("Could not send the confirmation email: %v", err)
	}
}
