//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package prompt

func (s *Session) canUseCursorMenu() bool {
	return false
}

func (s *Session) selectWithCursor(label string, options []string, defaultValue string) (string, error) {
	return "", errCursorMenuUnavailable
}
