//go:build !windows

package app

// strayVerdict judges a failed removal of a test's folder (tempdir_test.go).
// Nothing else writes into a test's folder here, so no failure is waited
// out: it is left to testing's own removal, which reports it.
func strayVerdict(root string, err error) (wait bool, ours []string) {
	return false, nil
}
