# MCP Resource Auto-Registration System

This directory contains the pluggable resource registry for mcp-lsp-bridge, inspired by the `lsp/capabilities/` registry pattern. Resources auto-register via `init()` functions, making the system modular and extensible.

## Architecture

### Core Components

- **registry.go** - Central resource registry, manages all registered resources
- **types.go** - Resource definition interfaces
- **subscriptions.go** - Tracks which clients are subscribed to which resources
- **diagnostics.go** - Example: LSP diagnostics resource (auto-registers)
- **lsp_status.go** - Example: LSP server status resource (auto-registers)

### How It Works

1. **Auto-Registration**: Each resource file has an `init()` function that calls `Registry.Register()`
2. **Build Phase**: During server setup, `Registry.Build()` registers all resources with the MCP server
3. **Push Notifications**: Resources can optionally wire up notifications via `OnSetup` hooks
4. **Subscriptions**: Clients can subscribe to resources to receive real-time updates

## Currently Registered Resources

### diagnostics://{uri}
**Purpose**: LSP diagnostics for a specific file
**Example**: `diagnostics://file:///path/to/file.ts`
**Notifications**: ✅ Sends updates when diagnostics change
**Implementation**: `diagnostics.go`

### lsp-server://{server_name}/status
**Purpose**: Connection status and metrics for a language server
**Example**: `lsp-server://gopls/status`
**Notifications**: ⚠️  Planned (needs lifecycle hooks)
**Implementation**: `lsp_status.go`

## Adding a New Resource

### Step 1: Create Resource File

Create a new file in `mcpserver/resources/`, e.g., `my_resource.go`:

```go
package resources

import (
    "encoding/json"
    "fmt"

    "rockerboo/mcp-lsp-bridge/interfaces"
    "rockerboo/mcp-lsp-bridge/logger"
)

// init auto-registers the resource when package is imported
func init() {
    Registry.Register(&ResourceDefinition{
        Name:        "my-resource",
        UriTemplate: "my-resource://{id}",
        Description: "Description of what this resource provides",
        MimeType:    "application/json",
        ReadHandler: handleMyResourceRead,
        OnSetup:     setupMyResourceNotifications, // Optional
    })
}

// handleMyResourceRead implements the read operation
func handleMyResourceRead(uri string, bridge interfaces.BridgeInterface) (interface{}, error) {
    logger.Debug(fmt.Sprintf("My resource read for URI: %s", uri))

    // 1. Parse the URI to extract parameters
    // 2. Fetch data from the bridge or other source
    // 3. Return the data (will be JSON-serialized)

    return map[string]interface{}{
        "status": "success",
        "data": "...",
    }, nil
}

// setupMyResourceNotifications wires up push notifications (optional)
func setupMyResourceNotifications(bridge interfaces.BridgeInterface) error {
    logger.Debug("Setting up my resource push notifications")

    // Hook into events that should trigger notifications
    // When your resource changes, call:
    // Registry.Notify(resourceURI, optionalPayload)

    return nil
}
```

### Step 2: That's It!

The resource is now automatically available:
- ✅ Auto-registered when package is imported
- ✅ Available via MCP resources/read requests
- ✅ Appears in resources/templates/list
- ✅ Push notifications work if OnSetup is implemented

## Resource URI Patterns

Resources use URI templates with variables in curly braces:

| Pattern | Example | Variables |
|---------|---------|-----------|
| `resource://{id}` | `resource://123` | `id` |
| `resource://{category}/{id}` | `resource://user/123` | `category`, `id` |
| `resource://{uri}` | `resource://file:///path` | `uri` |

**Important**: Your `ReadHandler` receives the full URI and must parse out the variables.

## Push Notifications

### How Subscriptions Work

1. **Client subscribes**: `resources/subscribe` with resource URI
2. **Resource changes**: Your code detects the change
3. **Notify subscribers**: Call `Registry.Notify(uri, payload)`
4. **Clients notified**: Via `notifications/resources/updated`

### Example: Notification Setup

```go
func setupNotifications(bridge interfaces.BridgeInterface) error {
    // Hook into an event
    bridge.OnSomeEvent(func(identifier string) {
        resourceURI := fmt.Sprintf("my-resource://%s", identifier)

        // Notify all subscribers
        Registry.Notify(resourceURI, map[string]interface{}{
            "timestamp": time.Now(),
            "status": "updated",
        })
    })

    return nil
}
```

