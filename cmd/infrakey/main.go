package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	if err := fs.Parse(args); err != nil {
		return bundle.SnapshotOptions{}, err
	}
	if fs.NArg() > 0 {
		return bundle.SnapshotOptions{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return bundle.SnapshotOptions{
		RootDir:     *root,
		OutBundle:   *out,
		Recipient:   *recipient,
		IdentityOut: *identityOut,
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
	if *bundlePath == "" || *identityKey == "" || *target == "" {
		return restore.Options{}, fmt.Errorf("--bundle, --identity-key and --target are required")
	}
	if fs.NArg() > 0 {
		return restore.Options{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return restore.Options{
		BundlePath:      *bundlePath,
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
	selectedCompose, err := promptSnapshotSelection(opts.RootDir)
	if err != nil {
		return err
	}
	opts.ComposePaths = selectedCompose
	printSnapshotKeySecurityNotice(opts, false)

	summary, err := bundle.CreateSnapshot(opts)
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
	if summary.SkippedMissing > 0 {
		fmt.Printf("- Skipped missing references: %d\n", summary.SkippedMissing)
	}
	return nil
}

func runRestore(args []string) error {
	opts, err := parseRestoreFlags(args, "restore")
	if err != nil {
		return err
	}

	summary, err := restore.Run(opts)
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
	if *bundlePath == "" || *identityKey == "" {
		return fmt.Errorf("--bundle and --identity-key are required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	res, err := bundle.Inspect(bundle.InspectOptions{
		BundlePath:      *bundlePath,
		IdentityKeyPath: *identityKey,
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
	selectedCompose, err := promptSnapshotSelection(opts.RootDir)
	if err != nil {
		return err
	}
	opts.ComposePaths = selectedCompose
	printSnapshotKeySecurityNotice(opts, true)

	plan, err := bundle.PlanSnapshot(opts)
	if err != nil {
		return err
	}

	fmt.Printf("Dry run snapshot\n")
	fmt.Printf("- Root: %s\n", plan.RootDir)
	fmt.Printf("- Bundle output: %s\n", plan.OutBundle)
	if plan.WouldGenerateIdentity {
		fmt.Printf("- Would generate identity key: %s\n", plan.IdentityPath)
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

func promptSnapshotSelection(root string) ([]string, error) {
	result, err := appselect.Discover(root)
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
	fmt.Fprintln(w, "  infrakey snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>]")
	fmt.Fprintln(w, "  infrakey restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]")
	fmt.Fprintln(w, "  infrakey inspect --bundle <vault.bundle> --identity-key <identity.key>")
	fmt.Fprintln(w, "  infrakey dry-run snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>]")
	fmt.Fprintln(w, "  infrakey dry-run restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]")
}
