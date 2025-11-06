package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/mcpserver/resources"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// SetupMCPServer configures the MCP server with AI-powered tools
func SetupMCPServer(bridge interfaces.BridgeInterface) *server.MCPServer {
	// Declare mcpServer early so hooks can reference it
	var mcpServerInstance *server.MCPServer

	hooks := &server.Hooks{}

	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		logger.Debug("beforeAny:", method, id, message)
	})
	hooks.AddOnSuccess(func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
		logger.Debug("onSuccess:", method, id, message, result)
	})
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		logger.Error("onError:", method, id, message, err)
	})
	hooks.AddBeforeInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest) {
		logger.Debug("beforeInitialize:", id, message)
	})
	hooks.AddOnRequestInitialization(func(ctx context.Context, id any, message any) error {
		idStr := fmt.Sprintf("%v", id)

		var prettyJSON bytes.Buffer
		switch v := message.(type) {
		case []byte:
			if err := json.Indent(&prettyJSON, v, "", "  "); err != nil {
				prettyJSON.Write(v) // fallback if not valid JSON
			}
		case string:
			if err := json.Indent(&prettyJSON, []byte(v), "", "  "); err != nil {
				prettyJSON.WriteString(v) // fallback
			}
		default:
			jsonData, _ := json.MarshalIndent(v, "", "  ")
			prettyJSON.Write(jsonData)
		}

		logger.Debug(fmt.Sprintf("AddOnRequestInitialization: id=%s, message=%s", idStr, prettyJSON.String()))
		return nil
	})
	hooks.AddAfterInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {
		logger.Debug("afterInitialize:", id, message, result)
		logger.Info("Initialization complete, waiting for initialized notification...")
	})
	hooks.AddAfterCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest, result *mcp.CallToolResult) {
		logger.Debug("afterCallTool:", id, message, result)
	})
	hooks.AddBeforeCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest) {
		logger.Debug("beforeCallTool:", id, message)
	})

	mcpServerInstance = server.NewMCPServer(
		"mcp-lsp-bridge",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true), // Enable resources with subscribe support
		server.WithLogging(),
		server.WithHooks(hooks),
		server.WithInstructions(`This MCP server provides comprehensive Language Server Protocol (LSP) integration for advanced code analysis and manipulation across multiple programming languages.

## Key Capabilities & Usage Flow

The bridge offers robust tools for:

1.  **Project Initialization**: Detect project languages and connect to relevant language servers.

2.  **Document Lifecycle Management** (IMPORTANT):
    *   **document_open**: Explicitly open a document in the language server - REQUIRED before diagnostics/hover/etc.
    *   **document_change**: Send modified content to analyze without writing to disk
    *   **document_save**: Trigger full re-analysis (useful for some language servers)
    *   **document_close**: Clean up when done to free resources

    **BEST PRACTICE WORKFLOW**:
    Step 1: document_open (triggers analysis, wait 1-2 seconds)
    Step 2: document_diagnostics (get cached results)
    Step 3: document_close (cleanup when done)

3.  **Code Discovery & Understanding**:
    *   **Symbol Search**: Locate definitions, references, and usage of symbols project-wide.
    *   **Contextual Help**: Get documentation and type information for code elements.
    *   **Code Content**: Extract specific code blocks by range.
    *   **Diagnostics**: Identify errors, warnings, and overall project health.
    *   Note: Most operations auto-open documents if not already open, but explicit lifecycle management is recommended for better performance.

4.  **Code Improvement & Navigation**:
    *   **Refactoring**: Apply quick fixes, suggestions, and safely rename symbols (preview changes first).
    *   **Formatting**: Standardize code style.
    *   **Navigation**: Trace implementations and function call hierarchies.

5.  **Resource Management**: Disconnect language servers when analysis is complete.

## Multi-Language Support
The bridge automatically detects file types and connects to appropriate language servers. It supports fallback mechanisms and provides actionable error messages.

## Diagnostic Architecture (Push + Pull)
- **Push**: Language servers send diagnostics automatically after didOpen/didChange/didSave
- **Pull**: Explicitly request diagnostics via document_diagnostics tool
- Diagnostics are cached for fast retrieval
- Most language servers support push diagnostics (TypeScript, ESLint, etc.)`),
	)

	// Register all resources using the auto-registration system
	// Resources are defined in mcpserver/resources/*.go files and auto-register via init()
	if err := resources.Registry.Build(mcpServerInstance, bridge); err != nil {
		logger.Error("Failed to build resource registry", err)
	}

	// Register all MCP tools
	RegisterAllTools(mcpServerInstance, bridge)

	// Set up default session for clients that don't explicitly create sessions
	setupDefaultSession(mcpServerInstance)

	// Register handler for "notifications/initialized" to request roots after handshake completes
	// This follows the MCP protocol: client sends initialized notification AFTER receiving initialize response
	mcpServerInstance.AddNotificationHandler("notifications/initialized", func(ctx context.Context, notification mcp.JSONRPCNotification) {
		logger.Info("Received initialized notification from client")

		// Get the client session from context to access client capabilities
		session := server.ClientSessionFromContext(ctx)
		if session == nil {
			logger.Warn("No client session found in context")
			return
		}

		// Check if session has client capabilities (it should after initialize)
		sessionWithCaps, ok := session.(interface{ GetClientCapabilities() mcp.ClientCapabilities })
		if !ok {
			logger.Warn("Client session does not support GetClientCapabilities")
			return
		}

		clientCaps := sessionWithCaps.GetClientCapabilities()
		if clientCaps.Roots == nil {
			logger.Info("Client does not support roots capability, using working directory")
			return
		}

		logger.Info("Client supports roots capability, requesting roots...")

		// Add panic recovery to catch any crashes
		defer func() {
			if r := recover(); r != nil {
				logger.Error(fmt.Sprintf("PANIC in roots request handler: %v", r))
				logger.Error("Stack trace will be in stderr if available")
			}
		}()

		// Verify context and server instance are not nil
		logger.Debug(fmt.Sprintf("Roots handler state: ctx=%v, mcpServerInstance=%v", ctx != nil, mcpServerInstance != nil))

		// Request roots from the client (safe to do now that handshake is complete)
		// Use a timeout to avoid hanging if client doesn't respond
		logger.Debug("Creating timeout context for roots request...")
		rootsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		logger.Debug("Timeout context created successfully")

		logger.Debug("Calling mcpServerInstance.RequestRoots()...")
		rootsResult, err := mcpServerInstance.RequestRoots(rootsCtx, mcp.ListRootsRequest{})
		logger.Debug(fmt.Sprintf("RequestRoots returned: err=%v, result=%v, rootsCount=%d",
			err,
			rootsResult != nil,
			func() int { if rootsResult != nil { return len(rootsResult.Roots) }; return 0 }()))

		if err != nil {
			logger.Error(fmt.Sprintf("Failed to request roots from client: %v", err))
			logger.Info("Falling back to working directory for workspace roots")
			return
		}

		if rootsResult != nil && len(rootsResult.Roots) > 0 {
			logger.Info(fmt.Sprintf("Received %d root(s) from client:", len(rootsResult.Roots)))
			for i, root := range rootsResult.Roots {
				logger.Info(fmt.Sprintf("  Root %d: %s (name: %s)", i+1, root.URI, root.Name))
			}

			// Update bridge with the workspace roots
			logger.Debug("Calling updateBridgeWithRoots()...")
			updateBridgeWithRoots(bridge, rootsResult.Roots)
			logger.Debug("updateBridgeWithRoots() completed")
		} else {
			logger.Info("Client returned no roots, using working directory")
		}
	})

	return mcpServerInstance
}

