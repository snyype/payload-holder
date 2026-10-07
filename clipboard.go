package main

import (
	"fmt"
	"runtime"

	"github.com/atotto/clipboard"
)

// holdArg runs payload as the background clipboard holder (Linux only; see clipboard_linux.go).
const holdArg = "__hold-clipboard"

// copyText puts text on the system clipboard. On Linux without xclip, xsel or wl-copy installed,
// it falls back to payload's own built-in clipboard holder.
func copyText(text string) error {
	err := clipboard.WriteAll(text)
	if err == nil || runtime.GOOS != "linux" {
		return err
	}
	if herr := holdInBackground(text); herr != nil {
		return fmt.Errorf("no clipboard available (%v) — install wl-clipboard (Wayland) or xclip (X11)", herr)
	}
	return nil
}
