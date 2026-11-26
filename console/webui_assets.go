package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

//go:embed all:frontend
var colabOneEmbeddedFrontend embed.FS

func hasColabOneFrontendIndex(fsys fs.FS) bool {
	if fsys == nil {
		return false
	}
	info, err := fs.Stat(fsys, "index.html")
	return err == nil && !info.IsDir()
}

// colabOneFrontendFS resolves the built Vue SPA assets from (in order):
// 1. MYGO_FRONTEND_DIST override directory (development / custom staging)
// 2. The single-binary embedded console/frontend directory
// 3. Adjacent ../frontend/dist or ./frontend/dist directories on disk (dev fallback)
func colabOneFrontendFS() fs.FS {
	if override := strings.TrimSpace(os.Getenv("MYGO_FRONTEND_DIST")); override != "" {
		disk := os.DirFS(override)
		if hasColabOneFrontendIndex(disk) {
			return disk
		}
	}
	if sub, err := fs.Sub(colabOneEmbeddedFrontend, "frontend/dist"); err == nil && hasColabOneFrontendIndex(sub) {
		return sub
	}
	if sub, err := fs.Sub(colabOneEmbeddedFrontend, "frontend"); err == nil && hasColabOneFrontendIndex(sub) {
		return sub
	}
	cwd, _ := os.Getwd()
	root := colabOneRootDir()
	candidates := []string{
		filepath.Join(root, "frontend", "dist"),
		filepath.Join(cwd, "..", "frontend", "dist"),
		filepath.Join(cwd, "frontend", "dist"),
	}
	for _, dir := range candidates {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		disk := os.DirFS(dir)
		if hasColabOneFrontendIndex(disk) {
			return disk
		}
	}
	return nil
}

func init() {
	if fsys := colabOneFrontendFS(); fsys != nil {
		mygo.SetFrontend(fsys)
	}
}