// updateBridgeWithRoots updates the bridge's workspace roots from MCP roots
func updateBridgeWithRoots(bridge interfaces.BridgeInterface, roots []mcp.Root) {
	if len(roots) == 0 {
		return
	}

	// Convert file:// URIs to filesystem paths
	var workspacePaths []string
	for _, root := range roots {
		// URI format: file:///path/to/workspace
		// Strip "file://" prefix to get the path
		if len(root.URI) > 7 && root.URI[:7] == "file://" {
			path := root.URI[7:] // Remove "file://"
			workspacePaths = append(workspacePaths, path)
			logger.Info(fmt.Sprintf("Added workspace root: %s", path))
		} else {
			logger.Warn(fmt.Sprintf("Skipping non-file URI root: %s", root.URI))
		}
	}

	// Update bridge with workspace paths
	if len(workspacePaths) > 0 {
		bridge.SetWorkspaceRoots(workspacePaths)
		logger.Info(fmt.Sprintf("Successfully updated bridge with %d workspace root(s)", len(workspacePaths)))
	}
}

// setupDefaultSession creates a default session for clients
func setupDefaultSession(mcpServer *server.MCPServer) {
	// Create a default session that clients can use
	defaultSession := NewLSPBridgeSession("default")

	if err := mcpServer.RegisterSession(context.Background(), defaultSession); err != nil {
		logger.Error("Failed to register default session", err)
	} else {
		logger.Info("Default session registered successfully")
	}
}

