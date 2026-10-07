//go:build !linux

package main

import "errors"

func holdInBackground(string) error { return errors.New("built-in clipboard is Linux-only") }

func runClipboardHolder() {}
