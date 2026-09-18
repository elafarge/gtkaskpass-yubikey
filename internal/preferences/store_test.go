package preferences

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistenceAndUnsafeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "preferences", "devices.json")
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	s := New(p)
	if e := s.Save(fp, "usb-serial-v1:abc"); e != nil {
		t.Fatal(e)
	}
	if New(p).Preferred(fp) != "usb-serial-v1:abc" {
		t.Fatal("mapping lost")
	}
	if e := os.WriteFile(p, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if New(p).Save(fp, "replacement") == nil {
		t.Fatal("corrupt preferences overwritten")
	}
	link := filepath.Join(t.TempDir(), "devices.json")
	if e := os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if New(link).Save(fp, "replacement") == nil {
		t.Fatal("symlink accepted")
	}
}
