package appselect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"infrakey/internal/compose"
	"infrakey/internal/discovery"
)

type App struct {
	Name               string
	ComposePath        string
	EstimatedSizeBytes int64
}

type Options struct {
	IncludeVolumes bool
}

type Result struct {
	RootDir                 string
	Apps                    []App
	TotalEstimatedSizeBytes int64
}

func Discover(root string, opts Options) (Result, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve root: %w", err)
	}
	st, err := os.Stat(rootAbs)
	if err != nil {
		return Result{}, fmt.Errorf("stat root: %w", err)
	}
	if !st.IsDir() {
		return Result{}, fmt.Errorf("root path must be a directory")
	}

	composeFiles, err := discovery.DiscoverComposeFiles(rootAbs)
	if err != nil {
		return Result{}, err
	}
	if len(composeFiles) == 0 {
		return Result{}, fmt.Errorf("no compose files found under %s", rootAbs)
	}

	labels := buildAppLabels(rootAbs, composeFiles)
	seenGlobal := map[string]struct{}{}
	apps := make([]App, 0, len(composeFiles))
	var totalBytes int64

	for _, composePath := range composeFiles {
		seenLocal := map[string]struct{}{}
		seenLocal[composePath] = struct{}{}

		parsed, err := compose.ParseFile(composePath)
		if err != nil {
			return Result{}, fmt.Errorf("parse compose file %s: %w", composePath, err)
		}
		for _, m := range parsed.Mentions {
			if !includeInEstimate(m.Kind, opts.IncludeVolumes) {
				continue
			}
			if _, ok := seenLocal[m.ResolvedAbs]; ok {
				continue
			}
			seenLocal[m.ResolvedAbs] = struct{}{}
		}
		if opts.IncludeVolumes {
			seenLocal[filepath.Dir(composePath)] = struct{}{}
		}

		var appBytes int64
		for p := range seenLocal {
			size, ok := estimatePathSize(p)
			if !ok {
				continue
			}
			appBytes += size
			if _, ok := seenGlobal[p]; ok {
				continue
			}
			seenGlobal[p] = struct{}{}
			totalBytes += size
		}

		apps = append(apps, App{
			Name:               labels[composePath],
			ComposePath:        composePath,
			EstimatedSizeBytes: appBytes,
		})
	}

	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Name != apps[j].Name {
			return apps[i].Name < apps[j].Name
		}
		return apps[i].ComposePath < apps[j].ComposePath
	})

	return Result{
		RootDir:                 rootAbs,
		Apps:                    apps,
		TotalEstimatedSizeBytes: totalBytes,
	}, nil
}

func buildAppLabels(root string, composeFiles []string) map[string]string {
	base := make(map[string]string, len(composeFiles))
	count := map[string]int{}

	for _, p := range composeFiles {
		relDir := relativeDir(root, p)
		name := relDir
		if name == "." {
			name = filepath.Base(p)
		}
		name = filepath.ToSlash(name)
		base[p] = name
		count[name]++
	}

	out := make(map[string]string, len(composeFiles))
	for _, p := range composeFiles {
		name := base[p]
		if count[name] > 1 {
			name = filepath.ToSlash(filepath.Join(relativeDir(root, p), filepath.Base(p)))
		}
		out[p] = name
	}
	return out
}

func relativeDir(root, composePath string) string {
	relDir, err := filepath.Rel(root, filepath.Dir(composePath))
	if err != nil || relDir == "" {
		return "."
	}
	return relDir
}

func includeInEstimate(kind string, includeVolumes bool) bool {
	switch kind {
	case compose.MentionEnvFile, compose.MentionSecret, compose.MentionConfig, compose.MentionCert:
		return true
	case compose.MentionVolume:
		return includeVolumes
	default:
		return false
	}
}

func estimatePathSize(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	if fi.Mode().IsRegular() {
		return fi.Size(), true
	}
	if !fi.IsDir() {
		return 0, false
	}

	var total int64
	err = filepath.WalkDir(path, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, false
	}
	return total, true
}
