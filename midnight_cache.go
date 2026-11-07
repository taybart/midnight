package lua

// midnight: compiled files are cached (by path, modification time, size, and
// the midnight options they were parsed w/), so loading the same file in many
// states (ex. `require` in a server that makes a state per request) compiles
// it once. Compiled code (FunctionProto) is immutable, so it's safe to share
// between states; each load still gets its own function and environment. A
// changed file has a new modification time / size, so it's compiled again.

import (
	"sync"

	"github.com/taybart/midnight/parse"
)

// CacheCompiledFiles turns the compiled file cache on (the default) or off.
var CacheCompiledFiles = true

// Limit on cached files, after which the cache starts over.
const compiledFilesMax = 1024

type compiledFileKey struct {
	path     string
	modTime  int64
	size     int64
	midnight parse.MidnightOptions
}

var compiledFiles struct {
	sync.Mutex
	protos map[compiledFileKey]*FunctionProto
}

// compiledFile returns the cache key for `path`, and its compiled code if
// it's cached (ok is false if the file can't be cached, ex. it doesn't
// exist).
func (ls *LState) compiledFile(path string) (key compiledFileKey, proto *FunctionProto, ok bool) {
	if !CacheCompiledFiles || path == "" {
		return key, nil, false
	}
	info, err := statFile(path)
	if err != nil || !info.Mode().IsRegular() {
		return key, nil, false
	}
	key = compiledFileKey{path: path, modTime: info.ModTime().UnixNano(), size: info.Size()}
	if isMidnightPath(path) {
		key.midnight = ls.Options.Midnight
	}

	compiledFiles.Lock()
	defer compiledFiles.Unlock()
	return key, compiledFiles.protos[key], true
}

func storeCompiledFile(key compiledFileKey, proto *FunctionProto) {
	compiledFiles.Lock()
	defer compiledFiles.Unlock()
	if compiledFiles.protos == nil || len(compiledFiles.protos) >= compiledFilesMax {
		compiledFiles.protos = map[compiledFileKey]*FunctionProto{}
	}
	compiledFiles.protos[key] = proto
}
