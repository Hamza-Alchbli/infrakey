package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"infrakey/internal/appselect"
	"infrakey/internal/bundle"
	"infrakey/internal/prompt"
	"infrakey/internal/restore"
)

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "snapshot":
		err = runSnapshot(os.Args[2:])
	case "restore":
		err = runRestore(os.Args[2:])
	case "inspect":
		err = runInspect(os.Args[2:])
	case "dry-run":
		err = runDryRun(os.Args[2:])
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func parseSnapshotFlags(args []string, name string) (bundle.SnapshotOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "Root directory to scan for compose files")
	out := fs.String("out", "vault.bundle", "Output encrypted bundle path")
	recipient := fs.String("recipient", "", "Age recipient public key")
	identityOut := fs.String("identity-out", "identity.key", "Identity key output path (used when --recipient is not set)")
	fullCopy := fs.Bool("full-copy", false, "Include bind-mounted volume data (files/directories)")
	chunkSize := fs.String("chunk-size", "", "Split encrypted output into chunks (e.g. 2GB). Default in full-copy mode: 2GB")
	if err := fs.Parse(args); err != nil {
		return bundle.SnapshotOptions{}, err
	}
	if fs.NArg() > 0 {
		return bundle.SnapshotOptions{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	chunkBytes, err := parseChunkSize(*chunkSize, *fullCopy)
	if err != nil {
		return bundle.SnapshotOptions{}, err
	}
	return bundle.SnapshotOptions{
		RootDir:        *root,
		OutBundle:      *out,
		Recipient:      *recipient,
		IdentityOut:    *identityOut,
		FullCopy:       *fullCopy,
		ChunkSizeBytes: chunkBytes,
	}, nil
}

func parseRestoreFlags(args []string, name string) (restore.Options, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bundlePath := fs.String("bundle", "", "Encrypted bundle path")
	identityKey := fs.String("identity-key", "", "Identity key path")
	target := fs.String("target", "", "Restore target directory")
	yes := fs.Bool("yes", false, "Non-interactive mode")
	includeExternal := fs.String("include-external", "", "External file policy: all|none")
	if err := fs.Parse(args); err != nil {
		return restore.Options{}, err
	}
	normalizedBundle := normalizeBundlePathInput(*bundlePath)
	if normalizedBundle == "" || *identityKey == "" || *target == "" {
		return restore.Options{}, fmt.Errorf("--bundle, --identity-key and --target are required")
	}
	if fs.NArg() > 0 {
		return restore.Options{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return restore.Options{
		BundlePath:      normalizedBundle,
		IdentityKeyPath: *identityKey,
		TargetDir:       *target,
		Yes:             *yes,
		IncludeExternal: *includeExternal,
	}, nil
}

func runSnapshot(args []string) error {
	opts, err := parseSnapshotFlags(args, "snapshot")
	if err != nil {
		return err
	}
	selectedCompose, err := promptSnapshotSelection(opts.RootDir, opts.FullCopy)
	if err != nil {
		return err
	}
	opts.ComposePaths = selectedCompose

	progress := newProgressPrinter("Snapshot in progress")
	opts.Progress = progress.UpdateSnapshot

	summary, err := bundle.CreateSnapshot(opts)
	progress.Stop(err)
	if err != nil {
		return err
	}

	outAbs, _ := filepath.Abs(opts.OutBundle)
	fmt.Printf("%s\n", successLabel("Snapshot complete"))
	fmt.Printf("- Bundle: %s\n", outAbs)
	if summary.GeneratedIdentityPath != "" {
		fmt.Printf("- Identity key: %s\n", summary.GeneratedIdentityPath)
	}
	fmt.Printf("- Compose files: %d\n", summary.ComposeFiles)
	fmt.Printf("- Captured files: %d\n", summary.CapturedFiles)
	fmt.Printf("- External files: %d\n", summary.ExternalFiles)
	if opts.FullCopy {
		fmt.Printf("- Full copy mode: enabled\n")
	}
	if summary.Chunked {
		fmt.Printf("- Bundle chunks: %d\n", summary.ChunkCount)
		for _, p := range summary.ChunkPaths {
			fmt.Printf("  - %s\n", p)
		}
	}
	if summary.SkippedMissing > 0 {
		fmt.Printf("- Skipped missing references: %d\n", summary.SkippedMissing)
	}
	printSnapshotKeySecurityNotice(opts, false)
	return nil
}

func runRestore(args []string) error {
	opts, err := parseRestoreFlags(args, "restore")
	if err != nil {
		return err
	}

	progress := newProgressPrinter("Restore in progress")
	opts.Progress = progress.UpdateRestore

	summary, err := restore.Run(opts)
	progress.Stop(err)
	if err != nil {
		return err
	}

	targetAbs, _ := filepath.Abs(opts.TargetDir)
	fmt.Printf("Restore complete\n")
	fmt.Printf("- Target: %s\n", targetAbs)
	fmt.Printf("- Restored entries: %d\n", summary.RestoredEntries)
	fmt.Printf("- External references total: %d\n", summary.ExternalTotal)
	fmt.Printf("- External references skipped: %d\n", summary.SkippedExternal)
	return nil
}

func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bundlePath := fs.String("bundle", "", "Encrypted bundle path")
	identityKey := fs.String("identity-key", "", "Identity key path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	normalizedBundle := normalizeBundlePathInput(*bundlePath)
	if normalizedBundle == "" || *identityKey == "" {
		return fmt.Errorf("--bundle and --identity-key are required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	res, err := runWithSpinner("Inspect in progress", func() (bundle.InspectResult, error) {
		return bundle.Inspect(bundle.InspectOptions{
			BundlePath:      normalizedBundle,
			IdentityKeyPath: *identityKey,
		})
	})
	if err != nil {
		return err
	}

	fmt.Printf("Bundle metadata\n")
	fmt.Printf("- pciVersion: %s\n", res.Manifest.PCIVersion)
	fmt.Printf("- snapshotId: %s\n", res.Manifest.SnapshotID)
	fmt.Printf("- createdAt: %s\n", res.Manifest.CreatedAt)
	fmt.Printf("- sourceRoot: %s\n", res.Manifest.SourceRoot)
	fmt.Printf("- entries: %d\n", len(res.Entries))
	fmt.Printf("- external entries: %d\n", len(res.External))

	fmt.Printf("\nPlanned restore tree\n")
	for _, e := range res.Entries {
		fmt.Printf("- <target>/%s (%s)\n", e.RestoreRelPath, e.Kind)
	}

	if len(res.External) > 0 {
		fmt.Printf("\nExternal references\n")
		for _, e := range res.External {
			fmt.Printf("- %s\n", e.SourceAbsPath)
		}
	}
	return nil
}

func runDryRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("dry-run requires a subcommand: snapshot or restore")
	}
	switch args[0] {
	case "snapshot":
		return runDryRunSnapshot(args[1:])
	case "restore":
		return runDryRunRestore(args[1:])
	default:
		return fmt.Errorf("unknown dry-run subcommand %q (expected snapshot or restore)", args[0])
	}
}

func runDryRunSnapshot(args []string) error {
	opts, err := parseSnapshotFlags(args, "dry-run snapshot")
	if err != nil {
		return err
	}
	selectedCompose, err := promptSnapshotSelection(opts.RootDir, opts.FullCopy)
	if err != nil {
		return err
	}
	opts.ComposePaths = selectedCompose

	plan, err := runWithSpinner("Dry-run planning in progress", func() (bundle.SnapshotPlan, error) {
		return bundle.PlanSnapshot(opts)
	})
	if err != nil {
		return err
	}

	fmt.Printf("Dry run snapshot\n")
	fmt.Printf("- Root: %s\n", plan.RootDir)
	fmt.Printf("- Bundle output: %s\n", plan.OutBundle)
	if plan.WouldGenerateIdentity {
		fmt.Printf("- Would generate identity key: %s\n", plan.IdentityPath)
	}
	if opts.FullCopy {
		fmt.Printf("- Full copy mode: enabled\n")
	}
	if opts.ChunkSizeBytes > 0 {
		fmt.Printf("- Chunk size: %s\n", humanBytes(opts.ChunkSizeBytes))
	}
	fmt.Printf("- Compose files found: %d\n", len(plan.ComposePaths))
	fmt.Printf("- Captured entries: %d\n", len(plan.Entries))
	fmt.Printf("- External entries: %d\n", len(plan.ExternalEntries))
	if plan.SkippedMissing > 0 {
		fmt.Printf("- Missing referenced files (would be skipped): %d\n", plan.SkippedMissing)
	}

	fmt.Printf("\nCompose files\n")
	for _, p := range plan.ComposePaths {
		fmt.Printf("- %s\n", p)
	}

	fmt.Printf("\nPlanned manifest entries\n")
	for _, e := range plan.Entries {
		fmt.Printf("- %s (%s) -> %s\n", e.SourceAbsPath, e.Kind, e.RestoreRelPath)
	}
	printSnapshotKeySecurityNotice(opts, true)
	return nil
}

func runDryRunRestore(args []string) error {
	opts, err := parseRestoreFlags(args, "dry-run restore")
	if err != nil {
		return err
	}

	plan, err := restore.PlanRestore(opts)
	if err != nil {
		return err
	}

	fmt.Printf("Dry run restore\n")
	fmt.Printf("- Target: %s\n", plan.TargetPath)
	fmt.Printf("- Total entries in bundle: %d\n", len(plan.Manifest.Entries))
	fmt.Printf("- Entries to restore: %d\n", len(plan.RestoredEntries))
	fmt.Printf("- External entries skipped: %d\n", len(plan.SkippedExternal))

	fmt.Printf("\nEntries that would be restored\n")
	for _, e := range plan.RestoredEntries {
		fmt.Printf("- <target>/%s (%s)\n", e.RestoreRelPath, e.Kind)
	}
	if len(plan.SkippedExternal) > 0 {
		fmt.Printf("\nExternal entries that would be skipped\n")
		for _, e := range plan.SkippedExternal {
			fmt.Printf("- %s\n", e.SourceAbsPath)
		}
	}
	return nil
}

func promptSnapshotSelection(root string, includeVolumes bool) ([]string, error) {
	result, err := appselect.Discover(root, appselect.Options{
		IncludeVolumes: includeVolumes,
	})
	if err != nil {
		return nil, err
	}

	scopeOptions := []string{
		fmt.Sprintf("All compose apps (%d) [%s]", len(result.Apps), humanBytes(result.TotalEstimatedSizeBytes)),
		"Select compose apps manually",
	}
	scopeIdx, err := prompt.SelectOne("Snapshot scope", scopeOptions)
	if err != nil {
		return nil, err
	}
	if scopeIdx == 0 {
		return allComposePaths(result.Apps), nil
	}

	manualLabels := make([]string, 0, len(result.Apps))
	for _, app := range result.Apps {
		manualLabels = append(manualLabels, fmt.Sprintf("%s [%s]", app.Name, humanBytes(app.EstimatedSizeBytes)))
	}
	selected, err := prompt.MultiSelect("Select compose apps", manualLabels)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(selected))
	for _, idx := range selected {
		if idx < 0 || idx >= len(result.Apps) {
			return nil, fmt.Errorf("invalid app selection index %d", idx)
		}
		paths = append(paths, result.Apps[idx].ComposePath)
	}
	sort.Strings(paths)
	return paths, nil
}

