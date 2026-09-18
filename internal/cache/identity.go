// SPDX-License-Identifier: Apache-2.0
package cache

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/elafarge/gtkaskpass-yubikey/internal/askpass"
	"golang.org/x/sys/unix"
)

type Key struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type Identity struct {
	Key
	Stamp string
}

func (k Key) AgentPIN() bool {
	return k.Kind == "pin" && k.Path == "" && askpass.ValidFingerprint(k.Fingerprint)
}

func (k Key) Valid() bool {
	return k.AgentPIN() || (k.Fingerprint == "" && k.Path != "" && (k.Kind == "passphrase" || k.Kind == "pin"))
}

// Resolve uses metadata only; it never opens or reads private-key contents.
func Resolve(k Key) (Identity, error) {
	if !k.Valid() {
		return Identity{}, fmt.Errorf("invalid cache key")
	}
	if k.AgentPIN() {
		return Identity{Key: k}, nil
	}
	p, err := filepath.Abs(k.Path)
	if err != nil {
		return Identity{}, err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return Identity{}, err
	}
	var st unix.Stat_t
	if err := unix.Stat(p, &st); err != nil {
		return Identity{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) {
		return Identity{}, fmt.Errorf("key must be a regular file owned by this user")
	}
	k.Path = p
	return Identity{k, fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d", st.Dev, st.Ino, st.Size, st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Sec, st.Ctim.Nsec)}, nil
}
