//go:build sqlite

package sqlstore

// Driver SQLite murni-Go (tanpa CGO), cross-compile ke linux/arm. Pasang dengan:
//
//	go get modernc.org/sqlite@latest
import _ "modernc.org/sqlite"
