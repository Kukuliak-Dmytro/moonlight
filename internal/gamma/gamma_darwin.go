//go:build darwin

package gamma

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// On macOS a gamma table set through CoreGraphics is undone as soon as the process
// that set it exits, and the system also resets it on sleep/wake and display changes.
// A short-lived `osascript` therefore cannot hold a tint.
//
// Instead one long-lived "holder" process (this binary, re-executed as `gamma-holder`)
// owns a persistent osascript child that applies ratios read from its stdin. Clients
// (the daemon, `moonlight set`, ...) talk to the holder over a unix socket.
//
//   - The holder re-applies the current tint every refreshInterval, so a reset by the
//     OS is undone within seconds, with no help from the daemon.
//   - Every SET carries a lease. When it lapses without a refresh (SET) or extension
//     (TOUCH) the holder closes the child's stdin, the child exits, and macOS restores
//     the original gamma: a dead daemon never leaves the screen tinted for long.
//   - A file lock guarantees at most one holder, however many clients start one.

const (
	dialTimeout   = time.Second
	startTimeout  = 3 * time.Second
	requestWindow = 8 * time.Second
	childReply    = 5 * time.Second
	maxLogBytes   = 1 << 20
)

// Variables so tests can run the real holder logic against a fake child and fast clocks.
var (
	// refreshInterval is how often the current tint is re-asserted.
	refreshInterval = 2 * time.Second
	// initialGrace is how long a fresh holder waits for its first SET before giving up.
	initialGrace = 30 * time.Second
	// childCommand builds the persistent helper process.
	childCommand = func() *exec.Cmd {
		return exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", helperScript)
	}
)

// helperScript runs inside the persistent osascript child: one "r g b" line in,
// "ok" or "err ..." out. EOF on stdin restores the system gamma and exits.
const helperScript = `
ObjC.import('AppKit'); ObjC.import('CoreGraphics'); ObjC.import('Foundation');
var inh = $.NSFileHandle.fileHandleWithStandardInput;
var outh = $.NSFileHandle.fileHandleWithStandardOutput;
function say(s) { outh.writeData($(s + "\n").dataUsingEncoding($.NSUTF8StringEncoding)); }
function apply(r, g, b) {
  var screens = $.NSScreen.screens, n = screens.count;
  if (n === 0) { $.CGSetDisplayTransferByFormula($.CGMainDisplayID(), 0, r, 1, 0, g, 1, 0, b, 1); return; }
  for (var i = 0; i < n; i++) {
    var id = screens.objectAtIndex(i).deviceDescription.objectForKey($('NSScreenNumber')).unsignedIntValue;
    $.CGSetDisplayTransferByFormula(id, 0, r, 1, 0, g, 1, 0, b, 1);
  }
}
var buf = "";
for (;;) {
  var data = inh.availableData;
  if (Number(data.length) === 0) break;
  buf += ObjC.unwrap($.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding));
  var idx;
  while ((idx = buf.indexOf("\n")) >= 0) {
    var p = buf.slice(0, idx).split(" "); buf = buf.slice(idx + 1);
    try { apply(parseFloat(p[0]), parseFloat(p[1]), parseFloat(p[2])); say("ok"); }
    catch (e) { say("err " + e); }
  }
}
$.CGDisplayRestoreColorSyncSettings();
`

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, "moonlight")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func socketPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gamma.sock"), nil
}

// Apply sets the color-temperature gamma on ALL active displays and holds it for the
// current Lease, starting the holder process if it is not running.
func Apply(ratios Ratios) error {
	r := ClampRatios(ratios)
	path, err := socketPath()
	if err != nil {
		return err
	}
	conn, err := dialOrStart(path)
	if err != nil {
		return err
	}
	defer conn.Close()
	return roundTrip(conn, formatSet(r, Lease()))
}

// Keepalive extends the lease of the tint that is currently held, without changing it
// and without ever shortening a longer lease. It does nothing (and reports no error) when
// no holder is running, since there is then nothing to keep.
func Keepalive() error {
	path, err := socketPath()
	if err != nil {
		return nil
	}
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return nil
	}
	defer conn.Close()
	return roundTrip(conn, formatTouch(Lease()))
}

