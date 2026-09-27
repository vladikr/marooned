package sandbox

import (
	"strings"
	"testing"
)

func TestMergeSampleMilliCPU(t *testing.T) {
	prev := &StatsSnapshot{CPUNano: 1e9, TS: 1e9}
	got := MergeSampleAt(prev, 2e9, 4096, 1, "box", 2e9)
	if got.MilliCPU != 1000 {
		t.Fatalf("milliCPU %d want 1000 (1 CPU)", got.MilliCPU)
	}
	if got.RSSBytes != 4096 {
		t.Fatalf("rss %d", got.RSSBytes)
	}
}

func TestPodMetricsJSON(t *testing.T) {
	b := PodMetricsJSON("ns", "p", StatsSnapshot{Container: "box", RSSBytes: 4096, MilliCPU: 7, TS: 1e9})
	if !strings.Contains(string(b), `"cpu":"7m"`) || !(strings.Contains(string(b), "4Ki") || strings.Contains(string(b), "4096")) {
		t.Fatalf("%s", b)
	}
}

func TestParseStatsAnnotation(t *testing.T) {
	s := StatsSnapshot{Container: "box", RSSBytes: 100, MilliCPU: 5}
	raw := FormatStatsAnnotation(s)
	got, err := ParseStatsAnnotation(raw)
	if err != nil || got == nil || got.RSSBytes != 100 || got.MilliCPU != 5 {
		t.Fatalf("%v %+v", err, got)
	}
}
