//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// On Linux the clipboard is not storage: the program that copied owns it and hands the text to
// whoever pastes, so the text vanishes when that program exits. xclip solves this by leaving a
// background process running, and so does payload: holdInBackground starts a detached copy of
// payload that owns the X11 CLIPBOARD selection until another program copies something.
// On Ubuntu's Wayland desktop this goes through Xwayland, which keeps both clipboards in sync.

// holdInBackground starts the holder, gives it the text, and waits until it owns the clipboard.
func holdInBackground(text string) error {
	if os.Getenv("DISPLAY") == "" {
		return errors.New("no X display: DISPLAY is not set")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, holdArg)
	cmd.Stdin = strings.NewReader(text)
	// Own session: no controlling terminal, so closing the terminal or Ctrl+C does not kill it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	// The holder prints "ok" once it owns the clipboard (or an error and exits), then keeps running.
	status := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		status <- strings.TrimSpace(line)
	}()
	select {
	case s := <-status:
		if s == "ok" {
			return cmd.Process.Release()
		}
		_ = cmd.Wait()
		if s == "" {
			s = "clipboard holder exited"
		}
		return errors.New(s)
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("timed out waiting for the X server")
	}
}

// runClipboardHolder is the background process: read the text, take the clipboard, then answer
// paste requests until another program takes the clipboard over.
func runClipboardHolder() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	h, err := newHolder(data)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("ok")
	os.Stdout.Close()
	h.serve()
}

type transferKey struct {
	window xproto.Window
	prop   xproto.Atom
}

// transfer is an INCR (chunked) send in progress, for text larger than one X request.
type transfer struct {
	typ    xproto.Atom
	offset int
}

type holder struct {
	conn      *xgb.Conn
	win       xproto.Window
	time      xproto.Timestamp
	data      []byte
	maxChunk  int
	transfers map[transferKey]*transfer

	clipboard, targets, timestamp, incr, utf8, text, textPlain, textPlainUTF8 xproto.Atom
}

func newHolder(data []byte) (*holder, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to the X display: %v", err)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	h := &holder{
		conn:      conn,
		data:      data,
		maxChunk:  int(setup.MaximumRequestLength)*4 - 64, // room for the ChangeProperty header
		transfers: map[transferKey]*transfer{},
	}

	for name, atom := range map[string]*xproto.Atom{
		"CLIPBOARD":                &h.clipboard,
		"TARGETS":                  &h.targets,
		"TIMESTAMP":                &h.timestamp,
		"INCR":                     &h.incr,
		"UTF8_STRING":              &h.utf8,
		"TEXT":                     &h.text,
		"text/plain":               &h.textPlain,
		"text/plain;charset=utf-8": &h.textPlainUTF8,
	} {
		r, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			return nil, err
		}
		*atom = r.Atom
	}

	if h.win, err = xproto.NewWindowId(conn); err != nil {
		return nil, err
	}
	if err := xproto.CreateWindowChecked(conn, screen.RootDepth, h.win, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		return nil, err
	}

	// Selection ownership needs a real server timestamp: touch a property on our own window
	// and take the time from the PropertyNotify it causes.
	xproto.ChangeProperty(conn, xproto.PropModeAppend, h.win, h.timestamp, xproto.AtomString, 8, 0, nil)
	for {
		ev, xerr := conn.WaitForEvent()
		if ev == nil && xerr == nil {
			return nil, errors.New("X connection closed")
		}
		if e, ok := ev.(xproto.PropertyNotifyEvent); ok && e.Window == h.win {
			h.time = e.Time
			break
		}
	}

	xproto.SetSelectionOwner(conn, h.win, h.clipboard, h.time)
	owner, err := xproto.GetSelectionOwner(conn, h.clipboard).Reply()
	if err != nil {
		return nil, err
	}
	if owner.Owner != h.win {
		return nil, errors.New("could not take ownership of the clipboard")
	}
	return h, nil
}

func (h *holder) serve() {
	for {
		ev, xerr := h.conn.WaitForEvent()
		if ev == nil && xerr == nil {
			return // X connection closed (session ended)
		}
		switch e := ev.(type) {
		case xproto.SelectionRequestEvent:
			h.answer(e)
		case xproto.PropertyNotifyEvent:
			h.sendNextChunk(e)
		case xproto.SelectionClearEvent:
			if e.Selection == h.clipboard {
				return // someone else copied something: our job is done
			}
		}
	}
}

// answer replies to one paste request: the list of formats we offer, or the text itself.
func (h *holder) answer(e xproto.SelectionRequestEvent) {
	prop := e.Property
	if prop == xproto.AtomNone {
		prop = e.Target // obsolete clients leave the property empty
	}

	switch e.Target {
	case h.targets:
		atoms := []xproto.Atom{h.targets, h.timestamp, h.utf8, h.textPlainUTF8, h.textPlain, xproto.AtomString, h.text}
		buf := make([]byte, 4*len(atoms))
		for i, a := range atoms {
			xgb.Put32(buf[i*4:], uint32(a))
		}
		xproto.ChangeProperty(h.conn, xproto.PropModeReplace, e.Requestor, prop, xproto.AtomAtom, 32, uint32(len(atoms)), buf)
	case h.timestamp:
		buf := make([]byte, 4)
		xgb.Put32(buf, uint32(h.time))
		xproto.ChangeProperty(h.conn, xproto.PropModeReplace, e.Requestor, prop, xproto.AtomInteger, 32, 1, buf)
	case h.utf8, h.textPlainUTF8, h.textPlain, xproto.AtomString, h.text:
		typ := e.Target
		if typ == h.text {
			typ = h.utf8
		}
		h.sendText(e.Requestor, prop, typ)
	default:
		prop = xproto.AtomNone // format we do not offer
	}

	notify := xproto.SelectionNotifyEvent{
		Time:      e.Time,
		Requestor: e.Requestor,
		Selection: e.Selection,
		Target:    e.Target,
		Property:  prop,
	}
	xproto.SendEvent(h.conn, false, e.Requestor, 0, string(notify.Bytes()))
}

// sendText writes the text into the requestor's property, in one go or, when it is too big for
// one X request, by the INCR protocol: announce the size, then send a chunk each time the
// requestor deletes the property, ending with an empty chunk.
func (h *holder) sendText(win xproto.Window, prop, typ xproto.Atom) {
	if len(h.data) <= h.maxChunk {
		xproto.ChangeProperty(h.conn, xproto.PropModeReplace, win, prop, typ, 8, uint32(len(h.data)), h.data)
		return
	}
	xproto.ChangeWindowAttributes(h.conn, win, xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange})
	size := make([]byte, 4)
	xgb.Put32(size, uint32(len(h.data)))
	xproto.ChangeProperty(h.conn, xproto.PropModeReplace, win, prop, h.incr, 32, 1, size)
	h.transfers[transferKey{win, prop}] = &transfer{typ: typ}
}

func (h *holder) sendNextChunk(e xproto.PropertyNotifyEvent) {
	if e.State != xproto.PropertyDelete {
		return
	}
	key := transferKey{e.Window, e.Atom}
	t, ok := h.transfers[key]
	if !ok {
		return
	}
	chunk := h.data[t.offset:min(t.offset+h.maxChunk, len(h.data))]
	xproto.ChangeProperty(h.conn, xproto.PropModeReplace, e.Window, e.Atom, t.typ, 8, uint32(len(chunk)), chunk)
	t.offset += len(chunk)
	if len(chunk) == 0 {
		delete(h.transfers, key)
		xproto.ChangeWindowAttributes(h.conn, e.Window, xproto.CwEventMask, []uint32{0})
	}
}
