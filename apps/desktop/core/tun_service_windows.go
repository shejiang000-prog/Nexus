package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xtls/libxray/tuntrust"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Use the existing one-shot UAC helper when SCM cannot start a worker.
// A foreign registration, live owner or failed handshake is an error.
func requestTunService(path, mode, nonce string) (*mgr.Service, error) {
	installed, err := tuntrust.InstalledHelper()
	if err != nil {
		return nil, tunFailure("tun_admin")
	}
	if !strings.EqualFold(installed, path) {
		return nil, nil
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil
	}
	defer windows.CloseServiceHandle(scm)
	name, _ := windows.UTF16PtrFromString(tuntrust.ServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS|windows.SERVICE_START)
	if err != nil {
		return nil, nil
	}
	s := &mgr.Service{Name: tuntrust.ServiceName, Handle: h}
	cfg, err := s.Config()
	if err != nil || !tuntrust.ServiceMatches(cfg, path) {
		s.Close()
		return nil, tunFailure("tun_admin")
	}
	if cfg.StartType == windows.SERVICE_DISABLED {
		s.Close()
		return nil, nil
	}
	err = s.Start(mode, strconv.Itoa(os.Getpid()), nonce)
	if err == nil {
		return s, nil
	}
	s.Close()
	if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return nil, tunFailure("tun_conflict")
	}
	return nil, nil
}

func servicePeer(s *mgr.Service, path string, h, event windows.Handle, pending bool) (windows.Handle, error) {
	until := time.Now().Add(30 * time.Second)
	for {
		status, err := s.Query()
		if err != nil || status.State == svc.Stopped {
			return 0, tunFailure("tun_admin")
		}
		if pending {
			signaled, err := windows.WaitForSingleObject(event, 100)
			if err != nil || time.Now().After(until) {
				return 0, tunFailure("tun_admin")
			}
			if signaled == uint32(windows.WAIT_TIMEOUT) {
				continue
			}
			if signaled != windows.WAIT_OBJECT_0 {
				return 0, tunFailure("tun_admin")
			}
		}
		var pid uint32
		status, err = s.Query()
		if err != nil {
			return 0, tunFailure("tun_admin")
		}
		if windows.GetNamedPipeClientProcessId(h, &pid) != nil || pid == 0 || pid != status.ProcessId {
			return 0, tunFailure("tun_admin")
		}
		proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			return 0, tunFailure("tun_admin")
		}
		// Keep the observed process object alive. Query the SCM again after
		// opening it to exclude PID reuse. The exact on-disk image is pinned.
		again, err := s.Query()
		if err != nil || again.State != svc.Running || again.ProcessId != pid || !tuntrust.ImageMatches(proc, path) {
			windows.CloseHandle(proc)
			return 0, tunFailure("tun_admin")
		}
		return proc, nil
	}
}
