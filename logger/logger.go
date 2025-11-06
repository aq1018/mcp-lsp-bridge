package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type LoggerConfig struct {
	LogPath     string
	LogLevel    string // "info", "debug", "error"
	LogOutput   string // "file", "stderr", "both"
	MaxLogFiles int    // Maximum number of log files to keep
}

var (
	config      LoggerConfig
	infoLogger  *log.Logger
	errorLogger *log.Logger
	debugLogger *log.Logger
	logFile     *os.File
	logMutex    sync.Mutex
)

// DefaultConfig provides a default logging configuration
func DefaultConfig() LoggerConfig {
	return LoggerConfig{
		LogPath:     filepath.Join(os.TempDir(), "mcp-lsp-bridge.log"),
		LogLevel:    "info",
		MaxLogFiles: 5,
	}
}

// InitLogger sets up logging with configuration (file, stderr, or both)
func InitLogger(cfg LoggerConfig) error {
	logMutex.Lock()
	defer logMutex.Unlock()

	// Use default config if not provided
	if cfg.LogPath == "" {
		cfg = DefaultConfig()
	}

	// Default to file output if not specified
	if cfg.LogOutput == "" {
		cfg.LogOutput = "file"
	}

	// Store configuration
	config = cfg

	// Determine output destination(s)
	var writers []io.Writer

	// Add stderr if configured
	if cfg.LogOutput == "stderr" || cfg.LogOutput == "both" {
		writers = append(writers, os.Stderr)
	}

	// Add file if configured
	if cfg.LogOutput == "file" || cfg.LogOutput == "both" {
		// Ensure log directory exists
		if err := os.MkdirAll(filepath.Dir(cfg.LogPath), 0700); err != nil {
			return fmt.Errorf("failed to create log directory: %v", err)
		}

		// Rotate logs if max log files exceeded
		rotateLogFiles(cfg)

		// Open log file with append mode and create if not exists
		file, err := os.OpenFile(cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("failed to open log file: %v", err)
		}

		logFile = file
		writers = append(writers, file)
	}

	// Validate we have at least one output
	if len(writers) == 0 {
		return fmt.Errorf("no output destination configured")
	}

	// Create multi-writer
	var finalWriter io.Writer
	if len(writers) == 1 {
		finalWriter = writers[0]
	} else {
		finalWriter = io.MultiWriter(writers...)
	}

	// Create loggers with timestamps
	infoLogger = log.New(finalWriter, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLogger = log.New(finalWriter, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	debugLogger = log.New(finalWriter, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)

	return nil
}

// rotateLogFiles manages log file rotation
func rotateLogFiles(cfg LoggerConfig) {
	if cfg.MaxLogFiles <= 0 {
		return
	}

	// Find existing log files
	baseDir := filepath.Dir(cfg.LogPath)
	baseFileName := filepath.Base(cfg.LogPath)
	files, _ := filepath.Glob(filepath.Join(baseDir, baseFileName+".*"))

	// If max log files exceeded, remove oldest logs
	if len(files) >= cfg.MaxLogFiles {
		sort.Slice(files, func(i, j int) bool {
			fiA, _ := os.Stat(files[i])
			fiB, _ := os.Stat(files[j])

			return fiA.ModTime().Before(fiB.ModTime())
		})

		// Remove oldest log files
		for _, oldFile := range files[:len(files)-cfg.MaxLogFiles+1] {
			err := os.Remove(oldFile)
			if err != nil {
				Error(fmt.Errorf("failed to remove old log file: %v", err))
			}
		}
	}
}

// Info logs an informational message with caller context
func Info(v ...any) {
	if config.LogLevel == "info" || config.LogLevel == "debug" {
		if infoLogger != nil {
			_ = infoLogger.Output(2, fmt.Sprintln(v...))
		}
	}
}

// Warn logs a warning message with caller context
func Warn(v ...any) {
	if config.LogLevel == "info" || config.LogLevel == "warn" {
		if infoLogger != nil {
			_ = infoLogger.Output(2, fmt.Sprintln(v...))
		}
	}
}

// Error logs an error message with caller context
func Error(v ...any) {
	if errorLogger != nil {
		_ = errorLogger.Output(2, fmt.Sprintln(v...))
	}
}

// Debug logs a debug message with caller context
func Debug(v ...any) {
	if config.LogLevel == "debug" {
		if debugLogger != nil {
			_ = debugLogger.Output(2, fmt.Sprintln(v...))
		}
	}
}

// Close closes the log file
func Close() {
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile != nil {
		err := logFile.Close()
		if err != nil {
			log.Printf("failed to close log file: %v", err)
		}
	}
}
