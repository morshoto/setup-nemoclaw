//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package prompt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	menuKeyUnknown = iota
	menuKeyUp
	menuKeyDown
	menuKeyEnter
	menuKeyInterrupt
	menuKeyDigit
	menuKeyBackspace
	menuKeyChar
)

const menuWindowSize = 12

type menuKey struct {
	kind  int
	index int
	ch    rune
}

func (s *Session) canUseCursorMenu() bool {
	return s.inFile != nil && s.outFile != nil && isTerminalFile(s.inFile) && isTerminalFile(s.outFile)
}

func (s *Session) selectWithCursor(label string, options []string, defaultValue string) (string, error) {
	if len(options) == 1 {
		return options[0], nil
	}

	restore, err := s.enableRawMode()
	if err != nil {
		return "", errCursorMenuUnavailable
	}
	defer func() {
		_ = restore()
	}()

	selected := 0
	for i, option := range options {
		if option == defaultValue {
			selected = i
			break
		}
	}

	lines := s.renderMenu(label, options, defaultValue, selected, 0)
	for {
		key, err := s.readMenuKey()
		if err != nil {
			return "", err
		}

		switch key.kind {
		case menuKeyUp:
			selected = (selected - 1 + len(options)) % len(options)
		case menuKeyDown:
			selected = (selected + 1) % len(options)
		case menuKeyDigit:
			if key.index >= 0 && key.index < len(options) {
				selected = key.index
			}
		case menuKeyBackspace:
		case menuKeyChar:
		case menuKeyEnter:
			fmt.Fprintln(s.out)
			return options[selected], nil
		case menuKeyInterrupt:
			return "", errors.New("prompt interrupted")
		default:
			continue
		}

		lines = s.renderMenu(label, options, defaultValue, selected, lines)
	}
}

func (s *Session) selectWithSearchCursor(label string, options []string, defaultValue string) (string, error) {
	if len(options) == 1 {
		return options[0], nil
	}

	restore, err := s.enableRawMode()
	if err != nil {
		return "", errCursorMenuUnavailable
	}
	defer func() {
		_ = restore()
	}()

	query := ""
	filtered := filterSearchOptions(options, query)
	selected := selectionIndex(filtered, defaultValue)
	lines := s.renderSearchMenu(label, query, filtered, defaultValue, selected, 0)
	for {
		key, err := s.readMenuKey()
		if err != nil {
			return "", err
		}

		updated := false
		switch key.kind {
		case menuKeyUp:
			if len(filtered) > 0 {
				selected = (selected - 1 + len(filtered)) % len(filtered)
			}
		case menuKeyDown:
			if len(filtered) > 0 {
				selected = (selected + 1) % len(filtered)
			}
		case menuKeyDigit:
			query += string(rune('1' + key.index))
			updated = true
		case menuKeyBackspace:
			if query != "" {
				query = removeLastRune(query)
				updated = true
			}
		case menuKeyChar:
			query += string(key.ch)
			updated = true
		case menuKeyEnter:
			if len(filtered) == 0 {
				continue
			}
			fmt.Fprintln(s.out)
			return filtered[selected], nil
		case menuKeyInterrupt:
			return "", errors.New("prompt interrupted")
		default:
			continue
		}

		if updated {
			filtered = filterSearchOptions(options, query)
			selected = selectionIndex(filtered, defaultValue)
		}
		lines = s.renderSearchMenu(label, query, filtered, defaultValue, selected, lines)
	}
}

func (s *Session) enableRawMode() (func() error, error) {
	if s.inFile == nil {
		return nil, errors.New("raw mode requires a terminal")
	}

	fd := int(s.inFile.Fd())
	originalState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}

	return func() error {
		return term.Restore(fd, originalState)
	}, nil
}

func (s *Session) readMenuKey() (menuKey, error) {
	first, err := s.in.ReadByte()
	if err != nil {
		return menuKey{}, err
	}

	switch first {
	case '\r', '\n':
		return menuKey{kind: menuKeyEnter}, nil
	case 0x03:
		return menuKey{kind: menuKeyInterrupt}, nil
	case 0x7f, 0x08:
		return menuKey{kind: menuKeyBackspace}, nil
	case 0x1b:
		second, err := s.in.ReadByte()
		if err != nil {
			return menuKey{}, err
		}
		switch second {
		case '[':
			third, err := s.in.ReadByte()
			if err != nil {
				return menuKey{}, err
			}
			switch third {
			case 'A':
				return menuKey{kind: menuKeyUp}, nil
			case 'B':
				return menuKey{kind: menuKeyDown}, nil
			}
		case 'O':
			third, err := s.in.ReadByte()
			if err != nil {
				return menuKey{}, err
			}
			switch third {
			case 'A':
				return menuKey{kind: menuKeyUp}, nil
			case 'B':
				return menuKey{kind: menuKeyDown}, nil
			}
		}
		return menuKey{kind: menuKeyUnknown}, nil
	}

	if first >= '1' && first <= '9' {
		return menuKey{kind: menuKeyDigit, index: int(first - '1')}, nil
	}
	if first >= 0x20 && first <= 0x7e {
		return menuKey{kind: menuKeyChar, ch: rune(first)}, nil
	}

	return menuKey{kind: menuKeyUnknown}, nil
}

