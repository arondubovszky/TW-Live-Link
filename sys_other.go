//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// (for testing on Linux: no tray, no start-up entry)
var quitCh = make(chan struct{})

func openWindow(u string)     { fmt.Println("window:", u) }
func runTray(open func())     { <-quitCh }
func quitApp()                { close(quitCh); os.Exit(0) }
func setAutostart(on bool)    {}
func autostartPossible() bool { return false }
func autostartActive() bool   { return false }
func runInstallerAndRestart(setup string) error {
	fmt.Println("would install", setup)
	return nil
}

func scanUDPListeners() map[string][]int {
	out := map[string][]int{}
	b, err := exec.Command("ss", "-ulpnH").Output()
	if err != nil {
		return out
	}
	re := regexp.MustCompile(`:(\d+)\s.*users:\(\("([^"]+)"`)
	for _, line := range strings.Split(string(b), "\n") {
		m := re.FindStringSubmatch(line)
		if m != nil {
			p, _ := strconv.Atoi(m[1])
			out[m[2]] = append(out[m[2]], p)
		}
	}
	return out
}

func setTrayLive(live bool) {}
