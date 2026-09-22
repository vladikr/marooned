package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"maroonedpods.io/maroonedpods/pkg/sandbox/agentproto"
)

func (a *agent) stats(payload json.RawMessage) (agentproto.StatsResponse, error) {
	var req agentproto.StatsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentproto.StatsResponse{}, err
	}
	a.mu.Lock()
	ctr := a.ctrs[req.ContainerID]
	a.mu.Unlock()
	if ctr == nil || ctr.cmd == nil || ctr.cmd.Process == nil {
		return agentproto.StatsResponse{}, fmt.Errorf("not found")
	}
	pid := ctr.cmd.Process.Pid
	s, err := sampleProcessTree(pid)
	if err != nil {
		return agentproto.StatsResponse{}, err
	}
	s.TimestampUnixNano = time.Now().UnixNano()
	return s, nil
}

func sampleProcessTree(rootPID int) (agentproto.StatsResponse, error) {
	st, err := readProcStat(rootPID)
	if err != nil {
		return agentproto.StatsResponse{}, err
	}
	return agentproto.StatsResponse{
		CPUNano:         st.cpuNano,
		RSSBytes:        st.rss,
		WorkingSetBytes: st.rss,
		Pids:            1,
	}, nil
}

type procSample struct {
	cpuNano uint64
	rss     uint64
}

func readProcStat(pid int) (procSample, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procSample{}, err
	}
	utime, stime, rssPages, err := parseProcStat(string(b))
	if err != nil {
		return procSample{}, err
	}
	rss := rssPages * uint64(os.Getpagesize())
	if rb, err := rssFromStatus(pid); err == nil && rb > 0 {
		rss = rb
	}
	return procSample{cpuNano: ticksToNano(utime + stime), rss: rss}, nil
}

func parseProcStat(data string) (utime, stime, rssPages uint64, err error) {
	i := strings.LastIndex(data, ")")
	if i < 0 || i+2 >= len(data) {
		return 0, 0, 0, fmt.Errorf("bad stat")
	}
	fields := strings.Fields(data[i+1:])
	// after comm: [0]=state [11]=utime [12]=stime [21]=rss
	if len(fields) < 22 {
		return 0, 0, 0, fmt.Errorf("short stat")
	}
	utime, _ = strconv.ParseUint(fields[11], 10, 64)
	stime, _ = strconv.ParseUint(fields[12], 10, 64)
	rssPages, _ = strconv.ParseUint(fields[21], 10, 64)
	return utime, stime, rssPages, nil
}

func rssFromStatus(pid int) (uint64, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("bad VmRSS")
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("no VmRSS")
}

func ticksToNano(ticks uint64) uint64 {
	hz := uint64(100)
	return ticks * (uint64(time.Second) / hz)
}
