// TW Live Link: passes Forza's Data Out on to the Team Wars live server (and to the player's own telemetry apps).
//
// The game sends its data to this app (Data Out 127.0.0.1, port 8001). The app:
//   - passes every packet on, unchanged, to telemetry apps on this PC (SimHub and the like) that the player switched on;
//   - sends it to the live server over a WebSocket, with the player's team and name, so the live view shows the car
//     under their name. In front of each packet goes "TWL2" plus how long ago the game sent it (connection delay
//     plus time waiting here, in ms), so the server can line up drivers on different connections.
//
// Free roam is handled by the server (it only counts once the game's race timer runs), so nothing is needed here.
package main

import (
	"sync"
	"math"
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const Version = "1.5.0"
const DefaultServer = "https://twlive.duckdns.org"
const UIPort = 17771

// ---------------- settings ----------------
type Settings struct {
	Server     string          `json:"server"`
	Team       string          `json:"team"`
	Name       string          `json:"name"`
	ListenPort int             `json:"listenPort"`
	Forward    map[string]bool `json:"forward"` // telemetry app (program name) -> pass data on to it
	AutoStart  bool            `json:"autoStart"`
	SetupSeen  bool            `json:"setupSeen"`
}

var (
	setMu    sync.Mutex
	settings = Settings{Server: DefaultServer, ListenPort: 8001, Forward: map[string]bool{}}
)

func dataDir() string {
	d := os.Getenv("APPDATA")
	if d == "" {
		d, _ = os.UserConfigDir()
	}
	p := filepath.Join(d, "TW Live Link")
	os.MkdirAll(p, 0o755)
	return p
}

func loadSettings() {
	b, err := os.ReadFile(filepath.Join(dataDir(), "settings.json"))
	if err == nil {
		setMu.Lock()
		json.Unmarshal(b, &settings)
		if settings.Forward == nil {
			settings.Forward = map[string]bool{}
		}
		if settings.ListenPort == 0 {
			settings.ListenPort = 8001
		}
		if strings.TrimSpace(settings.Server) == "" {
			settings.Server = DefaultServer
		}
		setMu.Unlock()
	}
}

func saveSettings() {
	setMu.Lock()
	b, _ := json.MarshalIndent(settings, "", "  ")
	setMu.Unlock()
	tmp := filepath.Join(dataDir(), "settings.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, filepath.Join(dataDir(), "settings.json"))
	}
}

func getSettings() Settings {
	setMu.Lock()
	defer setMu.Unlock()
	s := settings
	s.Forward = map[string]bool{}
	for k, v := range settings.Forward {
		s.Forward[k] = v
	}
	return s
}

// "twlive.duckdns.org", "https://twlive.duckdns.org/live/" ... -> "https://twlive.duckdns.org"
func normServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultServer
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return DefaultServer
	}
	return u.Scheme + "://" + u.Host
}

// ---------------- state shown in the window ----------------
type State struct {
	mu         sync.Mutex
	GameRx     int64     // packets from the game, total
	LastGame   time.Time // last packet from the game
	RaceOnLast time.Time // last packet while driving (not in a menu)
	Sent       int64
	WS         string // "off", "connecting", "open", "error"
	WSErr      string
	ServerOK   bool
	ServerErr  string
	RaceOpen   bool
	DelayMs    int
	ListenErr  string
	Update     string // a newer version on the server ("" = up to date)
	UpdateURL  string
	Updating   string
	Tools      []Tool
	FwdCount   map[int]int64
	rates      []time.Time
	sentTimes  []time.Time // when packets went out to the live server (for the "sending" meter)
	lastUI     time.Time   // when the window last asked for the status (it does every second while open)
	CheckIns, Scans int    // how often it asked the server / looked for other apps (to verify it stays calm)
	// the latest car data from Forza, shown in the window so you can see it's live
	Speed, RPM, MaxRPM float32
	Gear, Thr, Brk      int
}

var st = &State{WS: "off", FwdCount: map[int]int64{}}