// EnsureCurrent replaces a running holder that speaks an older protocol (for example one
// left over from before an upgrade) so the new behaviour actually takes effect. A current
// holder, or none at all, is left alone. The tint is re-applied by the caller right after.
func EnsureCurrent() error {
	path, err := socketPath()
	if err != nil {
		return nil
	}
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return nil
	}
	_ = conn.SetDeadline(time.Now().Add(requestWindow))
	_, werr := fmt.Fprintln(conn, "PING")
	reply, rerr := bufio.NewReader(conn).ReadString('\n')
	conn.Close()
	if werr == nil && rerr == nil && strings.TrimSpace(reply) == "OK "+protocolVersion {
		return nil
	}

	// Old (or unresponsive) holder: ask it to release and quit, then wait for it to go.
	if c, err := net.DialTimeout("unix", path, dialTimeout); err == nil {
		_ = roundTrip(c, "RESET")
		c.Close()
	}
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err != nil {
			return nil
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("old gamma holder did not exit")
}

// Reset releases any held gamma and restores the system tables on all displays.
func Reset() error {
	if path, err := socketPath(); err == nil {
		if conn, err := net.DialTimeout("unix", path, dialTimeout); err == nil {
			_ = roundTrip(conn, "RESET")
			conn.Close()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := `ObjC.import('CoreGraphics'); $.CGDisplayRestoreColorSyncSettings();`
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS gamma reset failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func roundTrip(conn net.Conn, line string) error {
	_ = conn.SetDeadline(time.Now().Add(requestWindow))
	if _, err := fmt.Fprintln(conn, line); err != nil {
		return fmt.Errorf("gamma holder write: %w", err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("gamma holder reply: %w", err)
	}
	if reply = strings.TrimSpace(reply); reply != "OK" {
		return fmt.Errorf("macOS gamma apply failed: %s", strings.TrimPrefix(reply, "ERR "))
	}
	return nil
}

// dialOrStart connects to the holder, launching a detached one first if needed.
func dialOrStart(path string) (net.Conn, error) {
	if c, err := net.DialTimeout("unix", path, dialTimeout); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate moonlight binary: %w", err)
	}
	cmd := exec.Command(exe, "gamma-holder")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive our exit, ignore our terminal
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gamma holder: %w", err)
	}
	go func() { _ = cmd.Wait() }() // reap it if it exits while we are still alive

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", path, dialTimeout); err == nil {
			return c, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, errors.New("gamma holder did not start in time")
}

// holder owns the persistent osascript child and the current tint.
type holder struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	done  chan struct{} // closed when the child exits

	last     *Ratios   // the tint to keep asserted; nil until the first SET
	deadline time.Time // when the lease lapses
	failing  bool      // the last refresh failed (log transitions only)
	log      *log.Logger
}

func (h *holder) alive() bool {
	if h.cmd == nil {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

func (h *holder) start() error {
	cmd := childCommand()
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	h.cmd, h.stdin, h.out, h.done = cmd, in, bufio.NewReader(out), done
	return nil
}

// stop closes the child's stdin so it restores the gamma and exits; kills it if it hangs.
func (h *holder) stop() {
	if h.cmd == nil {
		return
	}
	_ = h.stdin.Close()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		_ = h.cmd.Process.Kill()
		<-h.done
	}
	h.cmd = nil
}

// apply writes one tint to the child, restarting the child if it died.
func (h *holder) apply(r Ratios) error {
	if !h.alive() {
		h.stop()
		if err := h.start(); err != nil {
			return fmt.Errorf("start helper: %w", err)
		}
		h.log.Printf("helper started")
	}
	if _, err := fmt.Fprintf(h.stdin, "%.5f %.5f %.5f\n", r[0], r[1], r[2]); err != nil {
		h.stop()
		return fmt.Errorf("write to helper: %w", err)
	}
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() { l, err := h.out.ReadString('\n'); ch <- result{l, err} }()
	select {
	case res := <-ch:
		if res.err != nil {
			h.stop()
			return fmt.Errorf("helper exited: %w", res.err)
		}
		if line := strings.TrimSpace(res.line); line != "ok" {
			return errors.New(line)
		}
		return nil
	case <-time.After(childReply):
		_ = h.cmd.Process.Kill()
		h.stop()
		return errors.New("helper did not answer in time")
	}
}

// refresh re-asserts the current tint, undoing a reset by the OS. Only changes in
// success/failure are logged, so a long outage does not flood the log.
func (h *holder) refresh() {
	if h.last == nil {
		return
	}
	err := h.apply(*h.last)
	switch {
	case err != nil && !h.failing:
		h.failing = true
		h.log.Printf("refresh failed: %v", err)
	case err == nil && h.failing:
		h.failing = false
		h.log.Printf("refresh recovered")
	}
}

// openLog returns the holder's logger (a size-capped file next to the daemon logs), or a
// discarding logger if the file cannot be opened: logging must never stop the holder.
func openLog() *log.Logger {
	home, err := os.UserHomeDir()
	if err != nil {
		return log.New(io.Discard, "", 0)
	}
	dir := filepath.Join(home, "Library", "Logs", "moonlight")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return log.New(io.Discard, "", 0)
	}
	path := filepath.Join(dir, "holder.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogBytes {
		flags |= os.O_TRUNC // crude rotation: start over once it is large
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return log.New(io.Discard, "", 0)
	}
	return log.New(f, fmt.Sprintf("holder[%d] ", os.Getpid()), log.Ldate|log.Ltime)
}

// RunHolder is the entry point of the hidden `moonlight gamma-holder` command.
// It serves requests until the lease lapses, a RESET arrives, or it is signalled.
func RunHolder() error {
	logger := openLog()
	path, err := socketPath()
	if err != nil {
		logger.Printf("no socket path: %v", err)
		return err
	}

	// One holder at a time: the lock is released by the OS however this process ends.
	lock, err := os.OpenFile(strings.TrimSuffix(path, ".sock")+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		logger.Printf("open lock: %v", err)
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		logger.Printf("another holder is already running; exiting")
		return nil
	}

	_ = os.Remove(path) // stale socket from a crashed holder
	ln, err := net.Listen("unix", path)
	if err != nil {
		logger.Printf("listen: %v", err)
		return err
	}
	_ = os.Chmod(path, 0o600)
	defer os.Remove(path)
	defer ln.Close()

	conns := make(chan net.Conn)
	acceptDone := make(chan error, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				acceptDone <- err
				return
			}
			conns <- c
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	h := &holder{log: logger, deadline: time.Now().Add(initialGrace)}
	defer h.stop()
	tick := time.NewTicker(refreshInterval)
	defer tick.Stop()
	logger.Printf("started, refreshing every %v", refreshInterval)

	for {
		select {
		case s := <-sig:
			logger.Printf("signal %v; releasing gamma", s)
			return nil
		case err := <-acceptDone:
			logger.Printf("socket closed (%v); exiting", err)
			return nil
		case c := <-conns:
			if quit := h.serve(c); quit {
				return nil
			}
		case now := <-tick.C:
			if now.After(h.deadline) {
				logger.Printf("lease lapsed; releasing gamma")
				return nil
			}
			h.refresh()
		}
	}
}

// serve handles one request; it reports true when the holder should shut down.
func (h *holder) serve(c net.Conn) (quit bool) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(requestWindow))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return false
	}
	req, err := parseRequest(line)
	if err != nil {
		fmt.Fprintf(c, "ERR %v\n", err)
		return false
	}
	switch {
	case req.ping:
		fmt.Fprintln(c, "OK "+protocolVersion)
		return false
	case req.reset:
		h.log.Printf("reset requested; releasing gamma")
		h.stop()
		fmt.Fprintln(c, "OK")
		return true
	case req.touch:
		if h.last == nil {
			fmt.Fprintln(c, "ERR no tint is being held")
			return false
		}
		if d := time.Now().Add(req.ttl); d.After(h.deadline) { // extend only, never shorten
			h.deadline = d
		}
		fmt.Fprintln(c, "OK")
		return false
	default:
		if err := h.apply(req.ratios); err != nil {
			h.log.Printf("apply %v failed: %v", req.ratios, err)
			fmt.Fprintf(c, "ERR %v\n", err)
			return false
		}
		if h.last == nil || *h.last != req.ratios {
			h.log.Printf("holding %v (lease %v)", req.ratios, req.ttl)
		}
		r := req.ratios
		h.last = &r
		h.deadline = time.Now().Add(req.ttl) // an explicit SET sets the lease, up or down
		h.failing = false
		fmt.Fprintln(c, "OK")
		return false
	}
}
