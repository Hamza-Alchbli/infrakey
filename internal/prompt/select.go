package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var errPromptInterrupted = errors.New("prompt interrupted")

type keyEvent int

const (
	keyUnknown keyEvent = iota
	keyUp
	keyDown
	keyEnter
	keySpace
	keyToggleAll
	keyCtrlC
)

func SelectOne(message string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("select prompt requires at least one option")
	}

	if isInteractiveTTY() {
		idx, err := selectOneTTY(message, options)
		if err == nil {
			return idx, nil
		}
		if !errors.Is(err, errPromptInterrupted) {
			printPromptWarning(fmt.Sprintf("interactive selector unavailable, falling back to line input: %v", err))
		}
		if errors.Is(err, errPromptInterrupted) {
			return 0, err
		}
	}
	return selectOneLine(message, options)
}

func MultiSelect(message string, options []string) ([]int, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("multi-select prompt requires at least one option")
	}

	if isInteractiveTTY() {
		selected, err := multiSelectTTY(message, options)
		if err == nil {
			return selected, nil
		}
		if !errors.Is(err, errPromptInterrupted) {
			printPromptWarning(fmt.Sprintf("interactive selector unavailable, falling back to line input: %v", err))
		}
		if errors.Is(err, errPromptInterrupted) {
			return nil, err
		}
	}
	return multiSelectLine(message, options)
}

func selectOneTTY(message string, options []string) (int, error) {
	selected := 0
	cursor := 0
	if err := withRawTerminal(func() error {
		for {
			renderSelectOne(message, options, cursor)
			key, err := readKey(os.Stdin)
			if err != nil {
				return err
			}
			switch key {
			case keyUp:
				if cursor == 0 {
					cursor = len(options) - 1
				} else {
					cursor--
				}
			case keyDown:
				cursor = (cursor + 1) % len(options)
			case keyEnter:
				selected = cursor
				return nil
			case keyCtrlC:
				return errPromptInterrupted
			}
		}
	}); err != nil {
		return 0, err
	}
	clearScreen(os.Stdout)
	return selected, nil
}

func multiSelectTTY(message string, options []string) ([]int, error) {
	cursor := 0
	selected := make([]bool, len(options))
	errorLine := ""

	if err := withRawTerminal(func() error {
		for {
			renderMultiSelect(message, options, selected, cursor, errorLine)
			key, err := readKey(os.Stdin)
			if err != nil {
				return err
			}
			errorLine = ""
			switch key {
			case keyUp:
				if cursor == 0 {
					cursor = len(options) - 1
				} else {
					cursor--
				}
			case keyDown:
				cursor = (cursor + 1) % len(options)
			case keySpace:
				selected[cursor] = !selected[cursor]
			case keyToggleAll:
				allSelected := true
				for _, on := range selected {
					if !on {
						allSelected = false
						break
					}
				}
				next := !allSelected
				for i := range selected {
					selected[i] = next
				}
			case keyEnter:
				indices := collectSelectedIndices(selected)
				if len(indices) == 0 {
					errorLine = "Select at least one app."
					continue
				}
				return nil
			case keyCtrlC:
				return errPromptInterrupted
			}
		}
	}); err != nil {
		return nil, err
	}
	clearScreen(os.Stdout)
	return collectSelectedIndices(selected), nil
}

func selectOneLine(message string, options []string) (int, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s\n", message)
		for i, opt := range options {
			fmt.Printf("  %d) %s\n", i+1, opt)
		}
		fmt.Printf("Enter choice [1-%d]: ", len(options))

		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || n < 1 || n > len(options) {
			fmt.Printf("Invalid choice.\n\n")
			continue
		}
		return n - 1, nil
	}
}

