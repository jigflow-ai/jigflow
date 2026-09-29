package cli

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// The MCP versions the server speaks. Both carry server-sent
// elicitation/create in form mode (ADR 0024).
const (
	mcpProtocol       = "2025-06-18" // what a client offering any older version, or none, gets
	mcpLatestProtocol = "2025-11-25" // what a client offering it, or a later one, gets
)

// JSON-RPC error codes.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcNoMethod       = -32601
	rpcInvalidParams  = -32602
)

// mcpTool is one tool of the MCP server: a jfl command an agent may run.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// args turns the tool's arguments into the jfl command line it runs.
	args func(json.RawMessage) ([]string, error)
	// confirms is set on a tool whose command needs a person's
	// Confirmation: it runs as the person, who gives it in a form the
	// client shows only to them, so it is listed only to a client that
	// declared elicitation (ADR 0024).
	confirms bool
	// asks is set on a tool whose command runs as this session and then,
	// where the client can show a form, asks the person for a
	// Confirmation in one.
	asks bool
}

// mcpTools is the agent-safe surface of the CLI, and approve, which asks the
// person for a Confirmation in a form the agent's client shows only to them
// (ADR 0024); where the client can show one, propose asks for one too, at
// once. A client that can't show the person a form isn't offered
// approve, and no client is offered reject, so an agent using MCP can't
// decide for the person (ADR 0003); the engine refuses a Human Transition
// asked of move, as it does in the CLI.
var mcpTools = []mcpTool{
	{
		Name:        "next",
		Description: "Say which Skill to run on which Artifact, and make the pick this session's Focus, like `jfl next`. With autopilot, one step of autopilot, like `jfl next --autopilot`: repeat it, running the Skill it names, until it says \"autopilot stopped\".",
		InputSchema: schema(nil, map[string]any{"autopilot": map[string]any{"type": "boolean", "description": "one step of autopilot"}}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct{ Autopilot bool }
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if in.Autopilot {
				return []string{"next", "--autopilot"}, nil
			}
			return []string{"next"}, nil
		},
	},
	{
		Name:        "move",
		Description: "Move an Artifact through a declared Transition that isn't a Human Transition, like `jfl move <id> <status>`: its Gates run before and its Actions after, and it Claims the Artifact for this session. A Human Transition is refused: put it in a Proposal instead.",
		InputSchema: schema([]string{"id", "status"}, map[string]any{
			"id":     map[string]any{"type": "string", "description": "the Artifact's id, e.g. T-1"},
			"status": map[string]any{"type": "string", "description": "the Status to move it to"},
		}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct{ ID, Status string }
			if err := decodeArgs(raw, &in, "id", "status"); err != nil {
				return nil, err
			}
			return []string{"move", in.ID, in.Status}, nil
		},
	},
	{
		Name:        "propose",
		Description: "Put forward the creations and Transitions in a Proposal file for a person to approve or reject as one unit, like `jfl propose <file>`. The file is YAML: a summary and items, each {create: <Type>, ref, title, status, fields, links} or {move: <id>, to: <status>}; creations may Link to each other by ref. An item may change the Playbook instead: {gate: <name>, cmd: <command>} gives the Gates of that name their command, {guideline: <name>, text: <Markdown>} adds a Guideline, {type: <Type>, text: <its file's YAML>} declares or replaces an Artifact Type, {skill: <name>, text: <its SKILL.md>} writes a Skill; a Proposal whose Playbook would fail jfl check is refused. If your client can show the person a form, they are asked at once to approve or reject it, as the approve tool asks, and the result says what they decided; otherwise, or if they put it off, the result says where it waits for them.",
		InputSchema: schema([]string{"file"}, map[string]any{
			"file": map[string]any{"type": "string", "description": "the Proposal file, relative to the project root"},
		}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct{ File string }
			if err := decodeArgs(raw, &in, "file"); err != nil {
				return nil, err
			}
			return []string{"propose", in.File}, nil
		},
		asks: true,
	},
	{
		Name:        "approve",
		Description: "Ask the person to approve or reject a pending Proposal, in a form your client shows only to them; jfl writes the form from the Proposal itself. Approved, it is applied as one unit, as `jfl approve <proposal>` confirmed in a terminal applies it; rejected, it is dropped, as `jfl reject <proposal>` drops it. A form the person dismisses or declines leaves the Proposal pending. A Proposal making a Transition the Playbook requires the Dashboard for is refused without asking.",
		InputSchema: schema([]string{"proposal"}, map[string]any{
			"proposal": map[string]any{"type": "string", "description": "the pending Proposal's id, e.g. P-1"},
		}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct{ Proposal string }
			if err := decodeArgs(raw, &in, "proposal"); err != nil {
				return nil, err
			}
			return []string{"approve", in.Proposal}, nil
		},
		confirms: true,
	},
	{
		Name:        "query",
		Description: "List the Artifacts, with their Type, title, Status, Claim and Links, like `jfl query`, optionally only those of one Type or in one Status.",
		InputSchema: schema(nil, map[string]any{
			"type":   map[string]any{"type": "string", "description": "only Artifacts of this Artifact Type"},
			"status": map[string]any{"type": "string", "description": "only Artifacts in this Status"},
		}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct{ Type, Status string }
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			args := []string{"query"}
			if in.Type != "" {
				args = append(args, "--type="+in.Type)
			}
			if in.Status != "" {
				args = append(args, "--status="+in.Status)
			}
			return args, nil
		},
	},
	{
		Name:        "show",
		Description: "Print an Artifact as query lists it, then its body and the comments its tracker keeps, like `jfl show <id>`.",
		InputSchema: schema([]string{"id"}, map[string]any{
			"id": map[string]any{"type": "string", "description": "the Artifact's id"},
		}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct{ ID string }
			if err := decodeArgs(raw, &in, "id"); err != nil {
				return nil, err
			}
			return []string{"show", in.ID}, nil
		},
	},
	{
		Name:        "create",
		Description: "File an Artifact into an Inbox, like `jfl create <Type> --title <title> --status <status>`. Creating into any other Status that has a Binding is refused: put it in a Proposal instead.",
		InputSchema: schema([]string{"type", "title"}, map[string]any{
			"type":   map[string]any{"type": "string", "description": "the Artifact Type"},
			"title":  map[string]any{"type": "string", "description": "the new Artifact's title"},
			"status": map[string]any{"type": "string", "description": "the Status to create it in (default: the Type's first initial Status)"},
			"links": map[string]any{
				"type":                 "object",
				"description":          "Links to other Artifacts: Link name -> their ids",
				"additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		}),
		args: func(raw json.RawMessage) ([]string, error) {
			var in struct {
				Type, Title, Status string
				Links               map[string][]string
			}
			if err := decodeArgs(raw, &in, "type", "title"); err != nil {
				return nil, err
			}
			args := []string{"create", in.Type, "--title=" + in.Title}
			if in.Status != "" {
				args = append(args, "--status="+in.Status)
			}
			for _, name := range slices.Sorted(maps.Keys(in.Links)) {
				for _, id := range in.Links[name] {
					args = append(args, "--link="+name+"="+id)
				}
			}
			return args, nil
		},
	},
}

// schema is the JSON Schema of a tool's arguments.
func schema(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// decodeArgs decodes a tool's arguments into v and checks that each of the
// required ones is a non-empty string.
func decodeArgs(raw json.RawMessage, v any, required ...string) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return err
	}
	var present map[string]any
	_ = json.Unmarshal(raw, &present)
	for _, name := range required {
		if s, _ := present[name].(string); s == "" {
			return fmt.Errorf("missing argument %q", name)
		}
	}
	return nil
}

// cmdMcp runs the commands of its tools through run, which dispatches
// through commands, so it joins commands here rather than in their
// declaration, which would be an initialization cycle.
func init() { commands["mcp"] = cmdMcp }

// cmdMcp serves the agent-safe surface of the CLI as an MCP server over
// stdio: newline-delimited JSON-RPC 2.0 on stdin and stdout, until stdin
// closes. Each tool call runs its jfl command as the CLI does, so it is
// decided, and refused, exactly as there. A tool that needs a Confirmation
// asks the client, while the call is in flight, to show the person a form
// (elicitation/create), and runs its command as the person, confirmed by
// their answer (ADR 0024).
//
// The server is always an agent session: JFL_SESSION's id when it is set,
// so its Claims are shared with the session's jfl commands, and otherwise a
// fresh id for the life of the server.
func cmdMcp(e *env, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: jfl mcp takes no arguments", errUsage)
	}
	session := e.actor.Session
	if session == "" {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		session = "mcp-" + hex.EncodeToString(b)
	}
	in := bufio.NewScanner(e.stdin)
	in.Buffer(make([]byte, 64*1024), 16*1024*1024)
	s := &mcpServer{e: e, session: session, in: in, out: json.NewEncoder(e.stdout)}
	for {
		line, ok := s.read()
		if !ok {
			return in.Err()
		}
		if err := s.handle(line); err != nil {
			return err
		}
	}
}

type mcpServer struct {
	e        *env
	session  string
	in       *bufio.Scanner
	out      *json.Encoder
	protocol string // the MCP version negotiated at initialize
	// forms is whether the client declared elicitation in form mode at
	// initialize: whether it can show the person a form.
	forms bool
	// queued are the client's messages that came while the server waited
	// for the person's answer to a form, to handle once the tool call is
	// done.
	queued [][]byte
	// requests counts the requests the server has sent the client, giving
	// each its id.
	requests int
	// inFlight is the id of the tools/call being answered, and cancelled is
	// set once the client cancels it, so that it gets no response.
	inFlight  json.RawMessage
	cancelled bool
}

// read returns the client's next message: one queued, or else the next one
// on stdin. It reports false when stdin has closed.
func (s *mcpServer) read() ([]byte, bool) {
	if len(s.queued) > 0 {
		line := s.queued[0]
		s.queued = s.queued[1:]
		return line, true
	}
	return s.readClient()
}

// readClient returns the client's next line on stdin that isn't blank,
// never a queued one. It reports false when stdin has closed.
func (s *mcpServer) readClient() ([]byte, bool) {
	for s.in.Scan() {
		if len(bytes.TrimSpace(s.in.Bytes())) > 0 {
			return bytes.Clone(s.in.Bytes()), true
		}
	}
	return nil, false
}

// tools are the tools the client may call: those that ask for a
// Confirmation only when it can show the person a form.
func (s *mcpServer) tools() []mcpTool {
	return slices.DeleteFunc(slices.Clone(mcpTools), func(t mcpTool) bool { return t.confirms && !s.forms })
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	// Result and Error make it a response, to a request the server sent.
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// handle answers one JSON-RPC message. Notifications get no answer.
func (s *mcpServer) handle(line []byte) error {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return s.reply(json.RawMessage("null"), nil, &rpcError{rpcParseError, err.Error()})
	}
	// A notification gets no answer, nor does a response: one to a form
	// the server stopped waiting for.
	if len(req.ID) == 0 || req.Method == "" && (req.Result != nil || req.Error != nil) {
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return s.reply(req.ID, nil, &rpcError{rpcInvalidRequest, "not a JSON-RPC 2.0 request"})
	}
	s.inFlight, s.cancelled = req.ID, false
	result, rerr := s.call(req.Method, req.Params)
	s.inFlight = nil
	if s.cancelled {
		return nil
	}
	return s.reply(req.ID, result, rerr)
}

func (s *mcpServer) call(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var in struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				// Form and URL are the modes of 2025-11-25; a client
				// declaring neither supports form mode.
				Elicitation *struct {
					Form *struct{} `json:"form"`
					URL  *struct{} `json:"url"`
				} `json:"elicitation"`
			} `json:"capabilities"`
		}
		_ = json.Unmarshal(params, &in)
		el := in.Capabilities.Elicitation
		s.forms = el != nil && (el.Form != nil || el.URL == nil)
		// Versions are dates, so they sort as strings.
		version := mcpProtocol
		if in.ProtocolVersion >= mcpLatestProtocol {
			version = mcpLatestProtocol
		}
		s.protocol = version
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "jigflow", "version": Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools()}, nil
	case "tools/call":
		return s.callTool(params)
	}
	return nil, &rpcError{rpcNoMethod, fmt.Sprintf("unknown method %q", method)}
}

