package cli

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// mcpProtocol is the MCP version the server answers with when the client
// asks for none.
const mcpProtocol = "2025-06-18"

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
}

// mcpTools is the agent-safe surface of the CLI. Human Transitions, approve
// and reject aren't in it, so an agent using MCP never sees them (ADR 0003);
// the engine refuses a Human Transition asked of move, as it does in the CLI.
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
		Description: "Put forward the creations and Transitions in a Proposal file for a person to approve or reject as one unit, like `jfl propose <file>`. The file is YAML: a summary and items, each {create: <Type>, ref, title, status, links} or {move: <id>, to: <status>}; creations may Link to each other by ref. An item may change the Playbook instead: {gate: <name>, cmd: <command>} gives the Gates of that name their command, {guideline: <name>, text: <Markdown>} adds a Guideline.",
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

// cmdMcp runs the commands of its tools through Run, which dispatches
// through commands, so it joins commands here rather than in their
// declaration, which would be an initialization cycle.
func init() { commands["mcp"] = cmdMcp }

// cmdMcp serves the agent-safe surface of the CLI as an MCP server over
// stdio: newline-delimited JSON-RPC 2.0 on stdin and stdout, until stdin
// closes. Each tool call runs its jfl command as the CLI does, so it is
// decided, and refused, exactly as there.
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
	s := &mcpServer{e: e, session: session, out: json.NewEncoder(e.stdout)}
	in := bufio.NewScanner(e.stdin)
	in.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for in.Scan() {
		if len(bytes.TrimSpace(in.Bytes())) == 0 {
			continue
		}
		if err := s.handle(in.Bytes()); err != nil {
			return err
		}
	}
	return in.Err()
}

type mcpServer struct {
	e       *env
	session string
	out     *json.Encoder
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
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
	if len(req.ID) == 0 {
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return s.reply(req.ID, nil, &rpcError{rpcInvalidRequest, "not a JSON-RPC 2.0 request"})
	}
	result, rerr := s.call(req.Method, req.Params)
	return s.reply(req.ID, result, rerr)
}

func (s *mcpServer) call(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var in struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &in)
		version := in.ProtocolVersion
		if version == "" {
			version = mcpProtocol
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "jigflow", "version": Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
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
	i := slices.IndexFunc(mcpTools, func(t mcpTool) bool { return t.Name == in.Name })
	if i < 0 {
		return nil, &rpcError{rpcInvalidParams, fmt.Sprintf("unknown tool %q", in.Name)}
	}
	args, err := mcpTools[i].args(in.Arguments)
	if err != nil {
		return nil, &rpcError{rpcInvalidParams, fmt.Sprintf("%s: %v", in.Name, err)}
	}
	getenv := func(key string) string {
		switch key {
		case SessionEnv:
			return s.session
		case clockEnv:
			return s.e.getenv(key)
		}
		return ""
	}
	var out bytes.Buffer
	code := Run(args, s.e.dir, getenv, nil, &out, &out)
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": out.String()}},
		"isError": code != exitOK,
	}, nil
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
