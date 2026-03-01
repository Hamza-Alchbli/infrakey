package compose

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	MentionEnvFile = "env_file"
	MentionVolume  = "volume"
	MentionSecret  = "secret"
	MentionConfig  = "config"
	MentionCert    = "cert"
)

type PathMention struct {
	Kind             string
	Original         string
	ResolvedAbs      string
	OriginalAbsolute bool
}

type ParseResult struct {
	ComposePath string
	Mentions    []PathMention
}

type line struct {
	Raw    string
	Trim   string
	Indent int
}

var keyLineRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:\s*(.*)$`)

func ParseFile(composePath string) (ParseResult, error) {
	f, err := os.Open(composePath)
	if err != nil {
		return ParseResult{}, fmt.Errorf("open compose file: %w", err)
	}
	defer f.Close()

	var lines []line
	s := bufio.NewScanner(f)
	for s.Scan() {
		raw := s.Text()
		lines = append(lines, line{
			Raw:    raw,
			Trim:   strings.TrimSpace(raw),
			Indent: leadingIndent(raw),
		})
	}
	if err := s.Err(); err != nil {
		return ParseResult{}, fmt.Errorf("scan compose file: %w", err)
	}

	baseDir := filepath.Dir(composePath)
	seen := map[string]struct{}{}
	mentions := make([]PathMention, 0, 16)
	addMention := func(kind, original, resolved string, originalAbs bool) {
		if original == "" || resolved == "" {
			return
		}
		key := kind + "|" + original + "|" + resolved
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		mentions = append(mentions, PathMention{
			Kind:             kind,
			Original:         original,
			ResolvedAbs:      resolved,
			OriginalAbsolute: originalAbs,
		})
	}

	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if ln.Trim == "" || strings.HasPrefix(strings.TrimSpace(ln.Raw), "#") {
			continue
		}
		m := keyLineRe.FindStringSubmatch(ln.Trim)
		if len(m) != 3 {
			continue
		}
		key := m[1]
		value := strings.TrimSpace(stripYAMLComment(m[2]))

		switch key {
		case "env_file":
			vals, next := collectScalarValues(lines, i, ln.Indent, value)
			for _, v := range vals {
				resolved, origAbs, ok := resolvePath(baseDir, v)
				if ok {
					addMention(MentionEnvFile, v, resolved, origAbs)
				}
			}
			i = next
		case "volumes":
			sources, next := collectVolumeSources(lines, i, ln.Indent, value)
			for _, v := range sources {
				resolved, origAbs, ok := resolvePath(baseDir, v)
				if ok {
					addMention(MentionVolume, v, resolved, origAbs)
				}
			}
			i = next
		case "secrets", "configs":
			if ln.Indent != 0 {
				continue
			}
			kind := MentionSecret
			if key == "configs" {
				kind = MentionConfig
			}
			fileVals, next := collectTopLevelFileValues(lines, i, ln.Indent)
			for _, v := range fileVals {
				resolved, origAbs, ok := resolvePath(baseDir, v)
				if ok {
					addMention(kind, v, resolved, origAbs)
				}
			}
			i = next
		case "environment":
			vals, next := collectEnvironmentValues(lines, i, ln.Indent, value)
			for _, v := range vals {
				if !looksLikeCertPath(v) {
					continue
				}
				resolved, origAbs, ok := resolvePath(baseDir, v)
				if !ok {
					continue
				}
				if fi, err := os.Stat(resolved); err == nil && fi.Mode().IsRegular() {
					addMention(MentionCert, v, resolved, origAbs)
				}
			}
			i = next
		}
	}

	sort.Slice(mentions, func(i, j int) bool {
		if mentions[i].Kind != mentions[j].Kind {
			return mentions[i].Kind < mentions[j].Kind
		}
		if mentions[i].ResolvedAbs != mentions[j].ResolvedAbs {
			return mentions[i].ResolvedAbs < mentions[j].ResolvedAbs
		}
		return mentions[i].Original < mentions[j].Original
	})

	return ParseResult{ComposePath: composePath, Mentions: mentions}, nil
}

func leadingIndent(s string) int {
	count := 0
	for _, r := range s {
		if r == ' ' {
			count++
			continue
		}
		if r == '\t' {
			count += 2
			continue
		}
		break
	}
	return count
}

func stripYAMLComment(s string) string {
	inSingle := false
	inDouble := false
	for i, r := range s {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return strings.TrimSpace(s)
}

func unquoteYAML(s string) string {
	s = strings.TrimSpace(stripYAMLComment(s))
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"")
	s = strings.Trim(s, "'")
	return strings.TrimSpace(s)
}

func collectScalarValues(lines []line, idx, indent int, value string) ([]string, int) {
	var out []string
	value = strings.TrimSpace(value)
	if value != "" {
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			items := strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"), ",")
			for _, item := range items {
				v := unquoteYAML(item)
				if v != "" {
					out = append(out, v)
				}
			}
		} else {
			v := unquoteYAML(value)
			if v != "" {
				out = append(out, v)
			}
		}
		return out, idx
	}

	next := idx
	for j := idx + 1; j < len(lines); j++ {
		if lines[j].Trim == "" {
			continue
		}
		if lines[j].Indent <= indent {
			break
		}
		t := lines[j].Trim
		if strings.HasPrefix(t, "- ") {
			v := unquoteYAML(strings.TrimPrefix(t, "- "))
			if v != "" {
				out = append(out, v)
			}
		}
		next = j
	}
	return out, next
}

func collectTopLevelFileValues(lines []line, idx, indent int) ([]string, int) {
	var out []string
	next := idx
	for j := idx + 1; j < len(lines); j++ {
		if lines[j].Trim == "" {
			continue
		}
		if lines[j].Indent <= indent {
			break
		}
		m := keyLineRe.FindStringSubmatch(lines[j].Trim)
		if len(m) == 3 && m[1] == "file" {
			v := unquoteYAML(m[2])
			if v != "" {
				out = append(out, v)
			}
		}
		next = j
	}
	return out, next
}

func collectVolumeSources(lines []line, idx, indent int, value string) ([]string, int) {
	var out []string
	value = strings.TrimSpace(value)
	if value != "" {
		if src, ok := parseShortVolumeSource(unquoteYAML(value)); ok {
			out = append(out, src)
		}
		return out, idx
	}

	next := idx
	for j := idx + 1; j < len(lines); j++ {
		if lines[j].Trim == "" {
			continue
		}
		if lines[j].Indent <= indent {
			break
		}
		lineTrim := lines[j].Trim
		if !strings.HasPrefix(lineTrim, "- ") {
			next = j
			continue
		}
		itemIndent := lines[j].Indent
		item := strings.TrimSpace(strings.TrimPrefix(lineTrim, "- "))
		if item == "" {
			bind := parseVolumeMapItem(lines, j+1, itemIndent)
			if bind != "" {
				out = append(out, bind)
			}
			for k := j + 1; k < len(lines); k++ {
				if lines[k].Trim == "" {
					continue
				}
				if lines[k].Indent <= itemIndent {
					j = k - 1
					break
				}
				j = k
			}
			next = j
			continue
		}

		if maybeMapSyntax(item) {
			bind := parseInlineVolumeMap(item)
			if bind == "" {
				bind = parseVolumeMapItemWithInitial(lines, j+1, itemIndent, parseInlineKeyValues(item))
			}
			if bind != "" {
				out = append(out, bind)
			}
			for k := j + 1; k < len(lines); k++ {
				if lines[k].Trim == "" {
					continue
				}
				if lines[k].Indent <= itemIndent {
					j = k - 1
					break
				}
				j = k
			}
			next = j
			continue
		}

		if src, ok := parseShortVolumeSource(item); ok {
			out = append(out, src)
		}
		next = j
	}
	return out, next
}

func parseVolumeMapItem(lines []line, start, parentIndent int) string {
	return parseVolumeMapItemWithInitial(lines, start, parentIndent, map[string]string{})
}

func parseVolumeMapItemWithInitial(lines []line, start, parentIndent int, vals map[string]string) string {
	for j := start; j < len(lines); j++ {
		if lines[j].Trim == "" {
			continue
		}
		if lines[j].Indent <= parentIndent {
			break
		}
		m := keyLineRe.FindStringSubmatch(lines[j].Trim)
		if len(m) != 3 {
			continue
		}
		vals[m[1]] = unquoteYAML(m[2])
	}
	if t, ok := vals["type"]; ok && t != "bind" {
		return ""
	}
	source := vals["source"]
	if source == "" {
		source = vals["src"]
	}
	if source == "" {
		return ""
	}
	if !looksLikePath(source) {
		return ""
	}
	return source
}

func parseInlineKeyValues(item string) map[string]string {
	vals := map[string]string{}
	fields := strings.Split(item, ",")
	for _, f := range fields {
		parts := strings.SplitN(strings.TrimSpace(f), ":", 2)
		if len(parts) != 2 {
			continue
		}
		vals[strings.TrimSpace(parts[0])] = unquoteYAML(parts[1])
	}
	return vals
}

func parseInlineVolumeMap(item string) string {
	if !strings.Contains(item, "source:") && !strings.Contains(item, "src:") {
		return ""
	}
	if strings.Contains(item, "type:") && !strings.Contains(item, "type: bind") {
		return ""
	}
	fields := strings.Split(item, ",")
	vals := map[string]string{}
	for _, f := range fields {
		parts := strings.SplitN(strings.TrimSpace(f), ":", 2)
		if len(parts) != 2 {
			continue
		}
		vals[strings.TrimSpace(parts[0])] = unquoteYAML(parts[1])
	}
	src := vals["source"]
	if src == "" {
		src = vals["src"]
	}
	if !looksLikePath(src) {
		return ""
	}
	return src
}

func maybeMapSyntax(item string) bool {
	if strings.Contains(item, "source:") || strings.Contains(item, "src:") || strings.Contains(item, "type:") {
		return true
	}
	if strings.Contains(item, "target:") && !strings.Contains(item, "/") {
		return true
	}
	return false
}

func parseShortVolumeSource(item string) (string, bool) {
	item = unquoteYAML(item)
	if item == "" {
		return "", false
	}
	parts := strings.Split(item, ":")
	if len(parts) < 2 {
		return "", false
	}
	source := strings.TrimSpace(parts[0])
	if source == "" {
		return "", false
	}
	if !looksLikePath(source) {
		return "", false
	}
	return source, true
}

func looksLikePath(v string) bool {
	if v == "" {
		return false
	}
	if strings.Contains(v, "${") {
		return false
	}
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../") || strings.HasPrefix(v, "~/") {
		return true
	}
	return strings.HasPrefix(v, ".") || strings.Contains(v, "/")
}

func collectEnvironmentValues(lines []line, idx, indent int, value string) ([]string, int) {
	var out []string
	value = strings.TrimSpace(value)
	if value != "" {
		vals := parseInlineEnvironment(value)
		out = append(out, vals...)
		return out, idx
	}

	next := idx
	for j := idx + 1; j < len(lines); j++ {
		if lines[j].Trim == "" {
			continue
		}
		if lines[j].Indent <= indent {
			break
		}
		t := lines[j].Trim
		if strings.HasPrefix(t, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(t, "- "))
			if strings.Contains(item, "=") {
				parts := strings.SplitN(item, "=", 2)
				if len(parts) == 2 {
					out = append(out, unquoteYAML(parts[1]))
				}
			}
		} else {
			m := keyLineRe.FindStringSubmatch(t)
			if len(m) == 3 {
				out = append(out, unquoteYAML(m[2]))
			}
		}
		next = j
	}
	return out, next
}

func parseInlineEnvironment(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "{")
	v = strings.TrimSuffix(v, "}")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		kv := strings.SplitN(p, ":", 2)
		if len(kv) != 2 {
			continue
		}
		out = append(out, unquoteYAML(kv[1]))
	}
	return out
}

func looksLikeCertPath(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return strings.HasSuffix(v, ".pem") || strings.HasSuffix(v, ".crt") || strings.HasSuffix(v, ".key") || strings.HasSuffix(v, ".p12")
}

func resolvePath(baseDir, raw string) (resolved string, originalAbsolute bool, ok bool) {
	v := unquoteYAML(raw)
	if v == "" {
		return "", false, false
	}
	if strings.Contains(v, "${") {
		return "", false, false
	}
	if strings.HasPrefix(v, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, false
		}
		v = filepath.Join(home, strings.TrimPrefix(v, "~/"))
	}
	if filepath.IsAbs(v) {
		return filepath.Clean(v), true, true
	}
	return filepath.Clean(filepath.Join(baseDir, v)), false, true
}
