//go:build webembed

package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distribution embed.FS

func Assets() (fs.FS, bool) {
	assets, err := fs.Sub(distribution, "dist")
	if err != nil {
		panic(err)
	}
	return assets, true
}