// callTool runs the jfl command of a tool as this session, with no stdin, so
// nothing it runs can ask for a person's confirmation. Its output, or its
// refusal as the CLI words it, is the tool's result.
func (s *mcpServer) callTool(params json.RawMessage) (any, *rpcError) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, &rpcError{rpcInvalidParams, err.Error()}
	}
	tools := s.tools()
	i := slices.IndexFunc(tools, func(t mcpTool) bool { return t.Name == in.Name })
	if i < 0 {
		return nil, &rpcError{rpcInvalidParams, fmt.Sprintf("unknown tool %q", in.Name)}
	}
	args, err := tools[i].args(in.Arguments)
	if err != nil {
		return nil, &rpcError{rpcInvalidParams, fmt.Sprintf("%s: %v", in.Name, err)}
	}
	// A tool that asks for a Confirmation runs its command as the person,
	// who gives it in the form; any other runs it as this session.
	tool := tools[i]
	session := s.session
	if tool.confirms {
		session = ""
	}
	getenv := func(key string) string {
		switch key {
		case SessionEnv:
			return session
		case clockEnv:
			return s.e.getenv(key)
		}
		return ""
	}
	var out bytes.Buffer
	cmd := &env{dir: s.e.dir, actor: engine.Actor{Session: session}, stdout: &out, stderr: &out, getenv: getenv}
	if s.forms && (tool.confirms || tool.asks) {
		cmd.form = s.elicit
	}
	code := cmd.run(args)
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": out.String()}},
		"isError": code != exitOK,
	}, nil
}

