package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Per-client clipboard settings, keyed by device ID, in settings.json under "clipboard_clients":
//
//	"clipboard_clients": {"pd-3f9a0c41d27be610": {"name": "snype", "hold": true}}
//
// hold: text from that client is shown in "payload clipboard listen" but not copied. They are told
// with a notice; notices that can't be delivered yet wait in .cache/clipboard-notices.json and go
// out the next time payload sees them listening.

type clientPref struct {
	Name string `json:"name,omitempty"` // last name seen, for the settings screen when they're offline
	Hold bool   `json:"hold,omitempty"`
}

// Notice kinds sent to a client when its incoming clipboard is put on hold or released.
const (
	noticeHold    = "hold"
	noticeRelease = "release"
)

var prefsMu sync.Mutex

// loadClientPrefs reads the per-client settings fresh, so a running listener sees changes at once.
func loadClientPrefs() map[string]clientPref {
	prefs := map[string]clientPref{}
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return prefs
	}
	var f struct {
		Clients map[string]clientPref `json:"clipboard_clients"`
	}
	if json.Unmarshal(data, &f) == nil && f.Clients != nil {
		prefs = f.Clients
	}
	return prefs
}

func isHeld(id string) bool {
	return id != "" && loadClientPrefs()[id].Hold
}

// saveClientPref stores one client's settings, keeping everything else in settings.json.
func saveClientPref(id string, p clientPref) error {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	if _, err := writeDefaultSettings(); err != nil {
		return err
	}
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return err
	}
	var f map[string]json.RawMessage
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	prefs := map[string]clientPref{}
	if raw, ok := f["clipboard_clients"]; ok {
		_ = json.Unmarshal(raw, &prefs)
	}
	if p == (clientPref{}) {
		delete(prefs, id)
	} else {
		prefs[id] = p
	}
	if len(prefs) == 0 {
		delete(f, "clipboard_clients")
	} else {
		f["clipboard_clients"], _ = json.Marshal(prefs)
	}
	return os.WriteFile(settingsPath(), encodeSettings(f), 0o644)
}

// --- notices waiting for a client to be listening ---------------------------------------------

func noticesPath() string {
	return filepath.Join(storeDir(), ".cache", "clipboard-notices.json")
}

func loadNotices() map[string]string {
	n := map[string]string{}
	if data, err := os.ReadFile(noticesPath()); err == nil {
		_ = json.Unmarshal(data, &n)
	}
	return n
}

func saveNotices(n map[string]string) {
	if len(n) == 0 {
		_ = os.Remove(noticesPath())
		return
	}
	if os.MkdirAll(filepath.Dir(noticesPath()), 0o755) != nil {
		return
	}
	data, _ := json.Marshal(n)
	_ = os.WriteFile(noticesPath(), data, 0o644)
}

// notifyClient tells a client its incoming clipboard was put on hold or released: straight away if
// it is listening (ip known), otherwise the next time payload sees it. It reports whether it was
// delivered now.
func notifyClient(id, ip, kind string) bool {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	pending := loadNotices()
	delete(pending, id) // a newer toggle replaces an undelivered one
	delivered := ip != "" && clipNotify(ip, kind) == nil
	if !delivered {
		pending[id] = kind
	}
	saveNotices(pending)
	return delivered
}

// deliverNotices sends waiting notices to the clients among peers.
func deliverNotices(peers []clipPeer) {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	pending := loadNotices()
	if len(pending) == 0 {
		return
	}
	changed := false
	for _, p := range peers {
		if kind, ok := pending[p.ID]; ok && p.ID != "" && clipNotify(p.IP, kind) == nil {
			delete(pending, p.ID)
			changed = true
		}
	}
	if changed {
		saveNotices(pending)
	}
}
