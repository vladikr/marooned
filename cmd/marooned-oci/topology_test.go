package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestParseCPUList(t *testing.T) {
	got, err := parseCPUList("0-2,4,8-9\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0, 1, 2, 4, 8, 9}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	empty, err := parseCPUList(" \n")
	if err != nil || empty != nil {
		t.Fatalf("empty %v %v", empty, err)
	}
	if _, err := parseCPUList("5-3"); err == nil {
		t.Fatal("inverted range")
	}
}

func TestReadHostCPUTopologyNUMA(t *testing.T) {
	sys := t.TempDir()
	node0 := filepath.Join(sys, "devices/system/node/node0")
	if err := os.MkdirAll(node0, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(node0, "cpulist"), []byte("0-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeSiblings(t, sys, 0, "0-1")
	writeSiblings(t, sys, 1, "0-1")
	cells := readHostCPUTopology(sys)
	if len(cells) != 1 || cells[0].id != 0 || len(cells[0].cpus) != 2 {
		t.Fatalf("%+v", cells)
	}
	if !reflect.DeepEqual(cells[0].cpus[0].siblings, []uint32{0, 1}) {
		t.Fatalf("siblings %+v", cells[0].cpus[0])
	}
}

func TestReadHostCPUTopologyLive(t *testing.T) {
	if _, err := os.Stat("/sys/devices/system/cpu/online"); err != nil {
		t.Skip("no sysfs cpu")
	}
	cells := readHostCPUTopology("/sys")
	if len(cells) == 0 || len(cells[0].cpus) == 0 {
		t.Fatalf("host topology empty: %+v", cells)
	}
}

func TestReadHostCPUTopologyOnlineFallback(t *testing.T) {
	sys := t.TempDir()
	cpu := filepath.Join(sys, "devices/system/cpu")
	if err := os.MkdirAll(cpu, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cpu, "online"), []byte("0-2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cells := readHostCPUTopology(sys)
	if len(cells) != 1 || cells[0].id != 0 {
		t.Fatalf("%+v", cells)
	}
	var ids []uint32
	for _, c := range cells[0].cpus {
		ids = append(ids, c.id)
	}
	if !reflect.DeepEqual(ids, []uint32{0, 1, 2}) {
		t.Fatalf("ids %v", ids)
	}
	if !reflect.DeepEqual(cells[0].cpus[0].siblings, []uint32{0}) {
		t.Fatalf("missing siblings default %+v", cells[0].cpus[0])
	}
}

func TestMarshalTopologyProto(t *testing.T) {
	cells := []hostCell{{
		id: 0,
		cpus: []hostCPU{
			{id: 0, siblings: []uint32{0, 1}},
			{id: 1, siblings: []uint32{0, 1}},
		},
	}}
	topo := marshalTopology(cells)
	cell, rest, ok := protoField(topo, 1)
	if !ok || len(rest) != 0 {
		t.Fatal("numa_cells")
	}
	id, rest, ok := protoVarintField(cell, 1)
	if !ok || id != 0 {
		t.Fatalf("cell id %d", id)
	}
	cpu0, rest, ok := protoField(rest, 5)
	if !ok {
		t.Fatal("cpu0")
	}
	cpuID, packed, ok := protoVarintField(cpu0, 1)
	if !ok || cpuID != 0 {
		t.Fatalf("cpu id %d", cpuID)
	}
	sibs, rest, ok := protoField(packed, 2)
	if !ok || len(rest) != 0 {
		t.Fatal("siblings")
	}
	got := consumePackedU32(sibs)
	if !reflect.DeepEqual(got, []uint32{0, 1}) {
		t.Fatalf("siblings %v", got)
	}
}

func TestMarshalVMIRequestTopology(t *testing.T) {
	spec := []byte(`{"kind":"VirtualMachineInstance"}`)
	topo := marshalTopology([]hostCell{{
		id:   0,
		cpus: []hostCPU{{id: 0, siblings: []uint32{0}}},
	}})
	b := marshalVMIRequest(spec, map[string]string{"default": "tap"}, topo)
	vmiMsg, rest, ok := protoField(b, 1)
	if !ok {
		t.Fatal("vmi")
	}
	got, _, ok := protoField(vmiMsg, 1)
	if !ok || !bytes.Equal(got, spec) {
		t.Fatal("vmiJson")
	}
	opts, rest, ok := protoField(rest, 2)
	if !ok || len(rest) != 0 {
		t.Fatal("options")
	}
	gotTopo, rest, ok := protoField(opts, 4)
	if !ok || !bytes.Equal(gotTopo, topo) {
		t.Fatalf("topology %q", gotTopo)
	}
	entry, rest, ok := protoField(rest, 8)
	if !ok || len(rest) != 0 {
		t.Fatal("attachment")
	}
	k, rest, ok := protoField(entry, 1)
	if !ok || string(k) != "default" {
		t.Fatalf("key %q", k)
	}
}

func TestMarshalVMIRequestEmptyTopology(t *testing.T) {
	spec := []byte(`{"kind":"VirtualMachineInstance"}`)
	b := marshalVMIRequest(spec, nil, []byte{})
	_, rest, ok := protoField(b, 1)
	if !ok {
		t.Fatal("vmi")
	}
	opts, rest, ok := protoField(rest, 2)
	if !ok || len(rest) != 0 {
		t.Fatal("options")
	}
	payload, rest, ok := protoField(opts, 4)
	if !ok || len(payload) != 0 || len(rest) != 0 {
		t.Fatalf("empty topology payload %q rest %d", payload, len(rest))
	}
}

func writeSiblings(t *testing.T, sysfs string, id int, list string) {
	t.Helper()
	dir := filepath.Join(sysfs, "devices/system/cpu", "cpu"+strconv.Itoa(id), "topology")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thread_siblings_list"), []byte(list+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func protoVarintField(b []byte, want int) (uint64, []byte, bool) {
	key, n := consumeVarint(b)
	if n <= 0 {
		return 0, b, false
	}
	field, wt := int(key>>3), int(key&7)
	if field != want || wt != 0 {
		return 0, b, false
	}
	v, n2 := consumeVarint(b[n:])
	if n2 <= 0 {
		return 0, b, false
	}
	return v, b[n+n2:], true
}

func consumePackedU32(b []byte) []uint32 {
	var out []uint32
	for len(b) > 0 {
		v, n := consumeVarint(b)
		if n <= 0 {
			return out
		}
		out = append(out, uint32(v))
		b = b[n:]
	}
	return out
}
