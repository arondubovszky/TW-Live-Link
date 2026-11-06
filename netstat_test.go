package main

import "testing"

const sampleNetstat = "\r\nActive Connections\r\n\r\n  Proto  Local Address          Foreign Address        State           PID\r\n" +
	"  UDP    0.0.0.0:500            *:*                                    4512\r\n" +
	"  UDP    0.0.0.0:20777          *:*                                    9120\r\n" +
	"  UDP    127.0.0.1:8001         *:*                                    7788\r\n" +
	"  UDP    0.0.0.0:5353           *:*                                    15880\r\n" +
	"  UDP    192.168.1.20:137       *:*                                    4\r\n" +
	"  UDP    0.0.0.0:9999           *:*                                    3344\r\n" +
	"  UDP    [::]:20777             *:*                                    9120\r\n"
const sampleTasklist = "\"svchost.exe\",\"4512\",\"Services\",\"0\",\"12,000 K\"\r\n\"SimHubWPF.exe\",\"9120\",\"Console\",\"1\",\"300,000 K\"\r\n" +
	"\"TW Live Link.exe\",\"7788\",\"Console\",\"1\",\"20,000 K\"\r\n\"msedge.exe\",\"15880\",\"Console\",\"1\",\"90,000 K\"\r\n\"MyDash.exe\",\"3344\",\"Console\",\"1\",\"9,000 K\"\r\n"

func TestParseNetstat(t *testing.T) {
	got := parseNetstat(sampleNetstat, sampleTasklist, 7788)
	if len(got["SimHubWPF"]) != 1 || got["SimHubWPF"][0] != 20777 {
		t.Fatalf("SimHub: %v", got)
	}
	if _, ok := got["TW Live Link"]; ok {
		t.Fatalf("listed itself: %v", got)
	}
	skip, known := classify("SimHubWPF")
	if skip || !known {
		t.Fatalf("SimHub should be a known telemetry app")
	}
	if s, _ := classify("svchost"); !s {
		t.Fatalf("svchost should be skipped")
	}
	if s, _ := classify("msedge"); !s {
		t.Fatalf("browsers should be skipped")
	}
	if s, k := classify("MyDash"); s || k {
		t.Fatalf("unknown apps: listed, off by default")
	}
	t.Logf("found: %v", got)
}
