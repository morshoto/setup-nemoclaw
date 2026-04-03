package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

var errCursorMenuUnavailable = errors.New("cursor menu unavailable")

const (
	ansiReset       = "\033[0m"
	ansiBold        = "\033[1m"
	ansiDim         = "\033[2m"
	ansiGreen       = "\033[32m"
	ansiYellow      = "\033[33m"
	ansiCyan        = "\033[36m"
	ansiBrightGreen = "\033[1;32m"
	ansiBrightCyan  = "\033[1;36m"
)

type Session struct {
	in          *bufio.Reader
	out         io.Writer
	inFile      *os.File
	outFile     *os.File
	Interactive bool
}

func NewSession(in io.Reader, out io.Writer) *Session {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	var inFile *os.File
	if f, ok := in.(*os.File); ok {
		inFile = f
	}
	var outFile *os.File
	if f, ok := out.(*os.File); ok {
		outFile = f
	}
	return &Session{
		in:          bufio.NewReader(in),
		out:         out,
		inFile:      inFile,
		outFile:     outFile,
		Interactive: true,
	}
}

func (s *Session) Select(label string, options []string, defaultValue string) (string, error) {
	if !s.Interactive {
		return fallbackValue(defaultValue, options)
	}
	if len(options) == 0 {
		return "", fmt.Errorf("%s: no options available", label)
	}

	if s.canUseCursorMenu() {
		selected, err := s.selectWithCursor(label, options, defaultValue)
		switch {
		case err == nil:
			return selected, nil
		case errors.Is(err, errCursorMenuUnavailable):
		default:
			return "", err
		}
	}
	return s.selectWithLine(label, options, defaultValue)
}

func (s *Session) selectWithLine(label string, options []string, defaultValue string) (string, error) {
	fmt.Fprintln(s.out, s.style(ansiBrightCyan, label))
	for i, option := range options {
		marker := ""
		if option == defaultValue {
			marker = s.style(ansiYellow, " (default)")
		}
		line := fmt.Sprintf("  %d) %s%s", i+1, option, marker)
		fmt.Fprintln(s.out, line)
	}
	input, err := s.readLine()
	if err != nil {
		return "", err
	}
	if input == "" {
		return fallbackValue(defaultValue, options)
	}
	if idx, err := strconv.Atoi(input); err == nil {
		if idx >= 1 && idx <= len(options) {
			return options[idx-1], nil
		}
	}
	for _, option := range options {
		if strings.EqualFold(input, option) {
			return option, nil
		}
	}
	return "", fmt.Errorf("%s: invalid selection %q", label, input)
}

func (s *Session) Confirm(label string, defaultValue bool) (bool, error) {
	if !s.Interactive {
		return defaultValue, nil
	}

	indicator := "y/N"
	if defaultValue {
		indicator = "Y/n"
	}
	fmt.Fprintf(s.out, "%s [%s]: ", s.style(ansiBrightCyan, label), s.style(ansiYellow, indicator))
	input, err := s.readLine()
	if err != nil {
		return false, err
	}
	if input == "" {
		return defaultValue, nil
	}
	switch strings.ToLower(input) {
	case "y", "yes", "true", "1":
		return true, nil
	case "n", "no", "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("%s: invalid confirmation %q", label, input)
	}
}

func (s *Session) Text(label, defaultValue string) (string, error) {
	if !s.Interactive {
		return defaultValue, nil
	}

	if defaultValue != "" {
		fmt.Fprintf(s.out, "%s [%s]: ", s.style(ansiBrightCyan, label), s.style(ansiYellow, defaultValue))
	} else {
		fmt.Fprintf(s.out, "%s: ", s.style(ansiBrightCyan, label))
	}
	input, err := s.readLine()
	if err != nil {
		return "", err
	}
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

func (s *Session) Secret(label, defaultValue string) (string, error) {
	if !s.Interactive || s.inFile == nil || !isTerminalFile(s.inFile) {
		return s.Text(label, defaultValue)
	}

	if defaultValue != "" {
		fmt.Fprintf(s.out, "%s [%s]: ", s.style(ansiBrightCyan, label), s.style(ansiYellow, "configured"))
	} else {
		fmt.Fprintf(s.out, "%s: ", s.style(ansiBrightCyan, label))
	}
	value, err := term.ReadPassword(int(s.inFile.Fd()))
	fmt.Fprintln(s.out)
	if err != nil {
		return "", err
	}
	if trimmed := strings.TrimSpace(string(value)); trimmed != "" {
		return trimmed, nil
	}
	return defaultValue, nil
}

func (s *Session) Int(label string, defaultValue int) (int, error) {
	if !s.Interactive {
		return defaultValue, nil
	}

	if defaultValue != 0 {
		fmt.Fprintf(s.out, "%s [%d]: ", s.style(ansiBrightCyan, label), defaultValue)
	} else {
		fmt.Fprintf(s.out, "%s: ", s.style(ansiBrightCyan, label))
	}
	input, err := s.readLine()
	if err != nil {
		return 0, err
	}
	if input == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("%s: must be an integer", label)
	}
	return value, nil
}

func (s *Session) readLine() (string, error) {
	line, err := s.in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func fallbackValue(defaultValue string, options []string) (string, error) {
	if defaultValue != "" {
		return defaultValue, nil
	}
	if len(options) == 1 {
		return options[0], nil
	}
	return "", fmt.Errorf("interactive input required")
}

func (s *Session) supportsColor() bool {
	return s.outFile != nil && isTerminalFile(s.outFile)
}

func (s *Session) style(code, text string) string {
	if !s.supportsColor() || text == "" {
		return text
	}
	return code + text + ansiReset
}