func allComposePaths(apps []appselect.App) []string {
	out := make([]string, 0, len(apps))
	for _, app := range apps {
		out = append(out, app.ComposePath)
	}
	sort.Strings(out)
	return out
}

func parseChunkSize(raw string, fullCopy bool) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if fullCopy {
			return 2 * 1024 * 1024 * 1024, nil
		}
		return 0, nil
	}
	n, err := parseByteSize(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid --chunk-size %q: %w", raw, err)
	}
	return n, nil
}

func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	if s == "0" {
		return 0, nil
	}
	mult := int64(1)
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"TIB", 1024 * 1024 * 1024 * 1024},
		{"GIB", 1024 * 1024 * 1024},
		{"MIB", 1024 * 1024},
		{"KIB", 1024},
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"T", 1024 * 1024 * 1024 * 1024},
		{"G", 1024 * 1024 * 1024},
		{"M", 1024 * 1024},
		{"K", 1024},
		{"B", 1},
	}
	for _, def := range suffixes {
		suffix := def.suffix
		if strings.HasSuffix(s, suffix) {
			mult = def.mult
			s = strings.TrimSpace(strings.TrimSuffix(s, suffix))
			break
		}
	}
	if s == "" {
		return 0, fmt.Errorf("missing numeric size")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("size must be non-negative")
	}
	if mult > 1 && n > (1<<63-1)/mult {
		return 0, fmt.Errorf("size overflow")
	}
	return n * mult, nil
}