### Subscription Tracking

The registry automatically tracks subscriptions:
- `Subscribe(clientID, uri)` - Add a subscription
- `Unsubscribe(clientID, uri)` - Remove a subscription
- `GetSubscribers(uri)` - Get list of subscribed clients
- `UnsubscribeAll(clientID)` - Cleanup when client disconnects

## Best Practices

### 1. URI Design
- Use clear, hierarchical patterns
- Keep URIs human-readable
- Include enough context for filtering
- Example: `lsp-server://gopls/diagnostics` not `lsp://1/diag`

### 2. Error Handling
```go
func handleRead(uri string, bridge interfaces.BridgeInterface) (interface{}, error) {
    // Always validate URI format
    if !strings.HasPrefix(uri, "expected://") {
        return nil, fmt.Errorf("invalid URI: %s", uri)
    }

    // Return descriptive errors
    data, err := fetchData()
    if err != nil {
        return nil, fmt.Errorf("failed to fetch data: %w", err)
    }

    return data, nil
}
```

### 3. Notification Debouncing
If your resource changes frequently, debounce notifications:

```go
var (
    notifyMu sync.Mutex
    notifyTimer *time.Timer
)

func debounceNotify(uri string) {
    notifyMu.Lock()
    defer notifyMu.Unlock()

    if notifyTimer != nil {
        notifyTimer.Stop()
    }

    notifyTimer = time.AfterFunc(500*time.Millisecond, func() {
        Registry.Notify(uri, nil)
    })
}
```

### 4. Logging
- Log at DEBUG level for normal operations
- Log at INFO level for setup/registration
- Log at ERROR level for failures
- Include the resource URI in all logs

## Testing Your Resource

### 1. Verify Registration
Check that your resource appears in the registry:

```go
// In tests or debug code
registered := resources.Registry.ListRegistered()
for _, res := range registered {
    fmt.Println(res)
}
// Should see: "my-resource: my-resource://{id} - Description..."
```

### 2. Test Read Handler
Create a test that calls your read handler directly:

```go
func TestMyResourceRead(t *testing.T) {
    bridge := &MockBridge{} // Your mock implementation

    result, err := handleMyResourceRead("my-resource://test123", bridge)
    if err != nil {
        t.Fatalf("Read failed: %v", err)
    }

    // Assert on result
}
```

### 3. Test Notifications
Subscribe to the resource and verify notifications are sent:

```go
func TestMyResourceNotifications(t *testing.T) {
    // Subscribe to resource
    resources.Registry.subscriptions.Subscribe("test-client", "my-resource://123")

    // Trigger notification
    resources.Registry.Notify("my-resource://123", nil)

    // Verify subscribers received it
}
```

## Debugging

### View Subscription Stats
```go
total, clients, resources := Registry.GetSubscriptionStats()
fmt.Printf("Total: %d, Clients: %d, Resources: %d\n", total, clients, resources)
```

### List All Registered Resources
```go
for _, name := range Registry.ListRegistered() {
    fmt.Println(name)
}
```

### Check Client Subscriptions
```go
subs := Registry.subscriptions.GetClientSubscriptions("client-id")
fmt.Printf("Client subscribed to: %v\n", subs)
```

## Future Enhancements

Possible improvements to the resource system:

1. **Resource Metadata** - Add tags, categories, versioning
2. **Access Control** - Per-resource permissions
3. **Rate Limiting** - Prevent notification spam
4. **Caching** - Cache read results with TTL
5. **Batch Operations** - Read multiple resources at once
6. **Resource Discovery** - Better introspection of available resources

## Examples of Potential Resources

Here are ideas for additional resources you could implement:

- `workspace-symbols://{pattern}` - Project-wide symbol search results
- `semantic-tokens://{uri}` - Semantic highlighting tokens
- `lsp-changes://{uri}` - Proposed file modifications from language server
- `project-health://` - Overall project diagnostics summary
- `lsp-logs://{server_name}` - Language server log output
- `file-outline://{uri}` - Document symbol tree
- `type-hierarchy://{uri}/{position}` - Type hierarchy at a position
- `call-graph://{uri}/{position}` - Call graph for a function

Each of these follows the same pattern: create a file, implement the handlers, auto-register with `init()`.
