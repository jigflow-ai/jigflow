package clitest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"testing"
)

// MCP is a scripted MCP client (Seam B): it runs `jfl mcp` in the project
// folder and speaks newline-delimited JSON-RPC 2.0 over its stdin and stdout.
type MCP struct {
	t      testing.TB
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	nextID int
}

// RPCError is a JSON-RPC error response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message) }

// StartMCP starts `jfl mcp` as the agent session with the given id, or with
// no JFL_SESSION when it is empty, and completes the MCP handshake. The
// server is stopped when the test ends.
func (p *Project) StartMCP(session string) *MCP {
	p.t.Helper()
	cmd := exec.Command(p.bin.Jfl, "mcp")
	cmd.Dir = p.Dir
	cmd.Env = p.env(session)
	in, err := cmd.StdinPipe()
	if err != nil {
		p.t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		p.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	m := &MCP{t: p.t, cmd: cmd, in: in, out: bufio.NewReader(out)}
	p.t.Cleanup(m.Close)

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := m.Call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "clitest", "version": "0"},
	}, &init); err != nil {
		p.t.Fatalf("initialize: %v", err)
	}
	m.Notify("notifications/initialized")
	return m
}

// Close ends the session by closing the server's stdin, and waits for it.
func (m *MCP) Close() {
	_ = m.in.Close()
	_ = m.cmd.Wait()
}

// Notify sends a notification, which gets no response.
func (m *MCP) Notify(method string) {
	m.t.Helper()
	m.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// Call sends a request and decodes its result into result. A JSON-RPC error
// response is returned as an *RPCError.
func (m *MCP) Call(method string, params, result any) error {
	m.t.Helper()
	m.nextID++
	id := m.nextID
	m.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	line, err := m.out.ReadBytes('\n')
	if err != nil {
		m.t.Fatalf("%s: no response from jfl mcp: %v", method, err)
	}
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		m.t.Fatalf("%s: response is not JSON: %v\n%s", method, err, line)
	}
	if resp.JSONRPC != "2.0" || resp.ID != id {
		m.t.Fatalf("%s: response has jsonrpc %q and id %d, want \"2.0\" and %d\n%s", method, resp.JSONRPC, resp.ID, id, line)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			m.t.Fatalf("%s: result doesn't decode: %v\n%s", method, err, line)
		}
	}
	return nil
}

// Tools returns the names of the tools the server lists.
func (m *MCP) Tools() []string {
	m.t.Helper()
	var res struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := m.Call("tools/list", map[string]any{}, &res); err != nil {
		m.t.Fatalf("tools/list: %v", err)
	}
	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}
	return names
}

// ToolResult is the outcome of one tools/call: its text content, and
// whether the tool reported an error, such as a refusal.
type ToolResult struct {
	Text    string
	IsError bool
}

// CallTool calls the named tool with the given arguments. A JSON-RPC error
// response, such as for a tool the server doesn't have, is returned as an
// *RPCError.
func (m *MCP) CallTool(name string, args map[string]any) (ToolResult, error) {
	m.t.Helper()
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := m.Call("tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return ToolResult{}, err
	}
	r := ToolResult{IsError: res.IsError}
	for _, c := range res.Content {
		if c.Type == "text" {
			r.Text += c.Text
		}
	}
	return r, nil
}

// MustCallTool calls the named tool and fails the test on a JSON-RPC error.
func (m *MCP) MustCallTool(name string, args map[string]any) ToolResult {
	m.t.Helper()
	r, err := m.CallTool(name, args)
	if err != nil {
		m.t.Fatalf("tools/call %s: %v", name, err)
	}
	return r
}

func (m *MCP) send(msg map[string]any) {
	m.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		m.t.Fatal(err)
	}
	if _, err := m.in.Write(append(data, '\n')); err != nil {
		m.t.Fatalf("writing to jfl mcp: %v", err)
	}
}
