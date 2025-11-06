package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/security"
	"rockerboo/mcp-lsp-bridge/types"
	"rockerboo/mcp-lsp-bridge/utils"
)

// LoadLSPConfig loads the LSP configuration from a JSON file with security validation and template substitution
func LoadLSPConfig(path string, allowedDirectories []string, workspaceRoot string) (config *LSPServerConfig, err error) {
	// Create template context from workspace root
	templateCtx := utils.NewTemplateContext(workspaceRoot)

	// Track visited files to prevent circular imports
	visited := make(map[string]bool)
	config, err = loadLSPConfigRecursive(path, allowedDirectories, visited, templateCtx, true)
	if err != nil {
		return nil, err
	}

	// Log final configuration after all imports are processed
	fmt.Fprintf(os.Stderr, "[CONFIG] Validating root config. Language servers: %d, Language server mappings: %d, Extension mappings: %d\n",
		len(config.LanguageServers), len(config.LanguageServerMap), len(config.ExtensionLanguageMap))

	// Log language server details
	for serverName := range config.LanguageServers {
		if languages, ok := config.LanguageServerMap[serverName]; ok {
			fmt.Fprintf(os.Stderr, "[CONFIG]   Server '%s' configured for languages: %v\n", serverName, languages)
		} else {
			fmt.Fprintf(os.Stderr, "[CONFIG]   WARNING: Server '%s' has no language mapping!\n", serverName)
		}
	}

	fmt.Fprintf(os.Stderr, "[CONFIG] ✓ Successfully loaded config with %d language servers\n", len(config.LanguageServers))
	return config, nil
}

// loadLSPConfigRecursive handles recursive loading of config files with imports and template substitution
func loadLSPConfigRecursive(path string, allowedDirectories []string, visited map[string]bool, templateCtx *utils.TemplateContext, isRoot bool) (config *LSPServerConfig, err error) {
	// Use fmt.Fprintf since logger may not be initialized yet (config loads before logger init)
	fmt.Fprintf(os.Stderr, "[CONFIG] Loading config from '%s'\n", path)

	// Validate path using secure path validation
	cleanPath, err := security.ValidateConfigPath(path, allowedDirectories)
	if err != nil {
		return nil, fmt.Errorf("config path validation failed: %w", err)
	}

	// Get absolute path for circular import detection
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Check for circular imports
	if visited[absPath] {
		return nil, fmt.Errorf("circular import detected: %s", path)
	}
	visited[absPath] = true

	// Step 1: Read file as text
	fileBytes, err := os.ReadFile(cleanPath) // #nosec G304 - Path validated by security.ValidateConfigPath
	if err != nil {
		return nil, fmt.Errorf("failed to read config file '%s': %w", path, err)
	}

	// Step 2: Apply template substitution to raw JSON text
	fileContent := string(fileBytes)
	substitutedContent, err := templateCtx.ValidateAndSubstitute(fileContent)
	if err != nil {
		return nil, fmt.Errorf("template substitution failed in config file '%s': %w", path, err)
	}

	// Step 3: Parse substituted JSON
	if err := json.Unmarshal([]byte(substitutedContent), &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file '%s' after template substitution: %w", path, err)
	}

	// Initialize maps if they don't exist (for imported configs that are fragments)
	if config.LanguageServers == nil {
		config.LanguageServers = make(map[types.LanguageServer]LanguageServerConfig)
	}
	if config.LanguageServerMap == nil {
		config.LanguageServerMap = make(map[types.LanguageServer][]types.Language)
	}
	if config.ExtensionLanguageMap == nil {
		config.ExtensionLanguageMap = make(map[string]types.Language)
	}

	// Process imports if present (import paths are already substituted from Step 2)
	if len(config.Imports) > 0 {
		fmt.Fprintf(os.Stderr, "[CONFIG] Processing %d imports from config '%s'\n", len(config.Imports), path)
		baseDir := filepath.Dir(cleanPath)

		for i, importPath := range config.Imports {
			// Resolve import path relative to the importing config file
			var resolvedImportPath string
			if filepath.IsAbs(importPath) {
				resolvedImportPath = importPath
			} else {
				resolvedImportPath = filepath.Join(baseDir, importPath)
			}

			fmt.Fprintf(os.Stderr, "[CONFIG]   [%d/%d] Loading import: %s (resolved: %s)\n", i+1, len(config.Imports), importPath, resolvedImportPath)

			// Load imported config with same template context
			// Don't validate imported fragments - they may be incomplete
			importedConfig, err := loadLSPConfigRecursive(resolvedImportPath, allowedDirectories, visited, templateCtx, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[CONFIG] ERROR: Failed to load imported config '%s': %v\n", importPath, err)
				return nil, fmt.Errorf("failed to load imported config '%s': %w", importPath, err)
			}

			fmt.Fprintf(os.Stderr, "[CONFIG]   [%d/%d] Successfully loaded import with %d language servers\n", i+1, len(config.Imports), len(importedConfig.LanguageServers))

			// Merge imported config into main config
			mergeConfigs(config, importedConfig)
		}

		fmt.Fprintf(os.Stderr, "[CONFIG] Import processing complete. Total language servers after merge: %d\n", len(config.LanguageServers))
	} else {
		fmt.Fprintf(os.Stderr, "[CONFIG] No imports found in config '%s'\n", path)
	}

	// Only validate if this is the root config (not an import)
	if isRoot {
		// Validate final merged configuration
		if err := validateConfig(config); err != nil {
			return nil, err
		}
	}

	return config, nil
}

