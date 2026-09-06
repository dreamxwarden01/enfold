//go:build !windows

// Enfold's shell is a Windows program (docs/SCOPE.md); this stub keeps the
// module buildable elsewhere so that the libraries can be vetted and tested
// on any platform.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "enfold: the application runs on Windows only")
	os.Exit(2)
}
