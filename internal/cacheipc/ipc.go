// SPDX-License-Identifier: Apache-2.0
package cacheipc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/cache"
	"github.com/elafarge/ssh-askpass-fido/internal/lifecycle"
	"github.com/elafarge/ssh-askpass-fido/internal/trace"
	"golang.org/x/sys/unix"
)

const maxFrame = 16 * 1024
const Timeout = 2 * time.Second

// Transport cleanup cannot recover a failed connection or change a response
// already supplied to SSH. In particular, closing an already closed socket is OK.
func closeQuietly(c io.Closer) { _ = c.Close() }

type Request struct {
	Args    []string          `json:"args,omitempty"`
	Hint    string            `json:"hint,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	NoCache bool              `json:"no_cache,omitempty"`
	Trace   bool              `json:"trace,omitempty"`
	Version int               `json:"version"`
	Op      string            `json:"op"`
	Key     cache.Key         `json:"key"`
	Token   uint64            `json:"token,omitempty"`
	Secret  []byte            `json:"secret,omitempty"`
}
type Response struct {
	Event          string        `json:"event,omitempty"`
	Code           int           `json:"code,omitempty"`
	Notify         bool          `json:"notify,omitempty"`
	SecretResponse bool          `json:"secret_response,omitempty"`
	Version        int           `json:"version"`
	Reason         string        `json:"reason,omitempty"`
	Error          string        `json:"error,omitempty"`
	Token          uint64        `json:"token,omitempty"`
	TTL            time.Duration `json:"ttl,omitempty"`
	Secret         []byte        `json:"secret,omitempty"`
}

// Framed messages are shared by the request stream and private UI socketpair.
func ReadFrame(r io.Reader, v any) error  { return readFrame(r, v) }
func WriteFrame(w io.Writer, v any) error { return writeFrame(w, v) }

func Dial(ctx context.Context) (*net.UnixConn, error) {
	p, err := SocketPath(false)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Mode&0777 != 0600 || st.Uid != uint32(os.Getuid()) {
		return nil, errors.New("invalid cache socket ownership or mode")
	}
	d := net.Dialer{Timeout: Timeout}
	c, err := d.DialContext(ctx, "unix", p)
	if err != nil {
		return nil, err
	}
	u := c.(*net.UnixConn)
	if _, err := peer(u); err != nil {
		closeQuietly(u)
		return nil, err
	}
	return u, nil
}

type RequestHandler func(context.Context, *net.UnixConn, Request, string)

func ownedDir(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	var u unix.Stat_t
	if err := unix.Lstat(path, &u); err != nil {
		return err
	}
	if !st.IsDir() || u.Uid != uint32(os.Getuid()) || st.Mode().Perm() != 0700 {
		return errors.New("runtime directory must be owned by this user with mode 0700")
	}
	return nil
}

func SocketPath(create bool) (string, error) {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("XDG_RUNTIME_DIR must be an absolute private directory")
	}
	if err := ownedDir(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, "ssh-askpass-fido")
	if create {
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	if err := ownedDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache.sock"), nil
}

func peer(c *net.UnixConn) (*unix.Ucred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	var cred *unix.Ucred
	var inner error
	if err = raw.Control(func(fd uintptr) { cred, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return nil, err
	}
	if inner != nil {
		return nil, inner
	}
	if cred.Uid != uint32(os.Getuid()) {
		return nil, errors.New("cache peer has different UID")
	}
	return cred, nil
}

func readFrame(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxFrame {
		return errors.New("invalid frame size")
	}
	b := make([]byte, n)
	defer clear(b)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errors.New("invalid frame JSON")
	}
	return nil
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	defer clear(b)
	if len(b) > maxFrame {
		return errors.New("frame too large")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if n, err := w.Write(h[:]); err != nil {
		return err
	} else if n != len(h) {
		return io.ErrShortWrite
	}
	if n, err := w.Write(b); err != nil {
		return err
	} else if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

func Call(ctx context.Context, req Request) (Response, error) {
	p, err := SocketPath(false)
	if err != nil {
		return Response{}, err
	}
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		return Response{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Mode&0777 != 0600 || st.Uid != uint32(os.Getuid()) {
		return Response{}, errors.New("invalid cache socket ownership or mode")
	}
	d := net.Dialer{Timeout: Timeout}
	conn, err := d.DialContext(ctx, "unix", p)
	if err != nil {
		return Response{}, err
	}
	defer closeQuietly(conn)
	c := conn.(*net.UnixConn)
	if _, err := peer(c); err != nil {
		return Response{}, err
	}
	deadline := time.Now().Add(Timeout)
	if v, ok := ctx.Deadline(); ok && v.Before(deadline) {
		deadline = v
	}
	if err := c.SetDeadline(deadline); err != nil {
		return Response{}, err
	}
	stop := context.AfterFunc(ctx, func() { closeQuietly(c) })
	defer stop()
	req.Version = 1
	if err := writeFrame(c, req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := readFrame(c, &resp); err != nil {
		clear(resp.Secret)
		return Response{}, err
	}
	if resp.Version != 1 || resp.Error != "" {
		clear(resp.Secret)
		return Response{}, errors.New("cache request rejected")
	}
	return resp, nil
}

// Listen binds the session service's private endpoint exclusively. Systemd owns
// its runtime directory, but no longer owns or activates a separate socket unit.
func Listen() (*net.UnixListener, error) {
	p, err := SocketPath(true)
	if err != nil {
		return nil, err
	}
	if os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) && os.Getenv("LISTEN_FDS") != "" {
		return nil, errors.New("socket activation is no longer supported; disable the .socket unit and start the graphical-session service")
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0600); err != nil {
		closeQuietly(l)
		return nil, err
	}
	return l, nil
}

func caller(cred *unix.Ucred) (string, error) {
	helper, err := lifecycle.ReadProcess(int(cred.Pid))
	if err != nil {
		return "", err
	}
	p, err := lifecycle.ReadProcess(helper.Parent)
	if err != nil || p.PID <= 1 || !p.Alive() {
		return "", errors.New("caller unavailable")
	}
	return p.ID(), nil
}

func alive(id string) bool {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		return false
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	return (lifecycle.Process{PID: pid, Start: parts[1]}).Alive()
}

func handle(ctx context.Context, c *net.UnixConn, s *cache.Store, log *trace.Logger, handler RequestHandler) {
	defer closeQuietly(c)
	if err := c.SetDeadline(time.Now().Add(Timeout)); err != nil {
		return
	}
	cred, err := peer(c)
	if err != nil {
		return
	}
	var req Request
	if err := readFrame(c, &req); err != nil {
		clear(req.Secret)
		return
	}
	defer clear(req.Secret)
	if req.Version == 2 && req.Op == "ask" && handler != nil {
		who, err := caller(cred)
		if err != nil {
			return
		}
		if err := c.SetDeadline(time.Time{}); err != nil {
			return
		}
		handler(ctx, c, req, who)
		return
	}
	resp := Response{Version: 1}
	defer func() {
		defer clear(resp.Secret)
		if err := writeFrame(c, resp); err != nil {
			log.Event("ipc-write-failed")
		}
	}()
	if req.Version != 1 {
		resp.Error = "version"
		return
	}
	switch req.Op {
	case "begin", "commit":
		who, err := caller(cred)
		if err != nil {
			resp.Error = "caller"
			return
		}
		if !req.Key.AgentPIN() && !filepath.IsAbs(req.Key.Path) {
			resp.Error = "path"
			return
		}
		id, err := cache.Resolve(req.Key)
		if err != nil {
			resp.Error = "identity"
			return
		}
		if req.Op == "begin" {
			r := s.Begin(id, who)
			resp.Reason, resp.Secret, resp.Token, resp.TTL = r.Reason, r.Secret, r.Token, r.TTL
		} else if s.Commit(req.Token, id, who, req.Secret) {
			resp.Reason = "stored"
		} else {
			resp.Reason = "stale"
		}
	case "forget":
		if !req.Key.Valid() || (!req.Key.AgentPIN() && !filepath.IsAbs(req.Key.Path)) {
			resp.Error = "key"
			return
		}
		k := req.Key
		if !k.AgentPIN() {
			k.Path = filepath.Clean(k.Path)
		}
		s.Forget(&k)
		if id, err := cache.Resolve(k); err == nil {
			s.Forget(&id.Key)
		}
		resp.Reason = "forgotten"
	case "forget-all":
		s.Forget(nil)
		resp.Reason = "forgotten"
	default:
		resp.Error = "operation"
	}
	log.Event("cache", "op", req.Op, "peer", cred.Pid, "token", resp.Token, "reason", resp.Reason)
}

func Serve(ctx context.Context, l *net.UnixListener, s *cache.Store, log *trace.Logger, handlers ...RequestHandler) error {
	var handler RequestHandler
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	defer s.Forget(nil)
	stop := context.AfterFunc(ctx, func() { closeQuietly(l) })
	defer stop()
	defer closeQuietly(l)
	var wg sync.WaitGroup
	defer wg.Wait()
	sem := make(chan struct{}, 64)
	wg.Add(1)
	sweepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				s.Sweep(alive)
			}
		}
	}()
	log.Event("daemon-start")
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		select {
		case sem <- struct{}{}:
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-sem }(); handle(ctx, c, s, log, handler) }()
		default:
			closeQuietly(c)
		}
	}
}
