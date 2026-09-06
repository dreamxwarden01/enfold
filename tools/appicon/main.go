// Command appicon renders build/appicon.png from internal/brand, so the
// application icon is code, not a binary asset. `wails3 generate icons`
// turns it into build/windows/icon.ico.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dreamxwarden01/enfold/internal/brand"
)

func main() {
	out := flag.String("o", "build/appicon.png", "output file")
	size := flag.Int("size", 1024, "pixel size")
	flag.Parse()
	if err := os.WriteFile(*out, brand.PNG(brand.Mark(*size, false)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
