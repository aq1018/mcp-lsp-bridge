package bridge

import (
	"sync"

	"rockerboo/mcp-lsp-bridge/types"

	"github.com/mark3labs/mcp-go/server"
)

// MCPLSPBridge combines MCP server capabilities with multiple LSP clients
type MCPLSPBridge struct {
	server              *server.MCPServer
	clients             map[types.LanguageServer]types.LanguageClientInterface
	config              types.LSPServerConfigProvider
	allowedDirectories  []string
	workspaceRoots      []string // MCP workspace roots from client
	diagnosticsCallback func(uri string)
	mu                  sync.RWMutex
}

// serverResult holds the result from querying a single language server
type serverResult struct {
	ServerName types.LanguageServer
	Result     interface{}
	Error      error
}