func normalizeBundlePathInput(in string) string {
	p := strings.TrimSpace(in)
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if strings.HasSuffix(clean, ".parts") {
		return strings.TrimSuffix(clean, ".parts")
	}
	return clean
}

func runWithSpinner[T any](label string, fn func() (T, error)) (T, error) {
	if !isTTY(os.Stderr) {
		return fn()
	}

	done := make(chan struct{})
	var out T
	var err error
	start := time.Now()
	go func() {
		out, err = fn()
		close(done)
	}()

	frames := []rune{'|', '/', '-', '\\'}
	ticker := time.NewTicker(140 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-done:
			status := "done"
			if err != nil {
				status = "failed"
			}
			fmt.Fprintf(os.Stderr, "\r%s %s... %s (%s)\n", infoLabel("info:"), label, status, elapsedShort(start))
			return out, err
		case <-ticker.C:
			fmt.Fprintf(os.Stderr, "\r%s %s... %c %s", infoLabel("info:"), label, frames[i%len(frames)], elapsedShort(start))
			i++
		}
	}
}

func elapsedShort(start time.Time) string {
	d := time.Since(start).Round(time.Second)
	if d < time.Second {
		d = time.Second
	}
	return d.String()
}

type progressPrinter struct {
	label     string
	enabled   bool
	startedAt time.Time

	mu         sync.Mutex
	stage      string
	bytesDone  int64
	bytesTotal int64
	stageStart time.Time

	tickerStop chan struct{}

	lastLineLen int
	stopped     bool
}

