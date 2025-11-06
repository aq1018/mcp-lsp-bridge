package resources

import (
	"rockerboo/mcp-lsp-bridge/interfaces"
)

// ResourceDefinition defines a resource that can be registered with the MCP server
// Resources auto-register via init() functions in their respective files
type ResourceDefinition struct {
	// Name is the human-readable name of the resource (e.g., "diagnostics", "lsp-status")
	Name string

	// UriTemplate is the URI pattern for this resource (e.g., "diagnostics://{uri}")
	// Variables in curly braces will be extracted during read requests
	UriTemplate string

	// Description explains what this resource provides
	Description string

	// MimeType is the content type returned by this resource (typically "application/json")
	MimeType string

	// ReadHandler is called when a client requests this resource via resources/read
	// The uri parameter is the full resource URI (e.g., "diagnostics://file:///path/to/file.ts")
	// Returns the resource content and any error
	ReadHandler func(uri string, bridge interfaces.BridgeInterface) (interface{}, error)

	// OnSetup is called once during initialization to wire up notifications
	// This is where you hook into events and call Registry.Notify() when changes occur
	// Optional: if nil, the resource is read-only with no push notifications
	OnSetup func(bridge interfaces.BridgeInterface) error
}

// RegisteredResource wraps a ResourceDefinition with runtime state
type RegisteredResource struct {
	Definition *ResourceDefinition
	// Future: could add metrics, last access time, etc.
}