// validateConfig validates that the configuration meets minimum requirements
func validateConfig(config *LSPServerConfig) error {
	// Validate that we have the required mappings
	if config.ExtensionLanguageMap == nil {
		return errors.New("extension_language_map is required in configuration")
	}

	if config.LanguageServerMap == nil {
		return errors.New("language_server_map is required in configuration")
	}

	// Validate at least one language server is configured
	if len(config.LanguageServers) == 0 {
		return errors.New("no language servers configured - at least one language server is required")
	}

	// Validate at least one extension mapping exists
	if len(config.ExtensionLanguageMap) == 0 {
		return errors.New("no file extension mappings configured - at least one extension mapping is required")
	}

	// Validate at least one language server mapping exists
	if len(config.LanguageServerMap) == 0 {
		return errors.New("no language server mappings configured - at least one language server mapping is required")
	}

	// Validate log output value if specified
	if config.Global.LogOutput != "" {
		validOutputs := map[string]bool{"file": true, "stderr": true, "both": true}
		if !validOutputs[config.Global.LogOutput] {
			return fmt.Errorf("invalid log_output value '%s' - must be 'file', 'stderr', or 'both'", config.Global.LogOutput)
		}
	}

	return nil
}

// mergeConfigs merges importedConfig into baseConfig
func mergeConfigs(baseConfig, importedConfig *LSPServerConfig) {
	// Merge language servers
	if baseConfig.LanguageServers == nil {
		baseConfig.LanguageServers = make(map[types.LanguageServer]LanguageServerConfig)
	}
	for serverName, serverConfig := range importedConfig.LanguageServers {
		// Only add if not already defined (base config takes precedence)
		if _, exists := baseConfig.LanguageServers[serverName]; !exists {
			baseConfig.LanguageServers[serverName] = serverConfig
		}
	}

	// Merge language server map
	if baseConfig.LanguageServerMap == nil {
		baseConfig.LanguageServerMap = make(map[types.LanguageServer][]types.Language)
	}
	for serverName, languages := range importedConfig.LanguageServerMap {
		if existingLanguages, exists := baseConfig.LanguageServerMap[serverName]; exists {
			// Merge languages, avoiding duplicates
			languageSet := make(map[types.Language]bool)
			for _, lang := range existingLanguages {
				languageSet[lang] = true
			}
			for _, lang := range languages {
				if !languageSet[lang] {
					baseConfig.LanguageServerMap[serverName] = append(baseConfig.LanguageServerMap[serverName], lang)
				}
			}
		} else {
			baseConfig.LanguageServerMap[serverName] = languages
		}
	}

	// Merge extension language map
	if baseConfig.ExtensionLanguageMap == nil {
		baseConfig.ExtensionLanguageMap = make(map[string]types.Language)
	}
	for ext, language := range importedConfig.ExtensionLanguageMap {
		// Only add if not already defined (base config takes precedence)
		if _, exists := baseConfig.ExtensionLanguageMap[ext]; !exists {
			baseConfig.ExtensionLanguageMap[ext] = language
		}
	}

	// Merge workspace configuration
	if importedConfig.WorkspaceConfiguration != nil {
		if baseConfig.WorkspaceConfiguration == nil {
			baseConfig.WorkspaceConfiguration = make(map[string]interface{})
		}
		for key, value := range importedConfig.WorkspaceConfiguration {
			// Only add if not already defined (base config takes precedence)
			if _, exists := baseConfig.WorkspaceConfiguration[key]; !exists {
				baseConfig.WorkspaceConfiguration[key] = value
			}
		}
	}

	// Merge preferred formatters
	if importedConfig.PreferredFormatters != nil {
		if baseConfig.PreferredFormatters == nil {
			baseConfig.PreferredFormatters = make(map[string]string)
		}
		for language, formatter := range importedConfig.PreferredFormatters {
			// Only add if not already defined (base config takes precedence)
			if _, exists := baseConfig.PreferredFormatters[language]; !exists {
				baseConfig.PreferredFormatters[language] = formatter
			}
		}
	}

	// Global config from base takes precedence, don't merge
}