func newProgressPrinter(label string) *progressPrinter {
	p := &progressPrinter{
		label:      label,
		enabled:    isTTY(os.Stderr),
		startedAt:  time.Now(),
		stage:      "starting",
		stageStart: time.Now(),
		tickerStop: make(chan struct{}),
	}
	if p.enabled {
		go p.tickLoop()
	}
	return p
}

func (p *progressPrinter) UpdateSnapshot(ev bundle.ProgressEvent) {
	p.update(ev.Stage, ev.BytesDone, ev.BytesTotal)
}

func (p *progressPrinter) UpdateRestore(ev restore.ProgressEvent) {
	p.update(ev.Stage, ev.BytesDone, ev.BytesTotal)
}

func (p *progressPrinter) update(stage string, done, total int64) {
	if !p.enabled {
		return
	}
	now := time.Now()
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	if stage != "" && stage != p.stage {
		p.stage = stage
		p.stageStart = now
	}
	p.bytesDone = done
	p.bytesTotal = total
	line, lineLen := p.renderLineLocked(now)
	p.lastLineLen = lineLen
	p.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\r%s", line)
}

func (p *progressPrinter) Stop(runErr error) {
	if !p.enabled {
		return
	}
	closeTicker := false
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	closeTicker = true
	elapsed := elapsedShort(p.startedAt)
	lastLineLen := p.lastLineLen
	p.mu.Unlock()
	if closeTicker {
		close(p.tickerStop)
	}

	status := "done"
	if runErr != nil {
		status = "failed"
	}
	line := fmt.Sprintf("%s %s... %s (%s)", infoLabel("info:"), p.label, status, elapsed)
	if pad := lastLineLen - len(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	fmt.Fprintf(os.Stderr, "\r%s\n", line)
}

func (p *progressPrinter) tickLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.tickerStop:
			return
		case now := <-ticker.C:
			line, lineLen, ok := p.renderCurrent(now)
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "\r%s", line)
			p.mu.Lock()
			p.lastLineLen = lineLen
			p.mu.Unlock()
		}
	}
}