func multiSelectLine(message string, options []string) ([]int, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s\n", message)
		for i, opt := range options {
			fmt.Printf("  %d) %s\n", i+1, opt)
		}
		fmt.Printf("Enter comma-separated choices (e.g. 1,3,4): ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Printf("Select at least one app.\n\n")
			continue
		}
		parts := strings.Split(line, ",")
		seen := map[int]struct{}{}
		out := make([]int, 0, len(parts))
		valid := true
		for _, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 1 || n > len(options) {
				valid = false
				break
			}
			idx := n - 1
			if _, ok := seen[idx]; ok {
				continue
			}
			seen[idx] = struct{}{}
			out = append(out, idx)
		}
		if !valid || len(out) == 0 {
			fmt.Printf("Invalid selection.\n\n")
			continue
		}
		return out, nil
	}
}

func isInteractiveTTY() bool {
	inStat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	outStat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (inStat.Mode()&os.ModeCharDevice) != 0 && (outStat.Mode()&os.ModeCharDevice) != 0
}

func withRawTerminal(fn func() error) error {
	restore, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer restore()
	return fn()
}

func renderSelectOne(message string, options []string, cursor int) {
	clearScreen(os.Stdout)
	fmt.Printf("%s\n", styleHeader(message))
	fmt.Println(styleHint("Use ↑/↓ to move, Enter to confirm."))
	fmt.Println()
	for i, opt := range options {
		prefix := " "
		if i == cursor {
			prefix = styleCursor(">")
		}
		line := fmt.Sprintf("%s %s", prefix, opt)
		if i == cursor {
			line = styleSelected(line)
		}
		fmt.Printf("%s\n", line)
	}
}

func renderMultiSelect(message string, options []string, selected []bool, cursor int, errorLine string) {
	clearScreen(os.Stdout)
	fmt.Printf("%s\n", styleHeader(message))
	fmt.Println(styleHint("Use ↑/↓ to move, Space to toggle, A to toggle all, Enter to confirm."))
	fmt.Println()
	for i, opt := range options {
		prefix := " "
		if i == cursor {
			prefix = styleCursor(">")
		}
		checked := "[ ]"
		if selected[i] {
			checked = styleChecked("[x]")
		}
		line := fmt.Sprintf("%s %s %s", prefix, checked, opt)
		if i == cursor {
			line = styleSelected(line)
		}
		fmt.Printf("%s\n", line)
	}
	if errorLine != "" {
		fmt.Printf("\n%s\n", styleError(errorLine))
	}
}

func clearScreen(w io.Writer) {
	_, _ = fmt.Fprint(w, "\x1b[2J\x1b[H")
}

func readKey(r io.Reader) (keyEvent, error) {
	var b [1]byte
	if _, err := r.Read(b[:]); err != nil {
		return keyUnknown, err
	}
	switch b[0] {
	case 3:
		return keyCtrlC, nil
	case '\r', '\n':
		return keyEnter, nil
	case ' ':
		return keySpace, nil
	case 'a', 'A':
		return keyToggleAll, nil
	case 'k', 'K':
		return keyUp, nil
	case 'j', 'J':
		return keyDown, nil
	case 27:
		var seq [2]byte
		if _, err := io.ReadFull(r, seq[:1]); err != nil {
			return keyUnknown, nil
		}
		if seq[0] != '[' {
			return keyUnknown, nil
		}
		if _, err := io.ReadFull(r, seq[1:2]); err != nil {
			return keyUnknown, nil
		}
		switch seq[1] {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		default:
			return keyUnknown, nil
		}
	default:
		return keyUnknown, nil
	}
}

func collectSelectedIndices(selected []bool) []int {
	out := make([]int, 0, len(selected))
	for i, v := range selected {
		if v {
			out = append(out, i)
		}
	}
	return out
}

func printPromptWarning(message string) {
	label := "warning:"
	if promptSupportsColor() {
		label = styleANSI("33", label)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", label, message)
}

func styleHeader(s string) string  { return styleANSI("36", s) }
func styleHint(s string) string    { return styleANSI("2", s) }
func styleCursor(s string) string  { return styleANSI("32", s) }
func styleChecked(s string) string { return styleANSI("32", s) }
func styleSelected(s string) string {
	return styleANSI("1", s)
}
func styleError(s string) string { return styleANSI("31", s) }

func styleANSI(code, s string) string {
	if !promptSupportsColor() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func promptSupportsColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	outStat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (outStat.Mode() & os.ModeCharDevice) != 0
}