type Tool struct {
	Name  string `json:"name"`
	Port  int    `json:"port"`
	Known bool   `json:"known"`
	On    bool   `json:"on"`
}

// ---------------- receiving from the game ----------------
var sendQ = make(chan []byte, 512)
var targets atomic.Value // []int: ports of telemetry apps to pass data on to

func gameReceiver() {
	var conn *net.UDPConn
	port := 0
	for {
		want := getSettings().ListenPort
		if conn == nil || port != want {
			if conn != nil {
				conn.Close()
			}
			c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: want})
			if err != nil {
				st.mu.Lock()
				st.ListenErr = fmt.Sprintf("Port %d is already in use by another program.", want)
				st.mu.Unlock()
				time.Sleep(3 * time.Second)
				continue
			}
			c.SetReadBuffer(1 << 20)
			conn, port = c, want
			st.mu.Lock()
			st.ListenErr = ""
			st.mu.Unlock()
		}
		buf := make([]byte, 2048)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			continue
		}
		pkt := buf[:n]
		now := time.Now()
		// pass on to the player's telemetry apps first, unchanged and straight away
		if ts, _ := targets.Load().([]int); len(ts) > 0 {
			for _, tp := range ts {
				if tp == port {
					continue
				}
				if out, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: tp}); err == nil {
					out.Write(pkt)
					out.Close()
					st.mu.Lock()
					st.FwdCount[tp]++
					st.mu.Unlock()
				}
			}
		}
		st.mu.Lock()
		st.GameRx++
		st.LastGame = now
		if n >= 4 && binary.LittleEndian.Uint32(pkt[:4]) != 0 {
			st.RaceOnLast = now
		}
		st.rates = append(st.rates, now)
		if len(st.rates) > 400 {
			st.rates = st.rates[len(st.rates)-200:]
		}
		if n >= 324 {
			f := func(o int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(pkt[o:])) }
			st.MaxRPM, st.RPM, st.Speed = f(8), f(16), f(256)*3.6
			st.Gear, st.Thr, st.Brk = int(pkt[319]), int(pkt[315]), int(pkt[316])
		}
		raceOpen := st.RaceOpen
		st.mu.Unlock()
		if n >= 324 && raceOpen { // only during a Team Wars race: outside races nothing is sent
			// to the live server: [8 bytes when it arrived] + packet; the sender turns the time into the delay
			m := make([]byte, 8+n)
			binary.LittleEndian.PutUint64(m, uint64(now.UnixNano()))
			copy(m[8:], pkt)
			select {
			case sendQ <- m:
			default: // connection stuck: drop the oldest, keep the newest
				select {
				case <-sendQ:
				default:
				}
				select {
				case sendQ <- m:
				default:
				}
			}
		}
	}
}

// ---------------- the live server ----------------
var halfRTT atomic.Int64 // ms, measured by checkIn

func httpClient() *http.Client { return &http.Client{Timeout: 6 * time.Second} }

// every few seconds: can we reach the server, is a race open, and how long does a message take
// Is the player around? Forza sending in the last 2 minutes, a race open, or the window open. Only then does the
// app check in every few seconds and look for other apps; otherwise it stays quiet (once a minute, no scanning).
func busy() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return (!st.LastGame.IsZero() && time.Since(st.LastGame) < 2*time.Minute) || st.RaceOpen || time.Since(st.lastUI) < 10*time.Second
}

func checkIn() {
	for {
		s := getSettings()
		base := normServer(s.Server)
		t0 := time.Now()
		r, err := httpClient().Get(base + "/live/link?" + url.Values{"team": {s.Team}, "name": {s.Name}}.Encode())
		if err == nil {
			var j struct {
				Known bool `json:"known"`
				Open  bool `json:"open"`
			}
			json.NewDecoder(r.Body).Decode(&j)
			r.Body.Close()
			rtt := time.Since(t0).Milliseconds()
			halfRTT.Store(rtt / 2)
			st.mu.Lock()
			st.ServerOK, st.ServerErr, st.RaceOpen, st.DelayMs = true, "", j.Open, int(rtt/2)
			st.mu.Unlock()
		} else {
			st.mu.Lock()
			st.ServerOK, st.ServerErr = false, "Can't reach the live server ("+base+"). Check your internet."
			st.mu.Unlock()
		}
		st.mu.Lock()
		st.CheckIns++
		st.mu.Unlock()
		for i := 0; i < 60; i++ { // idle: ask once a minute; busy: every 5 seconds
			time.Sleep(time.Second)
			if i >= 4 && busy() {
				break
			}
		}
	}
}

