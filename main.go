// Copyright 2025 Dave Lage (rockerBOO)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"rockerboo/mcp-lsp-bridge/bridge"
	"rockerboo/mcp-lsp-bridge/directories"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/lsp"
	"rockerboo/mcp-lsp-bridge/mcpserver"
	"rockerboo/mcp-lsp-bridge/security"

	"github.com/mark3labs/mcp-go/server"
)

// configAttempt tracks an attempt to load a config file
type configAttempt struct {
	path   string
	exists bool
	err    error
}

// tryLoadConfig attempts to load configuration from multiple locations with security validation and template substitution
func tryLoadConfig(primaryPath, configDir, workspaceRoot string, allowedDirectories ...[]string) (*lsp.LSPServerConfig, []configAttempt, error) {
	var configAllowedDirectories []string
	var attempts []configAttempt

	// If allowed directories are not provided, use default
	if len(allowedDirectories) > 0 {
		configAllowedDirectories = allowedDirectories[0]
	} else {
		// Get current working directory for validation
		cwd, err := os.Getwd()
		if err != nil {
			return nil, attempts, fmt.Errorf("failed to get current working directory: %w", err)
		}

		// Get allowed directories for config files
		configAllowedDirectories = security.GetConfigAllowedDirectories(configDir, cwd)
	}

	// Build list of all paths to try
	pathsToTry := []string{primaryPath}

	// Add fallback paths (excluding example config)
	fallbackPaths := []string{
		"lsp_config.json",                       // Current directory
		filepath.Join(configDir, "config.json"), // Alternative name in config dir
	}

	for _, fallbackPath := range fallbackPaths {
		if fallbackPath != primaryPath {
			pathsToTry = append(pathsToTry, fallbackPath)
		}
	}

	// Try each path and track attempts
	for i, configPath := range pathsToTry {
		attempt := configAttempt{path: configPath}

		// Check if file exists
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			attempt.exists = false
			attempt.err = fmt.Errorf("file does not exist")
			attempts = append(attempts, attempt)
			continue
		}

		attempt.exists = true

		// Try to load the config
		config, err := lsp.LoadLSPConfig(configPath, configAllowedDirectories, workspaceRoot)
		if err == nil {
			// Success!
			if i > 0 {
				// Loaded from fallback, notify user
				logger.Warn(fmt.Sprintf("INFO: Loaded configuration from fallback location: %s\n", configPath))
				fmt.Fprintf(os.Stderr, "INFO: Loaded configuration from fallback location: %s\n", configPath)
			}
			return config, attempts, nil
		}

		attempt.err = err
		attempts = append(attempts, attempt)
	}

	return nil, attempts, errors.New("no valid configuration found")
}

// validateCommandLineArgs validates command line arguments for security
func validateCommandLineArgs(confPath, logPath, configDir, logDir string) error {
	// Get current working directory for validation
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

	// Validate config path if provided
	if confPath != "" {
		configAllowedDirs := security.GetConfigAllowedDirectories(configDir, cwd)
		if _, err := security.ValidateConfigPath(confPath, configAllowedDirs); err != nil {
			return fmt.Errorf("invalid config path: %w", err)
		}
	}

	// Validate log path if provided
	if logPath != "" {
		logAllowedDirs := []string{logDir, cwd, "."}
		if _, err := security.ValidateConfigPath(logPath, logAllowedDirs); err != nil {
			return fmt.Errorf("invalid log path: %w", err)
		}
	}

	return nil
}

