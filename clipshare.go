package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/mattn/go-isatty"
)

// Shared clipboard over the local network. "payload clipboard listen" waits for text; "payload
// clipboard <text>" finds every listener with a UDP broadcast, then hands each one the text over
// TCP. Both use the same port number.
const (
	clipPort     = 47321
	clipAsk      = "payload-clipboard?1"
	clipAnswer   = "payload-clipboard!1" // "<answer> <name>" (all versions)
	clipAnswer2  = "payload-clipboard!2" // "<answer2> <device id> <name>" (v2.4.0 and later)
	clipMaxBytes = 16 << 20
	clipWait     = 800 * time.Millisecond
)

// clipHeader is the first line of a TCP transfer; the text follows it.
type clipHeader struct {
	From   string `json:"from"`
	ID     string `json:"id,omitempty"`     // the sender's device ID
	Kind   string `json:"kind,omitempty"`   // "" (text) or "notice"
	Notice string `json:"notice,omitempty"` // for a notice: hold or release
	Size   int    `json:"size"`
}

// clipLoopbackOnly keeps discovery on this computer (tests must never send to other people's listeners).
var clipLoopbackOnly bool

// clipPeer is a listener that answered the broadcast. ID is empty for versions before device IDs.
type clipPeer struct {
	Name string
	IP   string
	ID   string
}

func clipboardCmd(args []string) {
	if len(args) == 1 && args[0] == "listen" {
		clipListen()
		return
	}
	text := strings.Join(args, " ")
	source := "the arguments"
	if len(args) == 0 && !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, clipMaxBytes+1))
		if err != nil {
			fail("could not read stdin: %v", err)
		}
		text, source = strings.TrimRight(string(b), "\r\n"), "stdin"
	}
	if len(args) == 0 && text == "" {
		// Nothing typed or piped: send what is on the clipboard.
		b, err := clipboard.ReadAll()
		if err != nil {
			fail("could not read the clipboard: %v", err)
		}
		text, source = b, "your clipboard"
	}
	if text == "" {
		fail("nothing to send — usage: payload clipboard <text>  (or pipe text in, or copy something first)")
	}
	if len(text) > clipMaxBytes {
		fail("text is too large to send (%s; the limit is %s)", humanSize(len(text)), humanSize(clipMaxBytes))
	}
	clipSend(text, source)
}

// clientName is the name other computers see: "name" in settings.json, else the computer name.
// It is read from the file each time, so a running listener picks up "payload settings set-name".
func clientName() string {
	if n := savedClientName(); n != "" {
		return n
	}
	return hostName()
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// clipListen answers discovery broadcasts and puts every text it receives on the clipboard.
func clipListen() {
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: clipPort})
	if err != nil {
		fail("could not listen on UDP port %d: %v — is another \"payload clipboard listen\" running?", clipPort, err)
	}
	tcp, err := net.Listen("tcp4", fmt.Sprintf(":%d", clipPort))
	if err != nil {
		fail("could not listen on TCP port %d: %v — is another \"payload clipboard listen\" running?", clipPort, err)
	}
	fmt.Println(styleBadge.Render("clipboard") + "  listening as " + styleTitle.Render(clientName()) +
		styleMuted.Render(fmt.Sprintf(" on port %d (%s)", clipPort, strings.Join(localIPs(), ", "))))
	info("Text sent with \"payload clipboard <text>\" from this network goes onto your clipboard. Ctrl+C stops.")
	info("Device %s · hold a client's text for approval: payload clients, then s", deviceID())

	// Notices for clients that weren't listening when they were flagged go out once they are.
	go func() {
		for {
			time.Sleep(20 * time.Second)
			if len(loadNotices()) > 0 {
				if peers, err := clipDiscover(); err == nil {
					deliverNotices(peers)
				}
			}
		}
	}()

	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := udp.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			if string(buf[:n]) == clipAsk {
				// Both answers: older senders only understand the first.
				_, _ = udp.WriteToUDP([]byte(clipAnswer+" "+clientName()), addr)
				_, _ = udp.WriteToUDP([]byte(clipAnswer2+" "+deviceID()+" "+clientName()), addr)
			}
		}
	}()

	var mu sync.Mutex // one clipboard write at a time
	for {
		conn, err := tcp.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			r := bufio.NewReader(c)
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			var h clipHeader
			if json.Unmarshal([]byte(line), &h) != nil || h.Size < 0 || h.Size > clipMaxBytes {
				fmt.Fprintln(c, "error: bad request")
				return
			}
			body := make([]byte, h.Size)
			if _, err := io.ReadFull(r, body); err != nil {
				return
			}
			ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
			stamp := styleMuted.Render(time.Now().Format("15:04:05"))
			from := styleTitle.Render(h.From)
			if h.Kind == "notice" {
				fmt.Fprintln(c, "ok")
				switch h.Notice {
				case noticeHold:
					fmt.Println(stamp + " " + styleWarn.Render("⚑ ") + from + styleMuted.Render(" ("+ip+")") +
						" flagged you risky — text you send them is on hold until they turn it off")
				case noticeRelease:
					fmt.Println(stamp + " " + styleOK.Render("✓ ") + from + styleMuted.Render(" ("+ip+")") +
						" removed the hold — text you send them goes onto their clipboard again")
				}
				return
			}
			if isHeld(h.ID) {
				fmt.Fprintf(c, "held: %s flagged you risky — what you send is on hold until they turn it off\n", clientName())
				fmt.Println(stamp + " " + styleWarn.Render("⏸ ") + "Held text from " + from +
					styleMuted.Render(" ("+ip+", "+humanSize(len(body))+") — not copied; on hold for approval"))
				fmt.Println(heldText(string(body)))
				return
			}
			mu.Lock()
			err = copyText(string(body))
			mu.Unlock()
			if err != nil {
				fmt.Fprintf(c, "error: %v\n", err)
				fmt.Println(stamp + " " + styleErr.Render("✗ ") + fmt.Sprintf("text from %s (%s) could not be copied: %v", h.From, ip, err))
				return
			}
			fmt.Fprintln(c, "ok")
			fmt.Println(stamp + " " + styleOK.Render("✓ ") + "Copied text from " + from +
				styleMuted.Render(" ("+ip+", "+humanSize(len(body))+")") + "  " + clipPreview(string(body)))
		}(conn)
	}
}