// elicit asks the client to show the person a form with message and one
// required field, decision, to choose among choices, and waits for their
// answer. Only an accepted form with one of the choices is an answer: a
// declined or cancelled one, or the client failing to show it, is an error,
// which leaves everything pending (ADR 0024). While it waits, it answers
// pings, and keeps the client's other messages for after the tool call, so
// that the person is asked one form at a time.
func (s *mcpServer) elicit(message string, choices ...string) (string, error) {
	s.requests++
	id, err := json.Marshal(fmt.Sprintf("jfl-%d", s.requests))
	if err != nil {
		return "", err
	}
	params := map[string]any{
		"message": message,
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"decision": map[string]any{"type": "string", "title": "Decision", "enum": choices},
			},
			"required": []string{"decision"},
		},
	}
	if s.protocol >= mcpLatestProtocol {
		params["mode"] = "form"
	}
	if err := s.out.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "elicitation/create", "params": params}); err != nil {
		return "", err
	}
	for {
		line, ok := s.readClient()
		if !ok {
			return "", errors.New("the client closed the connection before the person answered")
		}
		var head struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				RequestID json.RawMessage `json:"requestId"`
			} `json:"params"`
		}
		if json.Unmarshal(line, &head) != nil || head.Method != "" || !bytes.Equal(head.ID, id) {
			switch {
			case head.Method == "ping" && len(head.ID) > 0:
				if err := s.reply(head.ID, map[string]any{}, nil); err != nil {
					return "", err
				}
			case head.Method == "notifications/cancelled" && len(s.inFlight) > 0 && bytes.Equal(head.Params.RequestID, s.inFlight):
				// The client gave up on the tool call: it wants no result,
				// and the person's answer, if one comes, is ignored.
				s.cancelled = true
				return "", errors.New("the client cancelled the tool call before the person answered")
			default:
				s.queued = append(s.queued, line)
			}
			continue
		}
		var msg struct {
			Result *struct {
				Action  string `json:"action"`
				Content struct {
					Decision string `json:"decision"`
				} `json:"content"`
			} `json:"result"`
			Error *rpcError `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil {
			return "", errors.New("the client's answer to the form doesn't decode")
		}
		switch {
		case msg.Error != nil:
			return "", fmt.Errorf("the client couldn't ask the person (%s)", msg.Error.Message)
		case msg.Result == nil:
			return "", errors.New("the client answered the form with nothing")
		case msg.Result.Action == "decline":
			return "", errors.New("the form was declined")
		case msg.Result.Action == "cancel":
			return "", errors.New("the form was dismissed")
		case msg.Result.Action != "accept":
			return "", fmt.Errorf("the client answered the form with %q", msg.Result.Action)
		case !slices.Contains(choices, msg.Result.Content.Decision):
			return "", fmt.Errorf("the form was submitted with decision %q, not one of %s", msg.Result.Content.Decision, strings.Join(choices, ", "))
		}
		return msg.Result.Content.Decision, nil
	}
}

func (s *mcpServer) reply(id json.RawMessage, result any, rerr *rpcError) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		msg["error"] = rerr
	} else {
		msg["result"] = result
	}
	return s.out.Encode(msg)
}
