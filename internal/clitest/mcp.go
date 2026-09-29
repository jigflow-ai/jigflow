package clitest

import (
	"bufio"
	"encoding/json"
	"errors"
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
	// Protocol is the MCP version the server negotiated at initialize.
	Protocol string
	answers  []FormAnswer
	forms    []Form
}

// MCPClient is what a scripted MCP client declares at initialize.
type MCPClient struct {
	// Protocol is the MCP version it offers: 2025-06-18 when empty.
	Protocol string
	// Elicitation declares the elicitation capability, in form mode.
	Elicitation bool
}

// Form is an elicitation/create request the server sent the client: the
// message it asks the person to read, and the schema of their answer.
type Form struct {
	Message string
	Mode    string
	Schema  map[string]any
}

// FormAnswer is how the scripted client answers a form.
type FormAnswer struct {
	action     string
	content    map[string]any
	fail       bool
	cancelCall bool
}

// Accept answers a form as a person submitting it with decision chosen.
func Accept(decision string) FormAnswer {
	return FormAnswer{action: "accept", content: map[string]any{"decision": decision}}
}

// AcceptWith answers a form as accepted with content as it stands, such as
// a decision that isn't one of the form's choices.
func AcceptWith(content map[string]any) FormAnswer {
	return FormAnswer{action: "accept", content: content}
}

var (
	// Decline answers a form as a person, or a client policy, declining it.
	Decline = FormAnswer{action: "decline"}
	// Cancel answers a form as a person dismissing it.
	Cancel = FormAnswer{action: "cancel"}
	// FailForm answers a form with a JSON-RPC error, as a client that can't
	// show it would.
	FailForm = FormAnswer{fail: true}
	// CancelCall doesn't answer the form: the client cancels the request
	// that led to it instead, and Call returns ErrCancelled at once.
	CancelCall = FormAnswer{cancelCall: true}
)

// ErrCancelled is returned by Call when the client cancelled the request.
var ErrCancelled = errors.New("the client cancelled the request")

// RPCError is a JSON-RPC error response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message) }

// StartMCP starts `jfl mcp` as the agent session with the given id, or with
// no JFL_SESSION when it is empty, and completes the MCP handshake as a
// client with no elicitation. The server is stopped when the test ends.
func (p *Project) StartMCP(session string) *MCP {
	p.t.Helper()
	return p.StartMCPWith(session, MCPClient{})
}

// StartMCPWith starts `jfl mcp` as StartMCP does, completing the handshake
// as a client that declares what c says.
func (p *Project) StartMCPWith(session string, c MCPClient) *MCP {
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

	if c.Protocol == "" {
		c.Protocol = "2025-06-18"
	}
	capabilities := map[string]any{}
	if c.Elicitation {
		capabilities["elicitation"] = map[string]any{}
		if c.Protocol >= "2025-11-25" {
			capabilities["elicitation"] = map[string]any{"form": map[string]any{}}
		}
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := m.Call("initialize", map[string]any{
		"protocolVersion": c.Protocol,
		"capabilities":    capabilities,
		"clientInfo":      map[string]any{"name": "clitest", "version": "0"},
	}, &init); err != nil {
		p.t.Fatalf("initialize: %v", err)
	}
	m.Protocol = init.ProtocolVersion
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

// Answer scripts how the client answers the next forms the server asks it
// to show, one answer each, in order. A form with no answer left is failed.
func (m *MCP) Answer(answers ...FormAnswer) { m.answers = append(m.answers, answers...) }

// Forms returns every form the server has asked the client to show.
func (m *MCP) Forms() []Form { return m.forms }

// Call sends a request and decodes its result into result. A JSON-RPC error
// response is returned as an *RPCError. Requests the server sends while it
// answers, such as elicitation/create, are answered as scripted.
func (m *MCP) Call(method string, params, result any) error {
	m.t.Helper()
	m.nextID++
	id := m.nextID
	m.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   *RPCError       `json:"error"`
	}
	var line []byte
	for {
		var err error
		line, err = m.out.ReadBytes('\n')
		if err != nil {
			m.t.Fatalf("%s: no response from jfl mcp: %v", method, err)
		}
		resp.Method, resp.ID, resp.Result, resp.Error = "", nil, nil, nil
		if err := json.Unmarshal(line, &resp); err != nil {
			m.t.Fatalf("%s: response is not JSON: %v\n%s", method, err, line)
		}
		if resp.Method == "" {
			break
		}
		if len(resp.ID) > 0 && !m.serve(resp.ID, resp.Method, resp.Params) {
			m.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id, "reason": "the test cancelled it"}})
			return ErrCancelled
		}
	}
	if resp.JSONRPC != "2.0" || string(resp.ID) != fmt.Sprint(id) {
		m.t.Fatalf("%s: response has jsonrpc %q and id %s, want \"2.0\" and %d\n%s", method, resp.JSONRPC, resp.ID, id, line)
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

// serve answers a request the server sent the client: an elicitation/create
// as scripted, recording its form, and any other with method not found. It
// reports false when the script cancels the client's own request instead.
func (m *MCP) serve(id json.RawMessage, method string, params json.RawMessage) bool {
	m.t.Helper()
	if method != "elicitation/create" {
		m.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": RPCError{-32601, "method not found: " + method}})
		return true
	}
	var f struct {
		Message         string         `json:"message"`
		Mode            string         `json:"mode"`
		RequestedSchema map[string]any `json:"requestedSchema"`
	}
	if err := json.Unmarshal(params, &f); err != nil {
		m.t.Fatalf("elicitation/create params don't decode: %v\n%s", err, params)
	}
	m.forms = append(m.forms, Form{Message: f.Message, Mode: f.Mode, Schema: f.RequestedSchema})
	a := FailForm
	if len(m.answers) > 0 {
		a, m.answers = m.answers[0], m.answers[1:]
	}
	switch {
	case a.cancelCall:
		return false
	case a.fail:
		m.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": RPCError{-32603, "the client couldn't show the form"}})
		return true
	}
	result := map[string]any{"action": a.action}
	if a.content != nil {
		result["content"] = a.content
	}
	m.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return true
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
