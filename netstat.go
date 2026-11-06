package main

import (
	"encoding/csv"
	"strconv"
	"strings"
)

// parseNetstat: "netstat -ano -p UDP" + "tasklist /FO CSV /NH" output -> program name -> UDP ports it listens on
func parseNetstat(ns, tl string, me int) map[string][]int {
	out := map[string][]int{}
	names := map[int]string{}
	rows, _ := csv.NewReader(strings.NewReader(tl)).ReadAll()
	for _, r := range rows {
		if len(r) >= 2 {
			if pid, err := strconv.Atoi(strings.TrimSpace(r[1])); err == nil {
				names[pid] = strings.TrimSuffix(r[0], ".exe")
			}
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(ns, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.EqualFold(f[0], "UDP") {
			continue
		}
		addr := f[1]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		ip := addr[:i]
		if !(ip == "0.0.0.0" || ip == "127.0.0.1" || ip == "[::]") {
			continue // only programs listening on this PC itself
		}
		port, err1 := strconv.Atoi(addr[i+1:])
		pid, err2 := strconv.Atoi(f[len(f)-1])
		if err1 != nil || err2 != nil || pid == me || pid <= 4 {
			continue
		}
		name := names[pid]
		if name == "" {
			continue
		}
		k := name + ":" + strconv.Itoa(port)
		if seen[k] {
			continue
		}
		seen[k] = true
		out[name] = append(out[name], port)
	}
	return out
}
