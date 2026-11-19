// Why: std.simd.kernel is std-only and must not be imported by the browse file.
// What: the test of browsing a std.simd.kernel file.
// How: opens one and checks that no E0202 is published, while std.simd stays
// public.

package lsp

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLSPSimdKernelBrowse(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "std")
	for _, tc := range []struct {
		file, module string
		pulls        []string
	}{
		{"simd/kernel/aes.mk", "", []string{"std.crypto", "std.hash"}},
		{"simd/simd.mk", "std.simd", nil},
		{"task/scope.mk", "", stdPulls},
		{"time/time.mk", "std.time", nil},
	} {
		path := filepath.Join(tree, filepath.FromSlash(tc.file))
		if got := stdModuleOf(tree, path); got != tc.module {
			t.Errorf("stdModuleOf(%s) = %q, want %q", tc.file, got, tc.module)
		}
		if tc.module == "" && !slices.Equal(stdPullsOf(tree, path), tc.pulls) {
			t.Errorf("stdPullsOf(%s) = %v, want %v", tc.file, stdPullsOf(tree, path), tc.pulls)
		}
	}
}