func main() {
	// Initialize directory resolver
	dirResolver := directories.NewDirectoryResolver("mcp-lsp-bridge", directories.DefaultUserProvider{}, directories.DefaultEnvProvider{}, true)

	// Get default directories
	configDir, err := dirResolver.GetConfigDirectory()
	if err != nil {
		log.Fatalf("Failed to get config directory: %v", err)
	}

	logDir, err := dirResolver.GetLogDirectory()
	if err != nil {
		log.Fatalf("Failed to get log directory: %v", err)
	}

	// Set up default paths
	defaultConfigPath := filepath.Join(configDir, "lsp_config.json")
	defaultLogPath := filepath.Join(logDir, "mcp-lsp-bridge.log")

	// Parse command line flags
	var confPath string

	var logPath string

	var logLevel string

	flag.StringVar(&confPath, "config", defaultConfigPath, "Path to LSP configuration file")
	flag.StringVar(&confPath, "c", defaultConfigPath, "Path to LSP configuration file (short)")
	flag.StringVar(&logPath, "log-path", "", "Path to log file (overrides config and default)")
	flag.StringVar(&logPath, "l", "", "Path to log file (short)")
	flag.StringVar(&logLevel, "log-level", "", "Log level: debug, info, warn, error (overrides config)")
	flag.Parse()

	// Validate command line arguments for security
	if err := validateCommandLineArgs(confPath, logPath, configDir, logDir); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Invalid command line arguments: %v\n", err)
		os.Exit(1)
	}

	// Get current working directory for template substitution
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current working directory: %v", err)
	}

	// Load LSP configuration - HARD FAIL if config is missing or invalid
	config, attempts, err := tryLoadConfig(confPath, configDir, cwd)
	if err != nil {
		// Provide detailed error message showing all attempted paths
		fmt.Fprintf(os.Stderr, "ERROR: Failed to load LSP configuration\n\n")
		fmt.Fprintf(os.Stderr, "Attempted paths:\n")

		for _, attempt := range attempts {
			if attempt.exists {
				fmt.Fprintf(os.Stderr, "  ✗ %s\n", attempt.path)
				fmt.Fprintf(os.Stderr, "    → File exists but failed to load: %v\n\n", attempt.err)
			} else {
				fmt.Fprintf(os.Stderr, "  ✗ %s\n", attempt.path)
				fmt.Fprintf(os.Stderr, "    → %v\n\n", attempt.err)
			}
		}

		fmt.Fprintf(os.Stderr, "No valid configuration found.\n\n")
		fmt.Fprintf(os.Stderr, "For configuration instructions, see:\n")
		fmt.Fprintf(os.Stderr, "  https://github.com/rockerboo/mcp-lsp-bridge#configuration\n\n")
		fmt.Fprintf(os.Stderr, "The application requires a valid configuration with at least one language server.\n")
		os.Exit(1)
	}

	// Set up logging configuration from loaded config
	logConfig := logger.LoggerConfig{
		LogPath:     config.Global.LogPath,
		LogLevel:    config.Global.LogLevel,
		LogOutput:   config.Global.LogOutput,
		MaxLogFiles: config.Global.MaxLogFiles,
	}

	// Override with command-line flags if provided
	if logPath != "" {
		logConfig.LogPath = logPath
	}

	if logLevel != "" {
		logConfig.LogLevel = logLevel
	}

	// Ensure we have a log path (use default if not specified)
	if logConfig.LogPath == "" {
		logConfig.LogPath = defaultLogPath
	}

	if err := logger.InitLogger(logConfig); err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer logger.Close()

	logger.Info("Starting MCP-LSP Bridge...")

	// Log the loaded configuration summary
	logger.Info(fmt.Sprintf("Configuration loaded successfully: %d language servers, %d language mappings, %d extension mappings",
		len(config.LanguageServers), len(config.LanguageServerMap), len(config.ExtensionLanguageMap)))

	// Log each configured language server with its supported languages
	for serverName, serverConfig := range config.LanguageServers {
		if languages, ok := config.LanguageServerMap[serverName]; ok {
			logger.Info(fmt.Sprintf("  ✓ Server '%s' -> languages: %v, command: %s %v",
				serverName, languages, serverConfig.Command, serverConfig.Args))
		} else {
			logger.Warn(fmt.Sprintf("  ⚠ Server '%s' has configuration but no language mapping!", serverName))
		}
	}

	// Create and initialize the bridge (cwd already obtained earlier)
	bridgeInstance := bridge.NewMCPLSPBridge(config, []string{cwd})

	// Verify config is still valid after bridge creation
	logger.Info(fmt.Sprintf("Bridge created. Verifying config: %d language servers in config", len(config.LanguageServers)))
	bridgeConfig := bridgeInstance.GetConfig()
	if bridgeConfig != nil {
		servers := bridgeConfig.GetLanguageServers()
		logger.Info(fmt.Sprintf("Bridge GetConfig().GetLanguageServers() returns: %d servers", len(servers)))
	} else {
		logger.Error("Bridge GetConfig() returned nil!")
	}

	// Setup MCP server with bridge
	mcpServer := mcpserver.SetupMCPServer(bridgeInstance)

	// Store the server reference in the bridge
	bridgeInstance.SetServer(mcpServer)

	// Start MCP server
	logger.Info("Starting MCP server...")

	if err := server.ServeStdio(mcpServer); err != nil {
		logger.Error("MCP server error: " + err.Error())
	}
}
