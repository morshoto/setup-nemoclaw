//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package prompt

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

const (
	menuKeyUnknown = iota
	menuKeyUp
	menuKeyDown
	menuKeyEnter
	menuKeyInterrupt
	menuKeyDigit
)

type menuKey struct {
	kind  int
	index int
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

	return menuKey{kind: menuKeyUnknown}, nil
}

func (s *Session) renderMenu(label string, options []string, defaultValue string, selected, previousLines int) int {
	if previousLines > 0 {
		fmt.Fprintf(s.out, "\033[%dA", previousLines)
	}
	writeMenuLine(s.out, s.style(ansiBrightCyan, label))
	for i, option := range options {
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
	return len(options) + 1
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
