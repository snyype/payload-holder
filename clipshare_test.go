package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeviceID(t *testing.T) {
	id := deviceID()
	if !validDeviceID(id) {
		t.Fatalf("device ID %q is not pd- and 16 hex characters", id)
	}
	if id != deviceID() {
		t.Fatal("device ID changed within a run")
	}
	if strings.Contains(strings.ToLower(id), strings.ToLower(strings.TrimSpace(machineID()))) && machineID() != "" {
		t.Fatal("device ID exposes the raw machine ID")
	}
	for _, bad := range []string{"", "pd-123", "pd-zzzzzzzzzzzzzzzz", "xx-0123456789abcdef", "pd-0123456789abcdef extra"} {
		if validDeviceID(bad) {
			t.Errorf("validDeviceID(%q) = true", bad)
		}
	}
}

// lockedBuffer collects a background process's output safely.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestHoldForApproval runs a real listener whose settings hold this device's text, then sends to it:
// the text must be shown but not copied, the sender told it is held, and notices shown.
func TestHoldForApproval(t *testing.T) {
	clipLoopbackOnly = true // only ever talk to the listener started here
	if ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", clipPort)); err != nil {
		t.Skipf("port %d is busy (a listener is running?): %v", clipPort, err)
	} else {
		ln.Close()
	}
	listenStore, sendStore := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(listenStore, "customization", "settings.json"),
		fmt.Sprintf(`{"name":"receiver","clipboard_clients":{%q:{"name":"sender","hold":true}}}`, deviceID()))
	writeFile(t, filepath.Join(sendStore, "customization", "settings.json"), `{"name":"sender"}`)

	listener := exec.Command(os.Args[0])
	listener.Env = append(os.Environ(), "PAYLOAD_TEST_MAIN=1", "PAYLOAD_STORE="+listenStore,
		"PAYLOAD_TEST_ARGS=clipboard listen", "PAYLOAD_NO_UPDATE_CHECK=1", "NO_COLOR=1")
	var out lockedBuffer
	listener.Stdout, listener.Stderr = &out, &out
	if err := listener.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Process.Kill(); _ = listener.Wait() }()
	waitFor(t, &out, "listening as receiver")

	sent, code := runPayload(t, "", sendStore, "clipboard", "secret-ish", "text")
	if code != 0 || !strings.Contains(sent, "receiver flagged you risky") || !strings.Contains(sent, "1 held for approval") {
		t.Fatalf("send to a holding listener (%d):\n%s", code, sent)
	}
	waitFor(t, &out, "Held text from sender")
	waitFor(t, &out, "│ secret-ish text")
	if strings.Contains(out.String(), "Copied text") {
		t.Fatalf("held text was copied:\n%s", out.String())
	}

	if err := clipNotify("127.0.0.1", noticeHold); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &out, "flagged you risky — text you send them is on hold")
	if err := clipNotify("127.0.0.1", noticeRelease); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &out, "removed the hold")

	// A flag set while the client isn't listening waits, then goes out when payload sees it.
	t.Setenv("PAYLOAD_STORE", sendStore)
	if notifyClient("pd-0000000000000001", "", noticeHold) {
		t.Fatal("notice reported delivered with no address")
	}
	if loadNotices()["pd-0000000000000001"] != noticeHold {
		t.Fatal("undelivered notice was not queued")
	}
	deliverNotices([]clipPeer{{ID: "pd-0000000000000001", IP: "127.0.0.1", Name: "receiver"}})
	if len(loadNotices()) != 0 {
		t.Fatal("queued notice was not delivered")
	}
	if strings.Count(out.String(), "flagged you risky — text you send them") != 2 {
		t.Fatalf("queued notice not shown:\n%s", out.String())
	}

	// The toggle is saved in settings.json, keeping the rest of the file.
	if err := saveClientPref("pd-0000000000000002", clientPref{Name: "x", Hold: true}); err != nil {
		t.Fatal(err)
	}
	if !isHeld("pd-0000000000000002") || savedClientName() != "sender" {
		t.Fatal("hold not saved, or settings lost")
	}
	if err := saveClientPref("pd-0000000000000002", clientPref{}); err != nil || isHeld("pd-0000000000000002") {
		t.Fatal("hold not removed")
	}
}

func waitFor(t *testing.T, out *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("listener never printed %q:\n%s", want, out.String())
}
