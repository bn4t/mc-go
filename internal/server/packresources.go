package server

import (
	"os"
	"path/filepath"
)

// folderPackRoots preserves the existing startup folder-pack discovery policy:
// lexicographic directories with an accessible pack.mcmeta. Pack selection,
// metadata-format validation, ZIP packs and reload are separate workflows.
func folderPackRoots(worldDir string) []string {
	if worldDir == "" {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(worldDir, "datapacks"))
	var roots []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(worldDir, "datapacks", entry.Name())
		if _, err := os.Stat(filepath.Join(root, "pack.mcmeta")); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}