func (c *LSPServerConfig) GlobalConfig() GlobalConfig {
	return c.Global
}

func (c *LSPServerConfig) FindExtLanguage(ext string) (*types.Language, error) {
	language, exists := c.ExtensionLanguageMap[ext]

	if !exists {
		return nil, fmt.Errorf("no language found for %s", ext)
	}

	typesLanguage := types.Language(language)
	return &typesLanguage, nil
}

func (c *LSPServerConfig) FindServerConfig(language string) (types.LanguageServerConfigProvider, error) {
	// Find which server handles this language
	for serverName, languages := range c.LanguageServerMap {
		for _, lang := range languages {
			if string(lang) == language {
				// Found the server, now get its config
				if serverConfig, exists := c.LanguageServers[serverName]; exists {
					return &serverConfig, nil
				}
				return nil, fmt.Errorf("server config not found for server '%s'", string(serverName))
			}
		}
	}

	return nil, fmt.Errorf("no server found for language '%s'", language)
}

// FindAllServerConfigs returns all server configurations that support the given language
func (c *LSPServerConfig) FindAllServerConfigs(language string) ([]types.LanguageServerConfigProvider, []types.LanguageServer, error) {
	var configs []types.LanguageServerConfigProvider
	var servers []types.LanguageServer

	// Find all servers that handle this language
	for serverName, languages := range c.LanguageServerMap {
		for _, lang := range languages {
			if string(lang) == language {
				// Found a server, get its config
				if serverConfig, exists := c.LanguageServers[serverName]; exists {
					configs = append(configs, &serverConfig)
					servers = append(servers, serverName)
				}
			}
		}
	}

	if len(configs) == 0 {
		return nil, nil, fmt.Errorf("no servers found for language '%s'", language)
	}

	return configs, servers, nil
}

// ProjectRootMarker represents a project root identifier
type ProjectRootMarker struct {
	Filename string
	Language string
}

