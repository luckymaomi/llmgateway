//go:build !webembed

package webassets

import "io/fs"

func Assets() (fs.FS, bool) {
	return nil, false
}
