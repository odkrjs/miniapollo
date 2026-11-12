package miniapollo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadDir publishes every recognized config file in dir as a namespace under
// the server's dimensions. The file's base name is the namespace name:
//
//	application.properties -> namespace "application"     (parsed: key=value lines)
//	application.yml        -> namespace "application.yml"  (raw file as content)
//	application.yaml       -> namespace "application.yaml"
//	application.json       -> namespace "application.json"
//	*.txt                  -> namespace "<name>.txt"
//
// Sub-directories, dot-files and files with other extensions are skipped.
// It returns the namespaces loaded, in sorted order. Each file is published
// immediately (a fresh release each), so clients polling right after LoadDir
// see them all — and long-pollers parked on those namespaces are woken.
//
//	m, _ := miniapollo.NewServer("shop", "dev")
//	ns, err := m.LoadDir("./configs")   // []{"application", "layout.yml", ...}
func (s *Server) LoadDir(dir string) ([]string, error) {
	return s.LoadDirWith(s.appID, s.cluster, dir)
}

// LoadDirWith is LoadDir for explicit (appId, cluster).
func (s *Server) LoadDirWith(appID, cluster, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("miniapollo: read dir %s: %w", dir, err)
	}
	var loaded []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // dot-files skipped (editor droppings, .gitignore, ...)
		}
		ns, ok := namespaceOf(name)
		if !ok {
			continue // unsupported extension skipped
		}
		if err := s.loadFileWith(appID, cluster, filepath.Join(dir, name), ns); err != nil {
			return loaded, err
		}
		loaded = append(loaded, ns)
	}
	sort.Strings(loaded)
	return loaded, nil
}

// LoadFile publishes a single config file under the server's dimensions,
// with the same namespace-naming rules as LoadDir.
// Returns the namespace the file was published as.
func (s *Server) LoadFile(file string) (string, error) {
	return s.LoadFileWith(s.appID, s.cluster, file)
}

// LoadFileWith is LoadFile for explicit (appId, cluster).
func (s *Server) LoadFileWith(appID, cluster, file string) (string, error) {
	ns, ok := namespaceOf(filepath.Base(file))
	if !ok {
		return "", fmt.Errorf("miniapollo: unsupported config file: %s", file)
	}
	if err := s.loadFileWith(appID, cluster, file, ns); err != nil {
		return "", err
	}
	return ns, nil
}

// namespaceOf maps a file name to its Apollo namespace name.
//
// properties files drop the extension — that is real Apollo's convention
// (the namespace "application" is delivered through application.properties).
// File-type namespaces (.yml/.yaml/.json/.txt) keep their full name,
// because the extension IS part of the namespace identity.
func namespaceOf(fileName string) (string, bool) {
	lower := strings.ToLower(fileName)
	switch {
	case strings.HasSuffix(lower, ".properties"):
		return strings.TrimSuffix(fileName, filepath.Ext(fileName)), true
	case strings.HasSuffix(lower, ".yml"),
		strings.HasSuffix(lower, ".yaml"),
		strings.HasSuffix(lower, ".json"),
		strings.HasSuffix(lower, ".txt"):
		return fileName, true
	}
	return "", false
}

// loadFileWith reads one file and publishes it under ns.
// properties -> parsed into a flat kv map; file-types -> raw content.
func (s *Server) loadFileWith(appID, cluster, file, ns string) error {
	body, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("miniapollo: read %s: %w", file, err)
	}
	if strings.EqualFold(filepath.Ext(file), ".properties") {
		s.SetWith(appID, cluster, ns, parseProperties(string(body)))
		return nil
	}
	s.SetContentWith(appID, cluster, ns, string(body))
	return nil
}

// parseProperties parses java-properties style "key=value" text:
// blank lines and #/! comments are skipped, whitespace around key/value is
// trimmed, and the first '=' or ':' separates key from value.
func parseProperties(src string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" ||
			strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		idx := strings.IndexAny(line, "=:")
		if idx < 0 {
			continue // no separator: not a kv line, skip
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if k != "" {
			kv[k] = v
		}
	}
	return kv
}