// GetProjectRootMarkers returns a list of common project root markers
func GetProjectRootMarkers() []ProjectRootMarker {
	return []ProjectRootMarker{
		{"go.mod", "go"},
		{"go.sum", "go"},
		{"package.json", "typescript"},
		{"yarn.lock", "typescript"},
		{"package-lock.json", "typescript"},
		{"tsconfig.json", "typescript"},
		{"Cargo.toml", "rust"},
		{"Cargo.lock", "rust"},
		{"pyproject.toml", "python"},
		{"setup.py", "python"},
		{"requirements.txt", "python"},
		{"Pipfile", "python"},
		{"poetry.lock", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
		{"composer.json", "php"},
		{"CMakeLists.txt", "cpp"},
		{"Makefile", "c"},
		{"Dockerfile", "dockerfile"},
		{".gitignore", ""},
		{"README.md", ""},
	}
}

// DetectProjectLanguages scans a directory for project root markers and file extensions
// to determine all languages used in the project, returning them in priority order
func (c *LSPServerConfig) DetectProjectLanguages(projectPath string) ([]types.Language, error) {
	if projectPath == "" {
		return nil, errors.New("project path cannot be empty")
	}

	logger.Info(fmt.Sprintf("Detecting project languages in '%s'", projectPath))

	// Check if directory exists
	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("project directory does not exist: %s", projectPath)
	}

	languageScores := make(map[string]int)
	rootMarkers := GetProjectRootMarkers()

	// Step 1: Check for project root markers (highest priority)
	for _, marker := range rootMarkers {
		markerPath := filepath.Join(projectPath, marker.Filename)
		if _, err := os.Stat(markerPath); err == nil && marker.Language != "" {
			languageScores[marker.Language] += 100 // High priority for root markers
		}
	}

	// Step 2: Scan files for language detection based on extensions
	err := filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and common ignore patterns
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}

			if name == "node_modules" || name == "target" || name == "build" || name == "dist" {
				return filepath.SkipDir
			}
		}

		if !info.IsDir() {
			ext := filepath.Ext(path)
			if language, found := c.ExtensionLanguageMap[ext]; found {
				languageScores[string(language)] += 1
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking project directory: %w", err)
	}

	// Step 3: Sort languages by score (descending)
	type languageScore struct {
		language string
		score    int
	}

	var sortedLanguages []languageScore
	for lang, score := range languageScores {
		sortedLanguages = append(sortedLanguages, languageScore{lang, score})
	}

	// Simple sorting by score (descending)
	for i := range sortedLanguages {
		for j := i + 1; j < len(sortedLanguages); j++ {
			if sortedLanguages[j].score > sortedLanguages[i].score {
				sortedLanguages[i], sortedLanguages[j] = sortedLanguages[j], sortedLanguages[i]
			}
		}
	}

	// Extract just the language names
	var result []types.Language
	for _, ls := range sortedLanguages {
		result = append(result, types.Language(ls.language))
	}

	if len(result) == 0 {
		return nil, errors.New("no recognizable project languages found")
	}

	return result, nil
}

func (c *LSPServerConfig) GetGlobalConfig() types.GlobalConfig {
	return types.GlobalConfig(c.Global)
}

func (c *LSPServerConfig) GetLanguageServers() map[types.LanguageServer]types.LanguageServerConfigProvider {
	result := make(map[types.LanguageServer]types.LanguageServerConfigProvider)
	// Build a server -> server config mapping
	for serverName, serverConfig := range c.LanguageServers {
		result[serverName] = &serverConfig
	}
	return result
}

// GetServerNameFromLanguage returns the server name for a given language
func (c *LSPServerConfig) GetServerNameFromLanguage(language types.Language) types.LanguageServer {
	for serverName, supportedLanguages := range c.LanguageServerMap {
		if slices.Contains(supportedLanguages, language) {
			return serverName
		}
	}
	return "" // Handle not found case
}

// DetectPrimaryProjectLanguage returns the most likely primary language for a project
func (c *LSPServerConfig) DetectPrimaryProjectLanguage(projectPath string) (*types.Language, error) {
	languages, err := c.DetectProjectLanguages(projectPath)
	if err != nil {
		return nil, err
	}

	if len(languages) == 0 {
		return nil, errors.New("no project language detected")
	}

	language := types.Language(languages[0])

	return &language, nil
}

// GetPreferredFormatter returns the preferred formatter server name for a language, if configured
func (c *LSPServerConfig) GetPreferredFormatter(language string) types.LanguageServer {
	if c.PreferredFormatters == nil {
		return ""
	}

	if serverName, ok := c.PreferredFormatters[language]; ok {
		return types.LanguageServer(serverName)
	}

	return ""
}
