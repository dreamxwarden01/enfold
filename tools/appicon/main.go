// Command appicon draws the application's icon from internal/brand and
// writes it where the build reads it: build/windows/icon.ico, every entry
// drawn for its own size (brand.IconSizes), which the executable's
// resources, the installer and the .efd class's DefaultIcon all take; and
// build/appicon.png, the icon large. Neither file is kept in the
// repository: build/Taskfile.yml's generate:icons runs this before every
// build, so the icon is code, as the drawings in the window are.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dreamxwarden01/enfold/internal/brand"
)

func main() {
	ico := flag.String("ico", "build/windows/icon.ico", "the Windows icon to write")
	png := flag.String("png", "build/appicon.png", "the large PNG to write")
	size := flag.Int("size", 1024, "the PNG's size in pixels")
	flag.Parse()
	for _, out := range []struct {
		path string
		data []byte
	}{
		{*ico, brand.ICO()},
		{*png, brand.PNG(brand.Icon(*size))},
	} {
		if err := os.WriteFile(out.path, out.data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