func (s *Session) renderMenu(label string, options []string, defaultValue string, selected, previousLines int) int {
	if previousLines > 0 {
		fmt.Fprintf(s.out, "\033[%dA", previousLines)
	}
	writeMenuLine(s.out, s.style(ansiBrightCyan, label))
	start, end := menuWindowBounds(len(options), selected, menuWindowSize)
	if start > 0 {
		writeMenuLine(s.out, s.style(ansiDim, "  ..."))
	}
	for i := start; i < end; i++ {
		option := options[i]
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		marker := ""
		if option == defaultValue {
			marker = s.style(ansiYellow, " (default)")
		}
		text := option
		if i == selected {
			text = s.style(ansiBrightGreen, option)
		}
		writeMenuLine(s.out, fmt.Sprintf("%s%s%s", prefix, text, marker))
	}
	if end < len(options) {
		writeMenuLine(s.out, s.style(ansiDim, "  ..."))
	}
	fmt.Fprint(s.out, "\033[J")
	lines := 1 + (end - start)
	if start > 0 {
		lines++
	}
	if end < len(options) {
		lines++
	}
	return lines
}

func (s *Session) renderSearchMenu(label, query string, options []string, defaultValue string, selected, previousLines int) int {
	if previousLines > 0 {
		fmt.Fprintf(s.out, "\033[%dA", previousLines)
	}
	writeMenuLine(s.out, s.style(ansiBrightCyan, label))
	if query == "" {
		writeMenuLine(s.out, s.style(ansiDim, "Type to filter, Backspace to edit, Enter to select"))
	} else {
		writeMenuLine(s.out, fmt.Sprintf("%s %s", s.style(ansiYellow, "Search:"), query))
	}
	if len(options) == 0 {
		writeMenuLine(s.out, s.style(ansiDim, "  No matches"))
		fmt.Fprint(s.out, "\033[J")
		return 3
	}
	start, end := menuWindowBounds(len(options), selected, menuWindowSize)
	if start > 0 {
		writeMenuLine(s.out, s.style(ansiDim, "  ..."))
	}
	for i := start; i < end; i++ {
		option := options[i]
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		marker := ""
		if option == defaultValue {
			marker = s.style(ansiYellow, " (default)")
		}
		text := option
		if i == selected {
			text = s.style(ansiBrightGreen, option)
		}
		writeMenuLine(s.out, fmt.Sprintf("%s%s%s", prefix, text, marker))
	}
	if end < len(options) {
		writeMenuLine(s.out, s.style(ansiDim, "  ..."))
	}
	fmt.Fprint(s.out, "\033[J")
	lines := 2 + (end - start)
	if start > 0 {
		lines++
	}
	if end < len(options) {
		lines++
	}
	return lines
}

func filterSearchOptions(options []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]string(nil), options...)
	}
	matches := make([]string, 0, len(options))
	for _, option := range options {
		if matchesSearchQuery(option, query) {
			matches = append(matches, option)
		}
	}
	return matches
}

func matchesSearchQuery(option, query string) bool {
	option = strings.ToLower(strings.TrimSpace(option))
	if query == "" {
		return true
	}
	if !strings.HasPrefix(option, query) {
		return false
	}
	if len(option) == len(query) {
		return true
	}
	next := option[len(query)]
	return next == '.' || next == '-' || (next >= '0' && next <= '9')
}

func selectionIndex(options []string, defaultValue string) int {
	for i, option := range options {
		if option == defaultValue {
			return i
		}
	}
	return 0
}

func removeLastRune(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func menuWindowBounds(total, selected, size int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if size <= 0 || total <= size {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - size/2
	if start < 0 {
		start = 0
	}
	if start > total-size {
		start = total - size
	}
	return start, start + size
}

func writeMenuLine(out io.Writer, text string) {
	fmt.Fprintf(out, "\r\033[2K%s\n", text)
}

func isTerminalFile(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
