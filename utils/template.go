package utils

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// TemplateContext holds the variables available for template substitution
type TemplateContext struct {
	WorkspaceRoot string // Absolute path to workspace root
	WorkspaceName string // Basename of workspace root
	WorkspacePath string // Same as WorkspaceRoot (for compatibility)
}

// NewTemplateContext creates a template context from a workspace root path
func NewTemplateContext(workspaceRoot string) *TemplateContext {
	absPath, err := filepath.Abs(workspaceRoot)
	if err != nil {
		absPath = workspaceRoot
	}

	return &TemplateContext{
		WorkspaceRoot: "file://" + absPath,
		WorkspaceName: filepath.Base(absPath),
		WorkspacePath: absPath,
	}
}

// SubstituteVariables replaces template variables in a string
// Supports: {{workspace_root}}, {{workspace_name}}, {{workspace_path}}
func (ctx *TemplateContext) SubstituteVariables(input string) string {
	result := input
	result = strings.ReplaceAll(result, "{{workspace_root}}", ctx.WorkspaceRoot)
	result = strings.ReplaceAll(result, "{{workspace_name}}", ctx.WorkspaceName)
	result = strings.ReplaceAll(result, "{{workspace_path}}", ctx.WorkspacePath)
	return result
}

// ValidateAndSubstitute validates that all template variables are known and substitutes them
// Returns an error if any unresolved template variables remain after substitution
func (ctx *TemplateContext) ValidateAndSubstitute(input string) (string, error) {
	// First, substitute all known variables
	result := ctx.SubstituteVariables(input)

	// Check if any template markers remain
	if strings.Contains(result, "{{") && strings.Contains(result, "}}") {
		// Find the first unresolved template variable
		start := strings.Index(result, "{{")
		end := strings.Index(result[start:], "}}") + start + 2
		if end > start {
			unresolvedVar := result[start:end]
			return "", fmt.Errorf("unsupported template variable '%s' found. Supported variables: {{workspace_root}}, {{workspace_name}}, {{workspace_path}}", unresolvedVar)
		}
		return "", fmt.Errorf("malformed template variable found")
	}

	return result, nil
}

// SubstituteInMap recursively substitutes template variables in a map
// Handles nested maps and string values
func (ctx *TemplateContext) SubstituteInMap(input map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range input {
		result[key] = ctx.substituteValue(value)
	}

	return result
}

// substituteValue recursively substitutes variables in any JSON-compatible value
func (ctx *TemplateContext) substituteValue(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return ctx.SubstituteVariables(v)
	case map[string]interface{}:
		return ctx.SubstituteInMap(v)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = ctx.substituteValue(item)
		}
		return result
	default:
		// Numbers, booleans, null - return as-is
		return v
	}
}

// SubstituteInJSON substitutes template variables in arbitrary JSON data
// Accepts any type, returns the same type with variables substituted
func (ctx *TemplateContext) SubstituteInJSON(data interface{}) interface{} {
	// Convert to JSON and back to get a clean interface{} representation
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return data
	}

	var result interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return data
	}

	return ctx.substituteValue(result)
}
