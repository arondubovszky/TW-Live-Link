package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
)

func uiURL() string { return "http://127.0.0.1:" + strconv.Itoa(UIPort) + "/" }

func main() {
	background := false
	for _, a := range os.Args[1:] {
		if a == "--background" {
			background = true
		}
	}
	// one Live Link at a time: if it's already running, just show its window
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(UIPort))
	if err != nil {
		openWindow(uiURL())
		return
	}
	setupLog()
	loadSettings()
	s := getSettings()
	if !s.AutoStart && autostartActive() { // turned on by the installer
		setMu.Lock()
		settings.AutoStart = true
		setMu.Unlock()
		saveSettings()
		s = getSettings()
	}
	if s.AutoStart {
		setAutostart(true) // keeps the start-up entry pointing at this copy (after an update it may have moved)
	}
	go uiServer(ln)
	go gameReceiver()
	go checkIn()
	go sender()
	go toolScanner()
	go updateChecker()
	if !background {
		openWindow(uiURL())
	}
	log.Printf("running; window %s", uiURL())
	go trayWatcher()
	runTray(func() { openWindow(uiURL()) }) // blocks until Quit
	fmt.Println("bye")
}