// clipSend finds the listeners and sends text to each of them.
func clipSend(text, source string) {
	fmt.Fprintln(os.Stderr, styleMuted.Render("Looking for listeners on the network…"))
	peers, err := clipDiscover()
	if err != nil {
		fail("could not search the network: %v", err)
	}
	if len(peers) == 0 {
		fail("no one is listening — run \"payload clipboard listen\" on the other computers (same network; allow payload through the firewall)")
	}
	local := map[string]bool{}
	for _, ip := range localIPs() {
		local[ip] = true
	}
	deliverNotices(peers)
	sent, held := 0, 0
	for _, p := range peers {
		label := styleTitle.Render(p.Name) + styleMuted.Render(" ("+p.IP+")")
		if local[p.IP] || net.ParseIP(p.IP).IsLoopback() {
			label += styleMuted.Render(" this computer")
		}
		hold, err := clipDeliver(p.IP, text)
		switch {
		case err != nil:
			fmt.Println(styleErr.Render("✗ ") + label + " " + styleErr.Render(err.Error()))
		case hold != "":
			fmt.Println(styleWarn.Render("⏸ ") + label + " " + styleWarn.Render(hold))
			held++
		default:
			fmt.Println(styleOK.Render("✓ ") + label)
			sent++
		}
	}
	summary := fmt.Sprintf("Sent %s from %s to %d of %d listener(s)", humanSize(len(text)), source, sent, len(peers))
	if held > 0 {
		summary += fmt.Sprintf(", %d held for approval", held)
	}
	if sent+held == 0 {
		fail("%s", summary)
	}
	success("%s", summary)
}

