// Copyright 2026 The git-gen Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build unix

package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckDirOwner runs CheckDir with the real owner lookup: the files of a temporary directory belong to
// the user running the test, and any other user id is refused.
func TestCheckDirOwner(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{".git/config": "[core]\n\tbare = false\n"})
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if uid, ok := fileOwner(fi); !ok || uid != os.Getuid() {
		t.Errorf("fileOwner() = %d, %t, want %d, true", uid, ok, os.Getuid())
	}

	tests := map[string]struct {
		check   func() error
		wantErr string
	}{
		"success: the current user owns .git": {
			check: func() error { return CheckDir(dir) },
		},
		"error: another user id": {
			check:   func() error { return checkDir(dir, os.Getuid()+1, fileOwner) },
			wantErr: filepath.Join(dir, ".git") + " is owned by uid ",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tt.check()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckDir() error = %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Fatalf("checkDir() error = %v, want one starting with %q", err, tt.wantErr)
			}
		})
	}
}
