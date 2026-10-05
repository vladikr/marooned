package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type hostCPU struct {
	id       uint32
	siblings []uint32
}

type hostCell struct {
	id   uint32
	cpus []hostCPU
}

// hostTopologyProto is virt-handler's VirtualMachineOptions.topology:
// host NUMA cells so dedicatedCPUPlacement can vcpupin. virt-launcher
// panics in cpuChunksToCells when this is nil.
func hostTopologyProto() []byte {
	b := marshalTopology(readHostCPUTopology("/sys"))
	if b == nil {
		return []byte{}
	}
	return b
}

func readHostCPUTopology(sysfs string) []hostCell {
	if sysfs == "" {
		return nil
	}
	nodes := filepath.Join(sysfs, "devices/system/node")
	ents, err := os.ReadDir(nodes)
	if err == nil {
		var cells []hostCell
		for _, e := range ents {
			name := e.Name()
			if !e.IsDir() || !isNodeDir(name) {
				continue
			}
			id, err := strconv.ParseUint(strings.TrimPrefix(name, "node"), 10, 32)
			if err != nil {
				continue
			}
			list, err := os.ReadFile(filepath.Join(nodes, name, "cpulist"))
			if err != nil {
				continue
			}
			cpus, err := parseCPUList(string(list))
			if err != nil || len(cpus) == 0 {
				continue
			}
			cells = append(cells, hostCell{id: uint32(id), cpus: hostCPUs(sysfs, cpus)})
		}
		if len(cells) > 0 {
			sort.Slice(cells, func(i, j int) bool { return cells[i].id < cells[j].id })
			return cells
		}
	}
	for _, name := range []string{"online", "present"} {
		b, err := os.ReadFile(filepath.Join(sysfs, "devices/system/cpu", name))
		if err != nil {
			continue
		}
		cpus, err := parseCPUList(string(b))
		if err != nil || len(cpus) == 0 {
			continue
		}
		return []hostCell{{id: 0, cpus: hostCPUs(sysfs, cpus)}}
	}
	return nil
}

func isNodeDir(name string) bool {
	if !strings.HasPrefix(name, "node") {
		return false
	}
	rest := strings.TrimPrefix(name, "node")
	if rest == "" {
		return false
	}
	for _, r := range rest {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func hostCPUs(sysfs string, ids []uint32) []hostCPU {
	out := make([]hostCPU, 0, len(ids))
	for _, id := range ids {
		sibs := []uint32{id}
		p := filepath.Join(sysfs, "devices/system/cpu", fmt.Sprintf("cpu%d", id), "topology/thread_siblings_list")
		if b, err := os.ReadFile(p); err == nil {
			if parsed, err := parseCPUList(string(b)); err == nil && len(parsed) > 0 {
				sibs = parsed
			}
		}
		out = append(out, hostCPU{id: id, siblings: sibs})
	}
	return out
}

// parseCPUList understands kernel cpulist (0-3,8,10-11).
func parseCPUList(s string) ([]uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []uint32
	seen := map[uint32]struct{}{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, err := parseCPURange(part)
		if err != nil {
			return nil, err
		}
		for n := lo; n <= hi; n++ {
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func parseCPURange(part string) (lo, hi uint32, err error) {
	a, b, ok := strings.Cut(part, "-")
	if !ok {
		n, err := strconv.ParseUint(part, 10, 32)
		return uint32(n), uint32(n), err
	}
	start, err := strconv.ParseUint(strings.TrimSpace(a), 10, 32)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.ParseUint(strings.TrimSpace(b), 10, 32)
	if err != nil {
		return 0, 0, err
	}
	if start > end {
		return 0, 0, fmt.Errorf("cpu range %s", part)
	}
	return uint32(start), uint32(end), nil
}

func marshalTopology(cells []hostCell) []byte {
	var out []byte
	for _, c := range cells {
		out = append(out, protoBytes(1, marshalCell(c))...)
	}
	return out
}

func marshalCell(c hostCell) []byte {
	out := protoU32(1, c.id)
	for _, cpu := range c.cpus {
		out = append(out, protoBytes(5, marshalCPU(cpu))...)
	}
	return out
}

func marshalCPU(c hostCPU) []byte {
	out := protoU32(1, c.id)
	if len(c.siblings) > 0 {
		out = append(out, protoPackedU32(2, c.siblings)...)
	}
	return out
}

func protoU32(field int, v uint32) []byte {
	out := protoVarint(uint64(field<<3 | 0))
	return append(out, protoVarint(uint64(v))...)
}

func protoPackedU32(field int, vs []uint32) []byte {
	var payload []byte
	for _, v := range vs {
		payload = append(payload, protoVarint(uint64(v))...)
	}
	return protoBytes(field, payload)
}
