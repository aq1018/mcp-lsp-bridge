# LSP Client Capabilities Auto-Registration

This directory contains the auto-registering capability system for mcp-lsp-bridge. Client capabilities are automatically built from registered LSP features, ensuring that advertised capabilities always match what's actually implemented.

## Architecture

### Core Components

1. **`registry.go`** - Core capability registry
   - Manages all client capability registrations
   - Thread-safe registration methods
   - `Build()` generates final `protocol.ClientCapabilities`

2. **`features.go`** - Feature registrations
   - Registers all currently implemented LSP features
   - Uses `init()` to auto-register on package import
   - Self-documenting with comments for each feature

## How It Works

### Automatic Registration

When the bridge package imports `lsp/capabilities`, all features are automatically registered via `init()` functions:

```go
// features.go
func init() {
    // Automatically registers publishDiagnostics capability
    Registry.RegisterPublishDiagnostics(&protocol.PublishDiagnosticsClientCapabilities{
        RelatedInformation: true,
        TagSupport: &protocol.ClientDiagnosticsTagOptions{ValueSet: []protocol.DiagnosticTag{1, 2}},
        // ...
    })

    // Automatically registers hover capability
    Registry.RegisterHover(&protocol.HoverClientCapabilities{
        ContentFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
    })

    // ... all other features
}
```

### Usage in Bridge

The bridge simply calls `Registry.Build()`:

```go
// bridge/bridge.go
params := protocol.InitializeParams{
    Capabilities: capabilities.Registry.Build(),
    // ...
}
```

The registry automatically includes all registered features!

## Adding a New LSP Feature

**Step 1: Implement the LSP method** in `lsp/methods.go`:

```go
// lsp/methods.go
func (lc *LanguageClient) Completion(uri string, line, character uint32) (*protocol.CompletionList, error) {
    var result protocol.CompletionList

    err := lc.SendRequest("textDocument/completion", protocol.CompletionParams{
        TextDocument: protocol.TextDocumentIdentifier{Uri: protocol.DocumentUri(uri)},
        Position: protocol.Position{Line: line, Character: character},
    }, &result, 5*time.Second)

    return &result, err
}
```

**Step 2: Add bridge method** in `bridge/bridge.go`:

```go
// bridge/bridge.go
func (b *MCPLSPBridge) GetCompletion(uri string, line, character uint32) (*protocol.CompletionList, error) {
    language, err := b.InferLanguage(uri)
    if err != nil {
        return nil, err
    }

    client, err := b.GetClientForLanguage(string(*language))
    if err != nil {
        return nil, err
    }

    return client.Completion(uri, line, character)
}
```

**Step 3: Create MCP tool** in `mcpserver/tools/completion.go`:

```go
// mcpserver/tools/completion.go
func RegisterCompletionTool(server ToolServer, bridge interfaces.BridgeInterface) {
    tool := mcp.NewTool("completion",
        mcp.WithDescription("Get code completion suggestions"),
        mcp.WithString("uri", mcp.Required()),
        mcp.WithNumber("line", mcp.Required()),
        mcp.WithNumber("character", mcp.Required()),
    )

    handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        // ... implementation
    }

    server.AddTool(tool, handler)
}
```

**Step 4: Register the capability** in `lsp/capabilities/features.go`:

```go
// lsp/capabilities/features.go - Add to init()
func init() {
    // ... existing registrations ...

    // textDocument/completion - Code completion
    // Implemented in: lsp.Completion(), bridge.GetCompletion()
    // Exposed via: completion MCP tool
    Registry.RegisterCompletion(&protocol.CompletionClientCapabilities{
        DynamicRegistration: false,
        CompletionItem: &protocol.CompletionClientCapabilitiesCompletionItem{
            SnippetSupport: true,              // Support snippet completions
            CommitCharactersSupport: true,      // Support commit characters
            DocumentationFormat: []protocol.MarkupKind{
                protocol.MarkupKindMarkdown,
                protocol.MarkupKindPlainText,
            },
            DeprecatedSupport: true,
            PreselectSupport: true,
        },
    })
}
```

**Step 5: Add registry method** (if not already present) in `lsp/capabilities/registry.go`:

```go
// registry.go
type CapabilityRegistry struct {
    // ... existing fields ...
    completion *protocol.CompletionClientCapabilities
}

func (r *CapabilityRegistry) RegisterCompletion(cap *protocol.CompletionClientCapabilities) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.completion = cap
}

func (r *CapabilityRegistry) Build() protocol.ClientCapabilities {
    r.mu.RLock()
    defer r.mu.RUnlock()

    // ... existing code ...

    caps.TextDocument = &protocol.TextDocumentClientCapabilities{
        // ... existing capabilities ...
        Completion: r.completion,  // Add this line
    }

    return caps
}
```

**Done!** The capability is now automatically included when the bridge initializes.

## Benefits

✅ **Can't forget to register** - Feature won't work if capability not registered
✅ **Self-documenting** - All capabilities in one place with comments
✅ **Type-safe** - Uses actual protocol types, not maps
✅ **Testable** - Can verify implementations match registrations
✅ **Maintainable** - Adding features is straightforward

## Currently Registered Features

See `features.go` for the complete list. As of now, we register:

**Workspace:**
- `workspace/configuration` - Dynamic configuration
- `workspace/workspaceFolders` - Multi-root workspaces
- `workspace/symbol` - Project-wide symbol search
- `workspace/diagnostic` - Workspace diagnostics

**TextDocument:**
- `textDocument/publishDiagnostics` - Push diagnostics ← **Fixes TypeScript!**
- `textDocument/diagnostic` - Pull diagnostics
- `textDocument/hover` - Hover information
- `textDocument/signatureHelp` - Function signatures
- `textDocument/definition` - Go to definition
- `textDocument/references` - Find references
- `textDocument/implementation` - Find implementations
- `textDocument/documentSymbol` - Document outline
- `textDocument/codeAction` - Quick fixes/refactorings
- `textDocument/formatting` - Document formatting
- `textDocument/rename` - Symbol renaming
- `textDocument/semanticTokens` - Semantic highlighting
- `callHierarchy/*` - Call hierarchy navigation

**Lifecycle:**
- `textDocument/did*` - didOpen, didChange, didSave, didClose

## Debugging

To see what capabilities are registered:

```go
import "rockerboo/mcp-lsp-bridge/lsp/capabilities"

// Get list of registered capability names
registered := capabilities.Registry.ListRegistered()
for _, cap := range registered {
    fmt.Println(cap)
}
```

## Future Enhancements

Possible improvements:

1. **Build-time validation** - Generate code that verifies each MCP tool has a registered capability
2. **Capability overrides** - Allow config to disable specific capabilities
3. **Auto-detection** - Use reflection to detect implemented methods
4. **Profiles** - Predefined capability sets (minimal, standard, full)
