// SPDX-License-Identifier: Apache-2.0
package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/elafarge/ssh-askpass-fido/internal/askpass"
)

type document struct {
	Version int               `json:"version"`
	Devices map[string]string `json:"devices"`
}
type Store struct {
	mu   sync.Mutex
	path string
	data document
	err  error
}

func New(path string) *Store {
	s := &Store{path: path, data: document{Version: 1, Devices: map[string]string{}}}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil {
		s.err = err
		return s
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		s.err = err
		return s
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		s.err = fmt.Errorf("unsafe device preference file")
		return s
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 {
		s.err = fmt.Errorf("cannot read bounded preference file")
		return s
	}
	var d document
	if json.Unmarshal(b, &d) != nil || d.Version != 1 || d.Devices == nil || len(d.Devices) > 256 {
		s.err = fmt.Errorf("invalid device preference document")
		return s
	}
	for k, v := range d.Devices {
		if !askpass.ValidFingerprint(k) || len(v) > 256 {
			s.err = fmt.Errorf("invalid device preference entry")
			return s
		}
	}
	s.data = d
	return s
}
func (s *Store) Preferred(fp string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Devices[fp]
}
func (s *Store) Save(fp, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if !askpass.ValidFingerprint(fp) || id == "" {
		return nil
	}
	if len(s.data.Devices) >= 256 && s.data.Devices[fp] == "" {
		return fmt.Errorf("device preference limit reached")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("unsafe device preference directory")
	}
	// Refuse symlink replacement even though rename would replace rather than follow it.
	if st, err := os.Lstat(s.path); err == nil && (!st.Mode().IsRegular() || st.Mode().Perm() != 0600) {
		return fmt.Errorf("unsafe preference target")
	}
	d := document{Version: 1, Devices: map[string]string{}}
	for k, v := range s.data.Devices {
		d.Devices[k] = v
	}
	d.Devices[fp] = id
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".devices-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()); _ = f.Close() }()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.data = d
	return nil
}
