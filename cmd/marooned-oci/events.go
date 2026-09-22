package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func doEvents(root string, args []string) int {
	statsOnce := false
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--stats":
			statsOnce = true
		case a == "--interval" && i+1 < len(args):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 {
		fatal("events: missing id")
	}
	id := pos[0]
	if mapped := strings.TrimSpace(string(mustRead(filepath.Join(root, id, "criid")))); mapped != "" {
		id = mapped
	}
	body, err := shimJSON("POST", "/v1/ContainerStats", map[string]string{"id": id})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var st struct {
		ID                string `json:"ID"`
		CPUNano           uint64 `json:"CPUNano"`
		RSSBytes          uint64 `json:"RSSBytes"`
		WorkingSetBytes   uint64 `json:"WorkingSetBytes"`
		Pids              uint64 `json:"Pids"`
		TimestampUnixNano int64  `json:"TimestampUnixNano"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ev := map[string]interface{}{
		"type": "stats",
		"id":   st.ID,
		"data": map[string]interface{}{
			"cpu": map[string]interface{}{
				"usage": map[string]interface{}{
					"total":  st.CPUNano,
					"kernel": uint64(0),
					"user":   st.CPUNano,
				},
			},
			"memory": map[string]interface{}{
				"usage": map[string]interface{}{
					"usage": st.RSSBytes,
					"max":   st.WorkingSetBytes,
				},
				"rss":   st.RSSBytes,
				"cache": uint64(0),
			},
			"pids": map[string]interface{}{
				"current": st.Pids,
			},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(ev); err != nil {
		return 1
	}
	if !statsOnce {
		return 0
	}
	return 0
}
