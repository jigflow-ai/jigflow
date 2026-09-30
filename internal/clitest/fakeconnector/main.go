// Command fakeconnector is a Connector for tests: it speaks the Connector
// protocol (docs/connector-protocol.md) over stdio, keeping its tracker in a
// JSON file and appending every request it gets to a log, one JSON object
// per line, so a test can see what jfl asked of the tracker.
//
// Both files are named by the Connector settings, relative to the project
// root it runs in:
//
//	settings: {tracker: tracker.json, log: calls.jsonl}
//
// The tracker file can make the Connector fail, to simulate a tracker
// problem: "fail" is the error kind to report, and "fail_on", when set, the
// only operation that fails. "fail" set to "crash" exits non-zero without
// a response, breaking the protocol.
//
// It answers describe as an unknown operation, as a Connector written
// before it did, unless its args give it a file, relative to the project
// root, that holds the answer, which it then sends as it is:
//
//	args: [--describe=describe.json]
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

type item struct {
	ID       string                         `json:"id"`
	Title    string                         `json:"title"`
	Body     string                         `json:"body,omitempty"`
	Labels   []string                       `json:"labels,omitempty"`
	State    string                         `json:"state,omitempty"`
	Claim    string                         `json:"claim,omitempty"`
	Links    map[string][]map[string]string `json:"links,omitempty"`
	Comments []string                       `json:"comments,omitempty"`
}

type tracker struct {
	Next   int    `json:"next"`
	Items  []item `json:"items"`
	Fail   string `json:"fail,omitempty"`
	FailOn string `json:"fail_on,omitempty"`
}

type term struct {
	Label string `json:"label"`
	State string `json:"state"`
}

type request struct {
	Protocol int            `json:"protocol"`
	Op       string         `json:"op"`
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings"`
	ID       string         `json:"id"`
	Item     item           `json:"item"`
	From     term           `json:"from"`
	To       term           `json:"to"`
	Claim    string         `json:"claim"`
	Body     string         `json:"body"`
}

// setting is the text setting name, or empty: a setting may be of any kind,
// such as yes/no, since jfl sends settings as they are.
func (r request) setting(name string) string {
	s, _ := r.Settings[name].(string)
	return s
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail("internal", err.Error())
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		fail("invalid", err.Error())
	}
	if req.Op == "describe" {
		for _, arg := range os.Args[1:] {
			if file, ok := strings.CutPrefix(arg, "--describe="); ok {
				answer, err := os.ReadFile(file)
				if err != nil {
					fail("internal", err.Error())
				}
				os.Stdout.Write(answer)
				return
			}
		}
		fail("invalid", "unknown op describe")
	}
	if f, err := os.OpenFile(req.setting("log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "%s\n", compact(raw))
		f.Close()
	}
	var tr tracker
	if data, err := os.ReadFile(req.setting("tracker")); err == nil {
		if err := json.Unmarshal(data, &tr); err != nil {
			fail("internal", err.Error())
		}
	}
	if tr.Fail != "" && (tr.FailOn == "" || tr.FailOn == req.Op) {
		if tr.Fail == "crash" {
			fmt.Fprintln(os.Stderr, "fakeconnector: crashed")
			os.Exit(2)
		}
		fail(tr.Fail, "the fake tracker says "+tr.Fail)
	}
	find := func() *item {
		i := slices.IndexFunc(tr.Items, func(it item) bool { return it.ID == req.ID })
		if i < 0 {
			fail("not_found", "no item "+req.ID)
		}
		return &tr.Items[i]
	}
	var resp any = map[string]any{}
	switch req.Op {
	case "list":
		resp = map[string]any{"items": tr.Items}
	case "get":
		resp = map[string]any{"item": find()}
	case "create":
		if tr.Next == 0 {
			tr.Next = 1
		}
		it := req.Item
		it.ID = strconv.Itoa(tr.Next)
		tr.Next++
		tr.Items = append(tr.Items, it)
		resp = map[string]any{"item": it}
	case "status":
		it := find()
		it.Labels = slices.DeleteFunc(it.Labels, func(l string) bool { return l == req.From.Label })
		if req.To.Label != "" && !slices.Contains(it.Labels, req.To.Label) {
			it.Labels = append(it.Labels, req.To.Label)
		}
		if req.To.State != "" {
			it.State = req.To.State
		}
	case "claim":
		find().Claim = req.Claim
	case "comment":
		it := find()
		it.Comments = append(it.Comments, req.Body)
	default:
		fail("invalid", "unknown op "+req.Op)
	}
	data, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		fail("internal", err.Error())
	}
	if err := os.WriteFile(req.setting("tracker"), data, 0o644); err != nil {
		fail("internal", err.Error())
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}

// fail answers with a protocol error of the given kind and exits.
func fail(kind, msg string) {
	e := map[string]any{"kind": kind, "message": msg}
	if kind == "rate_limit" {
		e["retry_after"] = 30
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"error": e})
	os.Exit(0)
}

func compact(raw []byte) []byte {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	out, _ := json.Marshal(v)
	return out
}
