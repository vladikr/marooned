package sandbox

import (
	"encoding/json"
	"fmt"
	"time"
)

// GuestStatsAnnotation is JSON Snapshot of guest RSS/CPU (not the pause cgroup).
const GuestStatsAnnotation = "maroonedpods.io/guest-stats"

// StatsSnapshot is written by the shim and read for metrics.k8s.io.
type StatsSnapshot struct {
	Container string `json:"container"`
	CPUNano   uint64 `json:"cpuNano"`
	RSSBytes  uint64 `json:"rssBytes"`
	Pids      uint64 `json:"pids"`
	TS        int64  `json:"ts"`
	PrevCPU   uint64 `json:"prevCPU,omitempty"`
	PrevTS    int64  `json:"prevTS,omitempty"`
	MilliCPU  int64  `json:"milliCPU"`
}

// MergeSample fills MilliCPU from the previous snapshot (cumulative CPUNano).
func MergeSample(prev *StatsSnapshot, cpuNano, rss, pids uint64, container string) StatsSnapshot {
	return MergeSampleAt(prev, cpuNano, rss, pids, container, time.Now().UnixNano())
}

func MergeSampleAt(prev *StatsSnapshot, cpuNano, rss, pids uint64, container string, now int64) StatsSnapshot {
	s := StatsSnapshot{
		Container: container,
		CPUNano:   cpuNano,
		RSSBytes:  rss,
		Pids:      pids,
		TS:        now,
	}
	if prev != nil && prev.TS > 0 && now > prev.TS && cpuNano >= prev.CPUNano {
		dt := now - prev.TS
		s.PrevCPU = prev.CPUNano
		s.PrevTS = prev.TS
		s.MilliCPU = int64((cpuNano - prev.CPUNano) * 1000 / uint64(dt))
	} else if prev != nil {
		s.MilliCPU = prev.MilliCPU
		s.PrevCPU = prev.CPUNano
		s.PrevTS = prev.TS
	}
	return s
}

func ParseStatsAnnotation(s string) (*StatsSnapshot, error) {
	if s == "" {
		return nil, nil
	}
	var out StatsSnapshot
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func FormatStatsAnnotation(s StatsSnapshot) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// PodMetricsJSON is a metrics.k8s.io/v1beta1 PodMetrics object.
func PodMetricsJSON(ns, name string, snap StatsSnapshot) []byte {
	cpu := fmt.Sprintf("%dm", snap.MilliCPU)
	if snap.MilliCPU <= 0 {
		cpu = "0"
	}
	mem := fmt.Sprintf("%d", snap.RSSBytes)
	ts := time.Unix(0, snap.TS).UTC().Format(time.RFC3339Nano)
	ctr := snap.Container
	if ctr == "" {
		ctr = name
	}
	m := map[string]interface{}{
		"kind":       "PodMetrics",
		"apiVersion": "metrics.k8s.io/v1beta1",
		"metadata":   map[string]string{"name": name, "namespace": ns},
		"timestamp":  ts,
		"window":     "15s",
		"containers": []map[string]interface{}{{
			"name": ctr,
			"usage": map[string]string{
				"cpu":    cpu,
				"memory": mem,
			},
		}},
	}
	b, _ := json.Marshal(m)
	return b
}
