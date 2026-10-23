package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// appServer is `codex app-server` on stdio, far enough for the handshake and
// account/rateLimits/read. It reads one JSON object per line until stdin
// closes. The rate limits answer is the file FAKECODEX_RATE_LIMITS names,
// verbatim as the reply's "result"; when that variable is unset the reply is a
// JSON-RPC error. FAKECODEX_SLEEP_MS delays the first reply. Before that answer it sends a notification, as Codex does.
func appServer() int {
	if path := os.Getenv("FAKECODEX_RECORD"); path != "" {
		record(path, nil)
	}
	sleepFor("FAKECODEX_SLEEP_MS")
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<26)
	for in.Scan() {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil || req.ID == nil {
			continue // a notification, or noise
		}
		switch req.Method {
		case "initialize":
			reply(*req.ID, `{"userAgent":"fakecodex","codexHome":"/fake","platformFamily":"unix","platformOs":"linux"}`)
		case "account/rateLimits/read":
			fmt.Println(`{"method":"account/updated","params":{"authMode":"chatgpt","planType":"prolite"}}`)
			rateLimits(*req.ID)
		default:
			replyError(*req.ID, "unknown method")
		}
	}
	return 0
}

// rateLimits answers account/rateLimits/read from the fixture file.
func rateLimits(id int) {
	path := os.Getenv("FAKECODEX_RATE_LIMITS")
	if path == "" {
		replyError(id, "no rate limits (FAKECODEX_RATE_LIMITS is unset)")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		replyError(id, err.Error())
		return
	}
	var compact bytes.Buffer
	if json.Compact(&compact, data) != nil {
		compact.Reset() // not JSON: sent as it is, so the caller meets an unreadable line
		compact.Write(bytes.TrimSpace(data))
	}
	reply(id, compact.String())
}

func reply(id int, result string) { fmt.Printf("{\"id\":%d,\"result\":%s}\n", id, result) }

func replyError(id int, message string) {
	quoted, _ := json.Marshal(message)
	fmt.Printf("{\"id\":%d,\"error\":{\"code\":-32601,\"message\":%s}}\n", id, quoted)
}