// clipDiscover broadcasts a question on every network and collects the listeners that answer.
func clipDiscover() ([]clipPeer, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	targets := append([]net.IP{net.IPv4bcast}, broadcastAddrs()...)
	if clipLoopbackOnly {
		targets = nil
	}
	sentAny := false
	for _, ip := range targets {
		if _, err := conn.WriteToUDP([]byte(clipAsk), &net.UDPAddr{IP: ip, Port: clipPort}); err == nil {
			sentAny = true
		}
	}
	// A listener on this computer may not hear the broadcast; ask it directly too.
	_, _ = conn.WriteToUDP([]byte(clipAsk), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: clipPort})
	if !sentAny && !clipLoopbackOnly {
		return nil, errors.New("no network to broadcast on")
	}

	found := map[string]clipPeer{}
	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(clipWait))
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		msg := string(buf[:n])
		var p clipPeer
		if rest, ok := strings.CutPrefix(msg, clipAnswer2+" "); ok {
			id, name, _ := strings.Cut(rest, " ")
			if !validDeviceID(id) {
				continue
			}
			p = clipPeer{ID: id, Name: name}
		} else if rest, ok := strings.CutPrefix(msg, clipAnswer+" "); ok {
			p = clipPeer{Name: rest}
		} else {
			continue
		}
		p.IP = addr.IP.String()
		// One entry per computer: a listener here answers both the broadcast and 127.0.0.1, and a
		// new listener answers in both formats.
		key := p.Name
		if old, ok := found[key]; ok {
			oldLAN, newLAN := !net.ParseIP(old.IP).IsLoopback(), !net.ParseIP(p.IP).IsLoopback()
			if old.ID != "" && p.ID == "" { // keep the answer with the device ID; a LAN address beats loopback
				if !oldLAN && newLAN {
					old.IP = p.IP
					found[key] = old
				}
				continue
			}
			if (old.ID == "") == (p.ID == "") && oldLAN {
				continue
			}
			if oldLAN && !newLAN {
				p.IP = old.IP
			}
		}
		found[key] = p
	}
	peers := make([]clipPeer, 0, len(found))
	for _, p := range found {
		peers = append(peers, p)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	return peers, nil
}

// clipDeliver sends text to one listener and waits for its answer. held is the listener's message
// when it kept the text for approval instead of copying it.
func clipDeliver(ip, text string) (held string, err error) {
	reply, err := clipExchange(ip, clipHeader{From: clientName(), ID: deviceID(), Size: len(text)}, text)
	if err != nil {
		return "", err
	}
	if msg, ok := strings.CutPrefix(reply, "held: "); ok {
		return msg, nil
	}
	if reply != "ok" {
		return "", errors.New(strings.TrimPrefix(reply, "error: "))
	}
	return "", nil
}

// clipNotify tells a listener its text is now held (or released) by this computer.
func clipNotify(ip, kind string) error {
	reply, err := clipExchange(ip, clipHeader{From: clientName(), ID: deviceID(), Kind: "notice", Notice: kind}, "")
	if err == nil && reply != "ok" {
		err = errors.New(strings.TrimPrefix(reply, "error: "))
	}
	return err
}

func clipExchange(ip string, h clipHeader, text string) (string, error) {
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort(ip, fmt.Sprint(clipPort)), 3*time.Second)
	if err != nil {
		return "", errors.New("could not connect (firewall?)")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	header, _ := json.Marshal(h)
	if _, err := conn.Write(append(append(header, '\n'), text...)); err != nil {
		return "", fmt.Errorf("send failed: %v", err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", errors.New("no answer from the listener")
	}
	return strings.TrimSpace(reply), nil
}

// heldText shows held text in the listen window, indented and cut at 2,000 characters.
func heldText(s string) string {
	const most = 2000
	r := []rune(strings.TrimRight(s, "\r\n"))
	more := ""
	if len(r) > most {
		more = styleMuted.Render(fmt.Sprintf("\n    … %d more characters", len(r)-most))
		r = r[:most]
	}
	lines := strings.Split(strings.ReplaceAll(string(r), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = styleMuted.Render("    │ ") + l
	}
	return strings.Join(lines, "\n") + more
}

// localIPs lists this computer's IPv4 addresses on interfaces that are up (no loopback).
func localIPs() []string {
	var ips []string
	for _, n := range upNets() {
		ips = append(ips, n.IP.String())
	}
	return ips
}

// broadcastAddrs gives each local network's directed broadcast address (e.g. 192.168.1.255).
// 255.255.255.255 alone leaves only one interface on Windows.
func broadcastAddrs() []net.IP {
	var out []net.IP
	for _, n := range upNets() {
		b := make(net.IP, 4)
		for i := range b {
			b[i] = n.IP[i] | ^n.Mask[i]
		}
		out = append(out, b)
	}
	return out
}

func upNets() []*net.IPNet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || len(n.Mask) != 4 || n.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, &net.IPNet{IP: n.IP.To4(), Mask: n.Mask})
		}
	}
	return out
}

// clipPreview is the first line of text, cut to 60 characters.
func clipPreview(s string) string {
	s = strings.TrimSpace(s)
	more := false
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s, more = s[:i], true
	}
	if utf8.RuneCountInString(s) > 60 {
		s, more = string([]rune(s)[:60]), true
	}
	if more {
		s += "…"
	}
	return styleSmall.Render(s)
}
