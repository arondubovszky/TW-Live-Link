package main

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html
var indexHTML []byte

//go:embed ui/logo.png
var logoPNG []byte

//go:embed ui/icon.png
var iconPNG []byte // the app icon, square, large

//go:embed ui/app.ico
var appICO []byte // the app icon in every size (TW at the small ones): the window's icon on the taskbar

var rosterCache struct {
	sync.Mutex
	body []byte
	at   time.Time
	srv  string
}

func uiServer(ln net.Listener) {
	mux := http.NewServeMux()
	local := func(h http.HandlerFunc) http.HandlerFunc { // only this PC, only this app's own page
		return func(w http.ResponseWriter, r *http.Request) {
			if o := r.Header.Get("Origin"); o != "" && !strings.HasPrefix(o, "http://127.0.0.1:"+strconv.Itoa(UIPort)) {
				http.Error(w, "no", 403)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			h(w, r)
		}
	}
	mux.HandleFunc("/", local(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			checkUpdateSoon() // the window was opened: look for a newer version now
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	}))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write(appICO)
	})
	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(iconPNG)
	})
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(logoPNG)
	})
	mux.HandleFunc("/api/state", local(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.lastUI = time.Now() // the window is open
		st.mu.Unlock()
		// while the window is open (it asks for this every second), look for a newer version once a minute: so the
		// Update button shows up even when Edge brings the window back without loading the page again
		if sinceUpdateCheck() > time.Minute {
			checkUpdateSoon()
		}
		s := getSettings()
		st.mu.Lock()
		now := time.Now()
		rate := 0
		for _, t := range st.rates {
			if now.Sub(t) < time.Second {
				rate++
			}
		}
		sentRate := 0
		for _, t := range st.sentTimes {
			if now.Sub(t) < time.Second {
				sentRate++
			}
		}
		out := map[string]any{
			"sentRate": sentRate,
			"counts":   map[string]int{"checkIns": st.CheckIns, "scans": st.Scans},
			"tele": map[string]any{"speed": st.Speed, "rpm": st.RPM, "maxRpm": st.MaxRPM, "gear": st.Gear, "thr": st.Thr, "brk": st.Brk},
			"version": Version, "settings": s, "gameRx": st.GameRx, "gameRate": rate,
			"gameRecent": !st.LastGame.IsZero() && now.Sub(st.LastGame) < 3*time.Second,
			"driving":    !st.RaceOnLast.IsZero() && now.Sub(st.RaceOnLast) < 3*time.Second,
			"sent":       st.Sent, "ws": st.WS, "wsErr": st.WSErr, "serverOK": st.ServerOK, "serverErr": st.ServerErr,
			"update": st.Update, "updating": st.Updating, "raceOpen": st.RaceOpen, "delayMs": st.DelayMs, "listenErr": st.ListenErr, "tools": append([]Tool{}, st.Tools...), "autostartPossible": autostartPossible(),
		}
		st.mu.Unlock()
		json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("/api/roster", local(func(w http.ResponseWriter, r *http.Request) {
		srv := normServer(getSettings().Server)
		rosterCache.Lock()
		defer rosterCache.Unlock()
		if rosterCache.body == nil || rosterCache.srv != srv || time.Since(rosterCache.at) > 10*time.Minute {
			resp, err := httpClient().Get(srv + "/live/roster")
			if err != nil {
				if rosterCache.body == nil {
					http.Error(w, "can't reach the server", 502)
					return
				}
			} else {
				var buf []byte
				buf, _ = readAll(resp)
				if resp.StatusCode == 200 && len(buf) > 2 {
					rosterCache.body, rosterCache.at, rosterCache.srv = buf, time.Now(), srv
					os.WriteFile(dataDir()+"/roster-cache.json", buf, 0o644)
				}
			}
			if rosterCache.body == nil {
				if b, err := os.ReadFile(dataDir() + "/roster-cache.json"); err == nil {
					rosterCache.body = b
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(rosterCache.body)
	}))
	mux.HandleFunc("/api/teams", local(func(w http.ResponseWriter, r *http.Request) { // team logos and colours, from the server
		resp, err := httpClient().Get(normServer(getSettings().Server) + "/live/teams.js")
		if err != nil {
			http.Error(w, "offline", 502)
			return
		}
		b, _ := readAll(resp)
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(b)
	}))
	mux.HandleFunc("/api/settings", local(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST", 405)
			return
		}
		var in map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "bad", 400)
			return
		}
		setMu.Lock()
		str := func(k string, dst *string) {
			if v, ok := in[k]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					*dst = strings.TrimSpace(s)
				}
			}
		}
		str("team", &settings.Team)
		str("name", &settings.Name)
		str("server", &settings.Server)
		if v, ok := in["listenPort"]; ok {
			var p int
			if json.Unmarshal(v, &p) == nil && p > 1023 && p < 65536 {
				settings.ListenPort = p
			}
		}
		if v, ok := in["forward"]; ok {
			var f map[string]bool
			if json.Unmarshal(v, &f) == nil {
				for k, b := range f {
					settings.Forward[k] = b
				}
			}
		}
		if v, ok := in["setupSeen"]; ok {
			json.Unmarshal(v, &settings.SetupSeen)
		}
		auto, autoSet := false, false
		if v, ok := in["autoStart"]; ok {
			if json.Unmarshal(v, &auto) == nil {
				settings.AutoStart, autoSet = auto, true
			}
		}
		settings.Server = normServer(settings.Server)
		setMu.Unlock()
		if autoSet {
			setAutostart(auto)
		}
		saveSettings()
		w.Write([]byte(`{"ok":true}`))
	}))
	mux.HandleFunc("/api/update", local(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST", 405)
			return
		}
		go startUpdate()
		w.Write([]byte(`{"ok":true}`))
	}))
	mux.HandleFunc("/api/quit", local(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST", 405)
			return
		}
		w.Write([]byte(`{"ok":true}`))
		go func() { time.Sleep(300 * time.Millisecond); quitApp() }()
	}))
	(&http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}).Serve(ln)
}

func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var b []byte
	buf := make([]byte, 32768)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			return b, nil
		}
		if len(b) > 8<<20 {
			return b, nil
		}
	}
}