func setWS(state, e string) {
	st.mu.Lock()
	st.WS, st.WSErr = state, e
	st.mu.Unlock()
}

// keeps a WebSocket to the server open while the player has picked who they are, and sends the game data
func sender() {
	backoff := time.Second
	for {
		s := getSettings()
		if s.Team == "" || s.Name == "" {
			setWS("off", "")
			drain()
			time.Sleep(time.Second)
			continue
		}
		setWS("connecting", "")
		conn, err := wsDial(normServer(s.Server) + "/live/ingest?" + url.Values{"team": {s.Team}, "name": {s.Name}}.Encode())
		if err != nil {
			setWS("error", err.Error())
			drain()
			time.Sleep(backoff)
			if backoff < 10*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		setWS("open", "")
		dead := make(chan struct{})
		go func() { // read side: answer pings, notice when the server goes away
			defer close(dead)
			r := bufio.NewReader(conn)
			for {
				op, payload, err := wsRead(r)
				if err != nil {
					return
				}
				if op == 9 {
					wsWrite(conn, 10, payload)
				}
				if op == 8 {
					return
				}
			}
		}()
		who := s.Team + "|" + s.Name
	loop:
		for {
			select {
			case <-dead:
				break loop
			case m := <-sendQ:
				cur := getSettings()
				if cur.Team+"|"+cur.Name != who || normServer(cur.Server) != normServer(s.Server) {
					break loop // they picked someone else or another server: reconnect as them
				}
				arrived := time.Unix(0, int64(binary.LittleEndian.Uint64(m[:8])))
				lat := halfRTT.Load() + time.Since(arrived).Milliseconds()
				if lat > 1000 {
					lat = 1000
				}
				out := make([]byte, 6+len(m)-8)
				copy(out, "TWL2")
				binary.LittleEndian.PutUint16(out[4:], uint16(lat))
				copy(out[6:], m[8:])
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := wsWrite(conn, 2, out); err != nil {
					break loop
				}
				st.mu.Lock()
				st.Sent++
				st.sentTimes = append(st.sentTimes, time.Now())
				if len(st.sentTimes) > 400 {
					st.sentTimes = st.sentTimes[len(st.sentTimes)-200:]
				}
				st.mu.Unlock()
			case <-time.After(15 * time.Second):
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if wsWrite(conn, 9, []byte("hi")) != nil {
					break loop
				}
			}
		}
		conn.Close()
		setWS("connecting", "")
		time.Sleep(500 * time.Millisecond)
	}
}

func drain() {
	for {
		select {
		case <-sendQ:
		default:
			return
		}
	}
}

// ---------------- a small WebSocket client (wss:// or ws://) ----------------
func wsDial(httpURL string) (net.Conn, error) {
	u, err := url.Parse(httpURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	var conn net.Conn
	d := &net.Dialer{Timeout: 8 * time.Second}
	if u.Scheme == "https" {
		if !strings.Contains(host, ":") {
			host += ":443"
		}
		conn, err = tls.DialWithDialer(d, "tcp", host, &tls.Config{ServerName: u.Hostname()})
	} else {
		if !strings.Contains(host, ":") {
			host += ":80"
		}
		conn, err = d.Dial("tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("can't connect to the server")
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	keyB := make([]byte, 16)
	rand.Read(keyB)
	key := base64.StdEncoding.EncodeToString(keyB)
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nUser-Agent: TW-Live-Link/" + Version + "\r\n\r\n"
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	accept := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(v)
		}
	}
	if !strings.Contains(status, " 101") {
		conn.Close()
		if strings.Contains(status, " 403") {
			return nil, fmt.Errorf("the server didn't accept your team and name")
		}
		return nil, fmt.Errorf("the server refused the connection (%s)", strings.TrimSpace(status))
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if accept != base64.StdEncoding.EncodeToString(h[:]) {
		conn.Close()
		return nil, fmt.Errorf("unexpected answer from the server")
	}
	conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 { // shouldn't happen; keep it simple
		conn.Close()
		return nil, fmt.Errorf("unexpected data from the server")
	}
	return conn, nil
}

func wsWrite(c net.Conn, op byte, p []byte) error {
	hdr := []byte{0x80 | op}
	n := len(p)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 65536:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
	}
	var mask [4]byte
	rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)
	out := make([]byte, len(hdr)+n)
	copy(out, hdr)
	for i := 0; i < n; i++ {
		out[len(hdr)+i] = p[i] ^ mask[i%4]
	}
	_, err := c.Write(out)
	return err
}

func wsRead(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	op := h[0] & 0x0F
	n := uint64(h[1] & 0x7F)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > 1<<20 {
		return 0, nil, fmt.Errorf("frame too big")
	}
	var mask [4]byte
	if h[1]&0x80 != 0 {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	if h[1]&0x80 != 0 {
		for i := range p {
			p[i] ^= mask[i%4]
		}
	}
	return op, p, nil
}

// ---------------- telemetry apps on this PC ----------------
// Programs known to read Forza's Data Out: switched on by default when found.
var knownTools = []string{"simhub", "horizonplus", "laptime", "f1r", "racelab", "crewchief", "simracingstudio", "sim racing studio",
	"motec", "z1", "overtake", "dashpanel", "srs", "fanatec", "moza", "simucube", "buttkicker", "telemetry", "trackmania", "forzatelemetry", "fh5", "fh6"}

// Programs that listen for UDP for their own reasons: never offered.
var skipTools = []string{"system", "svchost", "lsass", "services", "wininit", "spoolsv", "msedge", "chrome", "firefox", "opera", "brave", "vivaldi",
	"discord", "steam", "steamwebhelper", "forzahorizon", "spotify", "nvidia", "nvcontainer", "teams", "zoom", "onedrive", "searchhost",
	"explorer", "dashost", "mdns", "bonjour", "epic", "battle.net", "ea", "origin", "ubisoft", "xbox", "gamingservices", "tw live link", "twlivelink", "dropbox", "telegram", "whatsapp", "slack"}

func classify(name string) (skip, known bool) {
	n := strings.ToLower(name)
	for _, s := range skipTools {
		if strings.Contains(n, s) {
			return true, false
		}
	}
	for _, k := range knownTools {
		if strings.Contains(n, k) {
			return false, true
		}
	}
	return false, false
}

func toolScanner() {
	var last time.Time
	was := false
	for {
		now := busy()
		if !now || (was && time.Since(last) < 30*time.Second) {
			was = now
			time.Sleep(2 * time.Second)
			continue
		}
		was, last = true, time.Now()
		st.mu.Lock()
		st.Scans++
		st.mu.Unlock()
		found := scanUDPListeners() // platform-specific: program name -> ports
		s := getSettings()
		var tools []Tool
		var ports []int
		for name, ps := range found {
			skip, known := classify(name)
			if skip {
				continue
			}
			on, set := s.Forward[name]
			if !set {
				on = known
			}
			for _, p := range ps {
				if p == s.ListenPort || p < 1024 {
					continue
				}
				tools = append(tools, Tool{Name: name, Port: p, Known: known, On: on})
				if on {
					ports = append(ports, p)
				}
			}
		}
		sort.Slice(tools, func(i, j int) bool {
			if tools[i].Known != tools[j].Known {
				return tools[i].Known
			}
			return strings.ToLower(tools[i].Name) < strings.ToLower(tools[j].Name)
		})
		if tp := os.Getenv("TW_TEST_FORWARD"); tp != "" { // (only used for testing)
			var p int
			fmt.Sscanf(tp, "%d", &p)
			ports = append(ports, p)
		}
		targets.Store(ports)
		st.mu.Lock()
		st.Tools = tools
		st.mu.Unlock()
	}
}

// ---------------- updates ----------------
// The server says which version of the Live Link is current. If it's newer, the window offers to update:
// the new installer is downloaded and run, and the Live Link starts again by itself afterwards.
// the tray shows when you're live (sending to an open race, with Forza running), even with the window closed
func trayWatcher() {
	for range time.Tick(time.Second) {
		st.mu.Lock()
		live := st.WS == "open" && st.RaceOpen && !st.LastGame.IsZero() && time.Since(st.LastGame) < 3*time.Second
		st.mu.Unlock()
		setTrayLive(live)
	}
}

func newer(a, b string) bool { // is version a newer than b ("1.2.0" > "1.1.9")
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &x)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &y)
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// Looks for a newer version on the live server: when the app starts, every hour, and whenever its window is opened
// (checkUpdateSoon), so after a server update the Update button shows up straight away.
var updateKick = make(chan struct{}, 1)

func checkUpdateSoon() {
	select {
	case updateKick <- struct{}{}:
	default:
	}
}

var lastUpdateCheck time.Time // when it last asked the live server (only touched by updateChecker / checkUpdateOnce)
var lastCheckMu sync.Mutex

func sinceUpdateCheck() time.Duration {
	lastCheckMu.Lock()
	defer lastCheckMu.Unlock()
	return time.Since(lastUpdateCheck)
}

func checkUpdateOnce() {
	lastCheckMu.Lock()
	lastUpdateCheck = time.Now()
	lastCheckMu.Unlock()
	base := normServer(getSettings().Server)
	r, err := httpClient().Get(base + "/live/app-version")
	if err != nil {
		return
	}
	var j struct {
		Version string `json:"version"`
		URL     string `json:"url"`
	}
	json.NewDecoder(r.Body).Decode(&j)
	r.Body.Close()
	st.mu.Lock()
	if j.Version != "" && newer(j.Version, Version) && j.URL != "" {
		st.Update, st.UpdateURL = j.Version, base+j.URL
	} else {
		st.Update, st.UpdateURL = "", ""
	}
	st.mu.Unlock()
}

func updateChecker() {
	last := time.Time{}
	for {
		checkUpdateOnce()
		last = time.Now()
		select {
		case <-time.After(time.Hour):
		case <-updateKick:
			if time.Since(last) < 20*time.Second { // the window opened again right away: no need to ask twice
				time.Sleep(20*time.Second - time.Since(last))
			}
		}
	}
}

func startUpdate() {
	st.mu.Lock()
	u := st.UpdateURL
	st.Updating = "Downloading the update…"
	st.mu.Unlock()
	fail := func(msg string) {
		log.Printf("update failed: %s", msg)
		st.mu.Lock()
		st.Updating = "The update didn't work (" + msg + "). Try again later."
		st.mu.Unlock()
	}
	if u == "" {
		fail("no update found")
		return
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Get(u)
	if err != nil {
		fail("can't download it")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fail(resp.Status)
		return
	}
	path := filepath.Join(os.TempDir(), "TW-Live-Link-Setup.exe")
	f, err := os.Create(path)
	if err != nil {
		fail("can't save it")
		return
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil || n < 100000 {
		fail("the download was incomplete")
		return
	}
	st.mu.Lock()
	st.Updating = "Installing… (Windows may ask for permission: click Yes)"
	st.mu.Unlock()
	if err := runInstallerAndRestart(path); err != nil {
		fail(err.Error())
	}
}

// ---------------- logging ----------------
func setupLog() {
	f, err := os.OpenFile(filepath.Join(dataDir(), "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		if fi, _ := f.Stat(); fi != nil && fi.Size() > 2<<20 {
			f.Truncate(0)
		}
		log.SetOutput(f)
	}
	log.Printf("TW Live Link %s starting", Version)
}
