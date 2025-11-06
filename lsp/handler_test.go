package lsp

import (
	"testing"
)

// MockClientContext implements ClientContextProvider for testing
type MockClientContext struct {
	projectRoots           []string
	settings               map[string]interface{}
	workspaceConfiguration map[string]interface{}
}

func (m *MockClientContext) ProjectRoots() []string {
	return m.projectRoots
}

func (m *MockClientContext) InitializationSettings() map[string]interface{} {
	return m.settings
}

func (m *MockClientContext) GetWorkspaceConfiguration() map[string]interface{} {
	return m.workspaceConfiguration
}

func TestBuildConfigurationResponse_ESLint(t *testing.T) {
	// Setup
	mockClient := &MockClientContext{
		projectRoots: []string{"/test/workspace"},
		settings:     make(map[string]interface{}),
	}

	handler := &ClientHandler{
		client: mockClient,
	}

	// Create a configuration item for ESLint
	item := ConfigurationItem{
		Section: "eslint",
	}

	// Execute
	result := handler.buildConfigurationResponse(item)

	// Verify
	eslintConfig, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	// Verify ESLint config has workingDirectory
	if _, exists := eslintConfig["workingDirectory"]; !exists {
		t.Error("Expected ESLint config to have 'workingDirectory'")
	}

	// Verify workingDirectory has mode
	workingDir, ok := eslintConfig["workingDirectory"].(map[string]any)
	if !ok {
		t.Fatalf("Expected workingDirectory to be map[string]any, got %T", eslintConfig["workingDirectory"])
	}

	if mode, exists := workingDir["mode"]; !exists || mode != "location" {
		t.Errorf("Expected workingDirectory.mode to be 'location', got %v", mode)
	}

	// Verify enable is true
	if enable, exists := eslintConfig["enable"]; !exists || enable != true {
		t.Errorf("Expected eslint.enable to be true, got %v", enable)
	}
}

func TestBuildConfigurationResponse_CustomSettings(t *testing.T) {
	// Setup with custom settings
	customSettings := map[string]interface{}{
		"eslint": map[string]interface{}{
			"enable": true,
			"workingDirectory": map[string]interface{}{
				"mode": "auto",
			},
			"experimental": map[string]interface{}{
				"useFlatConfig": true,
			},
		},
	}

	mockClient := &MockClientContext{
		projectRoots: []string{"/test/workspace"},
		settings:     customSettings,
	}

	handler := &ClientHandler{
		client: mockClient,
	}

	// Create configuration item
	item := ConfigurationItem{
		Section: "eslint",
	}

	// Execute
	result := handler.buildConfigurationResponse(item)

	// Verify
	eslintConfig, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected response to be map[string]interface{}, got %T", result)
	}

	// Verify custom settings are returned
	workingDir, ok := eslintConfig["workingDirectory"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected workingDirectory to exist")
	}

	if mode := workingDir["mode"]; mode != "auto" {
		t.Errorf("Expected custom mode 'auto', got %v", mode)
	}

	experimental, ok := eslintConfig["experimental"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected experimental to exist")
	}

	if useFlatConfig := experimental["useFlatConfig"]; useFlatConfig != true {
		t.Errorf("Expected useFlatConfig to be true, got %v", useFlatConfig)
	}
}

func TestBuildConfigurationResponse_MultipleLanguages(t *testing.T) {
	// Setup
	mockClient := &MockClientContext{
		projectRoots: []string{"/test/workspace"},
		settings:     make(map[string]interface{}),
	}

	handler := &ClientHandler{
		client: mockClient,
	}

	// Test different language configurations
	testCases := []struct {
		section  string
		hasValue bool
	}{
		{"eslint", true},
		{"typescript", true},
		{"javascript", true},
		{"python", true},
		{"unknown", true}, // Should return empty map
	}

	for _, tc := range testCases {
		t.Run(tc.section, func(t *testing.T) {
			item := ConfigurationItem{Section: tc.section}
			result := handler.buildConfigurationResponse(item)

			if !tc.hasValue {
				if result != nil {
					t.Errorf("Expected nil result for %s, got %v", tc.section, result)
				}
			} else {
				config, ok := result.(map[string]any)
				if !ok {
					t.Errorf("Expected map[string]any for %s, got %T", tc.section, result)
				}
				// Empty map is acceptable for unknown sections
				if tc.section != "unknown" && len(config) == 0 {
					t.Errorf("Expected non-empty config for %s", tc.section)
				}
			}
		})
	}
}

func TestBuildDefaultConfiguration_UnknownSection(t *testing.T) {
	// Setup
	mockClient := &MockClientContext{
		projectRoots: []string{"/test/workspace"},
		settings:     make(map[string]interface{}),
	}

	handler := &ClientHandler{
		client: mockClient,
	}

	// Test with unknown section
	item := ConfigurationItem{
		Section: "unknown-language-server",
	}

	config := handler.buildDefaultConfiguration(item)

	// Verify it returns an empty map
	if config == nil {
		t.Error("Expected non-nil config for unknown section")
	}

	if len(config) != 0 {
		t.Errorf("Expected empty config for unknown section, got %d items", len(config))
	}
}
