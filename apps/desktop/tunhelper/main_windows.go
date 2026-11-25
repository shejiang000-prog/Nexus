// nexus-tun-helper accepts only a local pipe rendezvous with the invoking core.
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

var errPeer = errors.New("invalid TUN helper peer")

func helperArgs(args []string) (string, uint32, string, error) {
	if len(args) != 3 || (args[0] != "full" && args[0] != "test" && args[0] != "recover") {
		return "", 0, "", errPeer
	}
	id, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != args[1] || len(args[2]) != 64 {
		return "", 0, "", errPeer
	}
	for _, c := range args[2] {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return "", 0, "", errPeer
		}
	}
	return args[0], uint32(id), args[2], nil
}

func logonID(token windows.Token) (*windows.SID, error) {
	groups, err := token.GetTokenGroups()
	if err != nil {
		return nil, err
	}
	for _, group := range groups.AllGroups() {
		if group.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			return group.Sid.Copy()
		}
	}
	return nil, errPeer
}

// Split UAC tokens can represent linked logons. Accept that specific linked
// token, never another logon of the same account on the same workstation.
func sameLogon(token windows.Token) bool {
	other, err := logonID(token)
	if err != nil {
		return false
	}
	current := windows.GetCurrentProcessToken()
	own, err := logonID(current)
	if err == nil && own.Equals(other) {
		return true
	}
	linked, err := current.GetLinkedToken()
	if err != nil {
		return false
	}
	defer linked.Close()
	own, err = logonID(linked)
	return err == nil && own.Equals(other)
}

func verifyPeer(pipe, proc windows.Handle, pid uint32, expectedImage string) (string, error) {
	var server, session, ourSession uint32
	if windows.GetNamedPipeServerProcessId(pipe, &server) != nil || server != pid ||
		windows.ProcessIdToSessionId(pid, &session) != nil ||
		windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &ourSession) != nil || session != ourSession {
		return "", errPeer
	}
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	peer, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	own, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !own.User.Sid.Equals(peer.User.Sid) || !sameLogon(token) {
		return "", errPeer
	}

	name := make([]uint16, 32768)
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(proc, 0, &name[0], &size); err != nil {
		return "", err
	}
	actual := windows.UTF16ToString(name[:size])
	// Reject unrelated paths before opening them with an elevated token.
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(expectedImage)) {
		return "", errPeer
	}
	// Verify file AND directory identity. A hard link from another directory
	// must not authorize a core with a different adjacent installation.
	same := func(a, b string) bool {
		first, e1 := os.Stat(a)
		second, e2 := os.Stat(b)
		return e1 == nil && e2 == nil && os.SameFile(first, second)
	}
	if !same(filepath.Dir(actual), filepath.Dir(expectedImage)) || !same(actual, expectedImage) {
		return "", errPeer
	}
	state, err := windows.WaitForSingleObject(proc, 0)
	if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		return "", errPeer
	}
	return peer.User.Sid.String(), nil
}

func openLocalPipe(nonce string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(`\\.\pipe\NexusTun-` + nonce)
	if err != nil {
		return windows.InvalidHandle, err
	}
	// The server may identify us but cannot impersonate an elevated client.
	return windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION|windows.FILE_FLAG_OVERLAPPED, 0)
}

func serve(parent context.Context, mode string, proc windows.Handle, owner string, pipe *os.File) bool {
	send := func(code byte) error {
		_, err := pipe.Write([]byte{code})
		return err
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		_ = send(1)
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	done, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = send(3)
		cancel()
		return false
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result, err := windows.WaitForMultipleObjects([]windows.Handle{proc, done}, false, windows.INFINITE)
		if err != nil || result != windows.WAIT_OBJECT_0+1 {
			cancel()
			pipe.Close() // wake a pending read/write, including during startup
		}
	}()
	defer func() { windows.SetEvent(done); <-finished; windows.CloseHandle(done); cancel() }()
	if ctx.Err() != nil {
		return false
	}
	if mode == "recover" {
		if recoverNetwork(owner) != nil {
			_ = send(4)
			return false
		}
		return send(0) == nil
	}
	dll, err := pinWintun()
	if err != nil {
		_ = send(2)
		return false
	}
	defer dll.Close()
	started := false
	err = runNetwork(ctx, mode, owner, pipe, func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := send(0); err != nil {
			return err
		}
		started = true
		return nil
	})
	if err != nil && !started {
		var code byte = 2
		switch networkErrorCode(err) {
		case "tun_admin":
			code = 1
		case "tun_busy", "tun_conflict", "tun_network":
			code = 3
		case "tun_recovery":
			code = 4
		}
		_ = send(code)
	}
	return err == nil && started
}

func runHelper() bool {
	mode, pid, nonce, err := helperArgs(os.Args[1:])
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	// Keep this exact process object alive through authentication and cleanup.
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(proc)
	handle, err := openLocalPipe(nonce)
	if err != nil {
		return false
	}
	pipe := os.NewFile(uintptr(handle), "Nexus TUN pipe")
	defer pipe.Close()
	owner, err := verifyPeer(handle, proc, pid, filepath.Join(filepath.Dir(exe), "nexus-core.exe"))
	if err != nil {
		return false
	}
	return serve(context.Background(), mode, proc, owner, pipe)
}

func main() {
	if len(os.Args) == 2 {
		var err error
		switch os.Args[1] {
		case "--service":
			err = runWindowsService()
		case "--install-service", "--remove-service":
			err = manageWindowsService(os.Args[1] == "--install-service")
		default:
			err = errPeer
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if !runHelper() {
		os.Exit(1)
	}
}
