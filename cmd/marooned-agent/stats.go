package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	out := agentproto.StatsResponse{}
	pids, err := pidsInTree(rootPID)
	if err != nil || len(pids) == 0 {
		pids = []int{rootPID}
	}
	out.Pids = uint64(len(pids))
	for _, pid := range pids {
		st, err := readProcStat(pid)
		if err != nil {
			continue
		}
		out.CPUNano += st.cpuNano
		out.RSSBytes += st.rss
	}
	out.WorkingSetBytes = out.RSSBytes
	return out, nil
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

func pidsInTree(rootPID int) ([]int, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return []int{rootPID}, err
	}
	want := map[int]struct{}{rootPID: {}}
	changed := true
	for changed {
		changed = false
		for _, e := range ents {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			if _, ok := want[pid]; ok {
				continue
			}
			ppid, err := procPPID(pid)
			if err != nil {
				continue
			}
			if _, ok := want[ppid]; ok {
				want[pid] = struct{}{}
				changed = true
			}
		}
	}
	out := make([]int, 0, len(want))
	for pid := range want {
		out = append(out, pid)
	}
	return out, nil
}

func procPPID(pid int) (int, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	i := strings.LastIndex(string(b), ")")
	if i < 0 {
		return 0, fmt.Errorf("bad stat")
	}
	fields := strings.Fields(string(b)[i+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("short stat")
	}
	return strconv.Atoi(fields[1])
}
