package prompt

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Session struct {
	in          *bufio.Reader
	out         io.Writer
	Interactive bool
}

func NewSession(in io.Reader, out io.Writer) *Session {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	return &Session{
		in:          bufio.NewReader(in),
		out:         out,
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

	fmt.Fprintf(s.out, "%s\n", label)
	for i, option := range options {
		marker := ""
		if option == defaultValue {
			marker = " (default)"
		}
		fmt.Fprintf(s.out, "  %d) %s%s\n", i+1, option, marker)
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
	fmt.Fprintf(s.out, "%s [%s]: ", label, indicator)
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
		fmt.Fprintf(s.out, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(s.out, "%s: ", label)
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

func (s *Session) Int(label string, defaultValue int) (int, error) {
	if !s.Interactive {
		return defaultValue, nil
	}

	if defaultValue != 0 {
		fmt.Fprintf(s.out, "%s [%d]: ", label, defaultValue)
	} else {
		fmt.Fprintf(s.out, "%s: ", label)
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
