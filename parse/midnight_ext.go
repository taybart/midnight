package parse

import "strings"

// Extensions are the file extensions of Midnight source files. The first is
// canonical; both are recognized everywhere (loading, require, the CLI).
var Extensions = []string{".mn", ".midnight"}

// IsMidnightPath reports whether path has a Midnight extension.
func IsMidnightPath(path string) bool {
	for _, ext := range Extensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// TrimMidnightExt removes a Midnight extension from path, if it has one.
func TrimMidnightExt(path string) string {
	for _, ext := range Extensions {
		if strings.HasSuffix(path, ext) {
			return strings.TrimSuffix(path, ext)
		}
	}
	return path
}