func (p *progressPrinter) renderCurrent(now time.Time) (string, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return "", 0, false
	}
	line, lineLen := p.renderLineLocked(now)
	return line, lineLen, true
}

func (p *progressPrinter) renderLineLocked(now time.Time) (string, int) {
	stage := p.stage
	if stage == "" {
		stage = "working"
	}
	stageElapsed := now.Sub(p.stageStart)
	if stageElapsed <= 0 {
		stageElapsed = time.Millisecond
	}
	totalElapsed := elapsedShort(p.startedAt)
	throughput := float64(p.bytesDone) / stageElapsed.Seconds()
	if math.IsNaN(throughput) || math.IsInf(throughput, 0) || throughput < 0 {
		throughput = 0
	}

	progressPart := ""
	if p.bytesTotal > 0 {
		pct := float64(0)
		if p.bytesTotal > 0 {
			pct = (float64(p.bytesDone) / float64(p.bytesTotal)) * 100
		}
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		progressPart = fmt.Sprintf(" %s / %s (%.0f%%)", humanBytes(p.bytesDone), humanBytes(p.bytesTotal), pct)
	} else if p.bytesDone > 0 {
		progressPart = fmt.Sprintf(" %s", humanBytes(p.bytesDone))
	}
	speedPart := ""
	if throughput > 0 && p.bytesDone > 0 {
		speedPart = fmt.Sprintf(" @ %s/s", humanBytes(int64(throughput)))
	}
	line := fmt.Sprintf("%s %s [%s] %s%s%s", infoLabel("info:"), p.label, stage, totalElapsed, progressPart, speedPart)
	if pad := p.lastLineLen - len(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line, len(line)
}

func humanBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(size)
	u := -1
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[u])
	}
	return fmt.Sprintf("%.1f %s", v, units[u])
}

func printSnapshotKeySecurityNotice(opts bundle.SnapshotOptions, dryRun bool) {
	if strings.TrimSpace(opts.Recipient) != "" {
		return
	}

	identityOut := opts.IdentityOut
	if strings.TrimSpace(identityOut) == "" {
		identityOut = "identity.key"
	}
	identityAbs, err := filepath.Abs(identityOut)
	if err != nil {
		return
	}
	bundleAbs, err := filepath.Abs(opts.OutBundle)
	if err != nil {
		return
	}

	action := "will generate"
	if dryRun {
		action = "would generate"
	}
	fmt.Fprintf(os.Stderr, "%s no --recipient provided; snapshot %s a new identity key at %s\n", infoLabel("info:"), action, identityAbs)
	fmt.Fprintf(os.Stderr, "%s keep this identity key private and separate from your encrypted bundle.\n", infoLabel("info:"))

	if filepath.Dir(identityAbs) == filepath.Dir(bundleAbs) {
		fmt.Fprintf(os.Stderr, "%s identity key and bundle are in the same directory: %s\n", warningLabel("warning:"), filepath.Dir(identityAbs))
		fmt.Fprintf(os.Stderr, "%s if both files are copied together, encryption protection is weakened.\n", warningLabel("warning:"))
		fmt.Fprintf(os.Stderr, "%s recommended: store key separately (password manager/USB/offline) and transfer via a different channel.\n", warningLabel("warning:"))
	}
}

func infoLabel(text string) string {
	return colorizeFor(os.Stderr, "36", text)
}

func warningLabel(text string) string {
	return colorizeFor(os.Stderr, "33", text)
}

func successLabel(text string) string {
	return colorizeFor(os.Stdout, "32", text)
}

func colorizeFor(f *os.File, colorCode, text string) string {
	if !supportsColor(f) {
		return text
	}
	return "\x1b[" + colorCode + "m" + text + "\x1b[0m"
}

func supportsColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY(f)
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "InfraKey (Linux-only MVP)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  infrakey snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>] [--full-copy] [--chunk-size <size>]")
	fmt.Fprintln(w, "  infrakey restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]")
	fmt.Fprintln(w, "  infrakey inspect --bundle <vault.bundle> --identity-key <identity.key>")
	fmt.Fprintln(w, "  infrakey dry-run snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>] [--full-copy] [--chunk-size <size>]")
	fmt.Fprintln(w, "  infrakey dry-run restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]")
}
