package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type agentConfigFile struct {
	Label string
	Path  string
}

func listAgentConfigFiles(root string) ([]agentConfigFile, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "agents"
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents directory %q: %w", root, err)
	}

	var files []agentConfigFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentDir := filepath.Join(root, entry.Name())
		agentEntries, err := os.ReadDir(agentDir)
		if err != nil {
			return nil, fmt.Errorf("read agent directory %q: %w", agentDir, err)
		}
		for _, file := range agentEntries {
			if file.IsDir() || !isYAMLConfigFile(file.Name()) {
				continue
			}
			files = append(files, agentConfigFile{
				Label: filepath.ToSlash(filepath.Join(entry.Name(), file.Name())),
				Path:  filepath.Join(agentDir, file.Name()),
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Label < files[j].Label
	})
	return files, nil
}
