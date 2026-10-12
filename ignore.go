package main

import (
	"bufio"
	"os"
	pathPkg "path"
	"path/filepath"
	"strings"
)

// IgnoreMatcher evaluates paths against built-in rules, .syncignore file, and custom config rules.
type IgnoreMatcher struct {
	localRoot string
	rules     []string
}

// NewIgnoreMatcher initializes the matcher with built-in system rules, .syncignore file rules, and custom rules.
func NewIgnoreMatcher(localRoot string, customRules []string) *IgnoreMatcher {
	m := &IgnoreMatcher{
		localRoot: localRoot,
	}

	// 1. Built-in system ignore rules (OS temporary, system, and metadata files)
	builtins := []string{
		".google_sync_anchor",
		"System Volume Information",
		"$RECYCLE.BIN",
		"desktop.ini",
		"thumbs.db",
		"Thumbs.db",
		"*.tmp",
		"~$*",
		".~*",
		"*.crdownload",
		"__pycache__",
	}
	m.rules = append(m.rules, builtins...)

	// 2. Load rules from .syncignore file in localRoot if it exists
	if localRoot != "" {
		syncIgnorePath := filepath.Join(localRoot, ".syncignore")
		if f, err := os.Open(syncIgnorePath); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				line = strings.TrimPrefix(line, "/")
				m.rules = append(m.rules, line)
			}
			_ = f.Close()
		}
	}

	// 3. Add custom rules from Settings config
	for _, r := range customRules {
		r = strings.TrimSpace(r)
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		r = strings.TrimPrefix(r, "/")
		m.rules = append(m.rules, r)
	}

	return m
}

// ShouldIgnore tests whether a relative path or filename should be excluded from sync.
func (m *IgnoreMatcher) ShouldIgnore(relPath string, isDir bool) bool {
	if relPath == "" || relPath == "." {
		return false
	}

	clean := filepath.ToSlash(relPath)
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.TrimSuffix(clean, "/")
	parts := strings.Split(clean, "/")

	for _, rule := range m.rules {
		r := filepath.ToSlash(rule)
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}

		isDirOnly := strings.HasSuffix(r, "/")
		ruleClean := strings.TrimSuffix(r, "/")

		if isDirOnly {
			if !isDir {
				// Check if any parent folder of this file matches the dir-only rule
				for i := 0; i < len(parts)-1; i++ {
					if matchSegment(parts[i], ruleClean) {
						return true
					}
				}
				continue
			}
		}

		// Check full path match
		if matchSegment(clean, ruleClean) {
			return true
		}

		// Check each individual segment (e.g. node_modules, .git anywhere in path)
		for _, part := range parts {
			if matchSegment(part, ruleClean) {
				return true
			}
		}
	}

	return false
}

func matchSegment(target, pattern string) bool {
	targetLower := strings.ToLower(target)
	patternLower := strings.ToLower(pattern)

	if targetLower == patternLower {
		return true
	}

	// Glob pattern matching
	if strings.ContainsAny(patternLower, "*?[") {
		matched, err := pathPkg.Match(patternLower, targetLower)
		if err == nil && matched {
			return true
		}
	}

	return false
}
