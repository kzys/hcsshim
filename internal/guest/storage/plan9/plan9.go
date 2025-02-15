//go:build linux
// +build linux

package plan9

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/Microsoft/hcsshim/internal/guest/transport"
	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
	"golang.org/x/sys/unix"
)

const packetPayloadBytes = 65536

// Test dependencies
var (
	osMkdirAll  = os.MkdirAll
	osRemoveAll = os.RemoveAll
	unixMount   = unix.Mount
)

// Mount dials a connection from `vsock` and mounts a Plan9 share to `target`.
//
// `target` will be created. On mount failure the created `target` will be
// automatically cleaned up.
func Mount(ctx context.Context, vsock transport.Transport, target, share string, port uint32, readonly bool) (err error) {
	_, span := trace.StartSpan(ctx, "plan9::Mount")
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()

	span.AddAttributes(
		trace.StringAttribute("target", target),
		trace.StringAttribute("share", share),
		trace.Int64Attribute("port", int64(port)),
		trace.BoolAttribute("readonly", readonly))

	if err := osMkdirAll(target, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			osRemoveAll(target)
		}
	}()
	conn, err := vsock.Dial(port)
	if err != nil {
		return errors.Wrapf(err, "could not connect to plan9 server for %s", target)
	}
	f, err := conn.File()
	conn.Close()
	if err != nil {
		return errors.Wrapf(err, "could not get file for plan9 connection for %s", target)
	}
	defer f.Close()

	var mountOptions uintptr
	data := fmt.Sprintf("trans=fd,rfdno=%d,wfdno=%d,msize=%d", f.Fd(), f.Fd(), packetPayloadBytes)
	if readonly {
		mountOptions |= unix.MS_RDONLY
		data += ",noload"
	}
	if share != "" {
		data += ",aname=" + share
	}

	// set socket options to maximize bandwidth
	err = syscall.SetsockoptInt(int(f.Fd()), syscall.SOL_SOCKET, syscall.SO_RCVBUF, packetPayloadBytes)
	if err != nil {
		return errors.Wrapf(err, "failed to set sock option syscall.SO_RCVBUF to %v on fd %v", packetPayloadBytes, f.Fd())
	}
	err = syscall.SetsockoptInt(int(f.Fd()), syscall.SOL_SOCKET, syscall.SO_SNDBUF, packetPayloadBytes)
	if err != nil {
		return errors.Wrapf(err, "failed to set sock option syscall.SO_SNDBUF to %v on fd %v", packetPayloadBytes, f.Fd())
	}
	if err := unixMount(target, target, "9p", mountOptions, data); err != nil {
		return errors.Wrapf(err, "failed to mount directory for mapped directory %s", target)
	}
	return nil
}
