package discovery

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
)

var composeNames = map[string]struct{}{
	"docker-compose.yml":  {},
	"docker-compose.yaml": {},
	"compose.yml":         {},
	"compose.yaml":        {},
}

var skipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	".cache":       {},
	"dist":         {},
	"build":        {},
}

func DiscoverComposeFiles(root string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	var out []string
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return fs.SkipDir
			}
			return nil
		}
		if _, ok := composeNames[d.Name()]; ok {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover compose files: %w", err)
	}
	sort.Strings(out)
	return out, nil
}
