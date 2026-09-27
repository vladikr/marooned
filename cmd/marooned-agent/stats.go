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
	// The workload mounts proc inside the chroot (CLONE_NEWPID). That view
	// is what `ps` in the container sees (sh + python). Host /proc often
	// only shows the wrapper (4Ki RSS, pids=1) so kubectl top prints 0Mi.
	if s, err := sampleProcDir(fmt.Sprintf("/proc/%d/root/proc", rootPID)); err == nil && s.Pids > 0 {
		return s, nil
	}
	pids := pidsInPidNamespace(rootPID)
	return samplePIDSet(pids)
}

func samplePIDSet(pids map[int]struct{}) (agentproto.StatsResponse, error) {
	var cpu, rss uint64
	for pid := range pids {
		st, err := readProcStat(pid)
		if err != nil {
			continue
		}
		cpu += st.cpuNano
		rss += st.rss
	}
	if len(pids) == 0 {
		return agentproto.StatsResponse{}, fmt.Errorf("not found")
	}
	return agentproto.StatsResponse{
		CPUNano:         cpu,
		RSSBytes:        rss,
		WorkingSetBytes: rss,
		Pids:            uint64(len(pids)),
	}, nil
}

func sampleProcDir(procDir string) (agentproto.StatsResponse, error) {
	ents, err := os.ReadDir(procDir)
	if err != nil {
		return agentproto.StatsResponse{}, err
	}
	var cpu, rss, n uint64
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		st, err := readProcStatIn(procDir, pid)
		if err != nil {
			continue
		}
		cpu += st.cpuNano
		rss += st.rss
		n++
	}
	if n == 0 {
		return agentproto.StatsResponse{}, fmt.Errorf("empty proc")
	}
	return agentproto.StatsResponse{
		CPUNano:         cpu,
		RSSBytes:        rss,
		WorkingSetBytes: rss,
		Pids:            n,
	}, nil
}

// pidsInPidNamespace lists every process in rootPID's PID namespace.
// CLONE_NEWPID children are often not PPID-descendants in the agent's
// /proc view, so a parent-walk misses the workload (4Ki RSS, pids=1).
func pidsInPidNamespace(rootPID int) map[int]struct{} {
	want := map[int]struct{}{rootPID: {}}
	ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", rootPID))
	if err != nil {
		return want
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return want
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		n2, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", pid))
		if err != nil || n2 != ns {
			continue
		}
		want[pid] = struct{}{}
	}
	return want
}

type procSample struct {
	cpuNano uint64
	rss     uint64
}

func readProcStat(pid int) (procSample, error) {
	return readProcStatIn("/proc", pid)
}

func readProcStatIn(procDir string, pid int) (procSample, error) {
	b, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", procDir, pid))
	if err != nil {
		return procSample{}, err
	}
	utime, stime, rssPages, err := parseProcStat(string(b))
	if err != nil {
		return procSample{}, err
	}
	rss := rssPages * uint64(os.Getpagesize())
	if rb, err := rssFromStatusFile(fmt.Sprintf("%s/%d/status", procDir, pid)); err == nil && rb > 0 {
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
	return rssFromStatusFile(fmt.Sprintf("/proc/%d/status", pid))
}

func rssFromStatusFile(path string) (uint64, error) {
	f, err := os.Open(path)
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
