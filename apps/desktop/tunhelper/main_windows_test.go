package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/xtls/libxray/tuntrust"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Exercise only the unprivileged rendezvous, with the test process as peer.
// This does not call serve, load Wintun, request elevation or configure a NIC.
func TestLocalPipePeer(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(key[:])
	pid := windows.GetCurrentProcessId()
	args := []string{"full", strconv.FormatUint(uint64(pid), 10), nonce}
	if _, _, _, err := helperArgs(args); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{
		{"full", "0", nonce}, {"full", "10", strings.Repeat("g", 64)},
		{"other", "10", nonce}, {"test", "10", nonce, "extra"},
	} {
		if _, _, _, err := helperArgs(bad); err == nil {
			t.Fatal("accepted invalid arguments")
		}
	}
	name, _ := windows.UTF16PtrFromString(`\\.\pipe\NexusTun-` + nonce)
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(server)
	client, err := openLocalPipe(nonce)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(client)
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(proc)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := verifyPeer(client, proc, pid, exe); err != nil || owner == "" {
		t.Fatalf("same process rejected: %v", err)
	}
	if _, err := verifyPeer(client, proc, pid+1, exe); err == nil {
		t.Fatal("accepted incorrect pipe owner")
	}
	if _, err := verifyPeer(client, proc, pid, filepath.Join(t.TempDir(), "nexus-core.exe")); err == nil {
		t.Fatal("accepted a different installation")
	}
}

// Exercise the SCM entry without registering a service or using an admin token.
func TestInstalledServiceBoundary(t *testing.T) {
	image, err := tuntrust.InstalledHelper()
	if err != nil {
		t.Fatal(err)
	}
	config := mgr.Config{ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, ServiceStartName: "LocalSystem", BinaryPathName: syscall.EscapeArg(image) + " --service"}
	if !tuntrust.ServiceMatches(config, image) || tuntrust.ServiceMatches(config, filepath.Join(t.TempDir(), "nexus-tun-helper.exe")) {
		t.Fatal("service image check")
	}
	config.BinaryPathName += " other"
	if tuntrust.ServiceMatches(config, image) {
		t.Fatal("accepted extra service arguments")
	}
	temp := filepath.Join(t.TempDir(), "nexus-core.exe")
	if err := os.WriteFile(temp, []byte("user-writable"), 0600); err != nil {
		t.Fatal(err)
	}
	if lock, _, err := tuntrust.InstalledDigest(temp); err == nil {
		lock.Close()
		t.Fatal("accepted user-writable installation")
	}
	if _, _, err := installedPaths(); err == nil {
		t.Fatal("test process matched installed helper")
	}
	var worker tunService
	for _, mode := range []string{"test", "full", "recover", "other"} {
		status := make(chan svc.Status, 2)
		args := []string{tuntrust.ServiceName, mode, strconv.Itoa(os.Getpid()), strings.Repeat("a", 64)}
		_, rc := worker.Execute(args, nil, status)
		if rc == 0 || worker.completed.Load() {
			t.Fatal("accepted unauthenticated request")
		}
	}
}
