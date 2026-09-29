package adapter

import "fmt"

// claudeCodeMCP is the project's shared MCP configuration, committed, where
// Claude Code finds the MCP servers a project gives its agent. Only the
// server named mcpServerName is jfl's; the rest of it is the person's.
const claudeCodeMCP = ".mcp.json"

// mcpServerName is the name jfl's MCP server is registered by.
const mcpServerName = "jfl"

// claudeCodeMCPServer is the project's .mcp.json with jfl's MCP server in it.
func claudeCodeMCPServer() File {
	return File{Path: claudeCodeMCP, Merge: func(old string) (string, error) { return withMCPServer(old, true) }}
}

// withMCPServer is the MCP configuration in content with jfl's server, and
// only that, taken out, then, if add, put back in as a stdio server running
// `jfl mcp`. Everything else is kept, though its keys are written back in
// order. It is empty when nothing is left.
func withMCPServer(content string, add bool) (string, error) {
	config, err := decodeObject(claudeCodeMCP, content)
	if err != nil {
		return "", err
	}
	servers := map[string]any{}
	if s, ok := config["mcpServers"]; ok && s != nil {
		if servers, ok = s.(map[string]any); !ok {
			return "", fmt.Errorf("%s: mcpServers is not an object", claudeCodeMCP)
		}
	}
	delete(servers, mcpServerName)
	if add {
		servers[mcpServerName] = map[string]any{"type": "stdio", "command": "jfl", "args": []any{"mcp"}}
	}
	if len(servers) > 0 {
		config["mcpServers"] = servers
	} else {
		delete(config, "mcpServers")
	}
	return encodeObject(config)
}
