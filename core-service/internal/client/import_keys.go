package client

import (
	"path"
	"strings"
)

// IsImportKey reports whether the key is under the imports/ prefix (source and
// separated stems of a bring-your-own-track import).
func IsImportKey(key string) bool { return strings.HasPrefix(key, "imports/") }

// IsImportVocalKey reports whether the key is the separated original vocal of
// an import. It must never be mixed, only used as a guide.
func IsImportVocalKey(key string) bool {
	return IsImportKey(key) && path.Base(key) == "vocal.mp3"
}

// IsImportMasterKey reports whether the key is a master built from import
// stems; such masters are not publishable.
func IsImportMasterKey(key string) bool {
	return strings.HasPrefix(key, "masters/") && strings.HasPrefix(path.Base(key), "imp-")
}

// IsImportTainted reports whether the key is, or was derived from, a
// bring-your-own-track import: anything under imports/, or a master/export
// whose file name carries the imp- prefix. Tainted keys are never publishable.
func IsImportTainted(key string) bool {
	if IsImportKey(key) {
		return true
	}
	first, _, _ := strings.Cut(key, "/")
	return (first == "masters" || first == "exports") && strings.HasPrefix(path.Base(key), "imp-")
}
