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
	"testing"
)

// TestCheckDirOwner runs CheckDir with the real owner lookup: the files of a temporary directory belong to
// the user running the test. TestCheckDir covers the refusal of another user id with an injected owner.
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
	if err := CheckDir(dir); err != nil {
		t.Errorf("CheckDir() error = %v", err)
	}
}
