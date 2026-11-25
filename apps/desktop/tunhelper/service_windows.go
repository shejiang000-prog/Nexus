package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/xtls/libxray/tuntrust"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const coreDigestName = "ServiceCoreSHA256"

func installedPaths() (helper, core string, err error) {
	helper, err = tuntrust.InstalledHelper()
	if err != nil {
		return
	}
	if !tuntrust.ImageMatches(windows.CurrentProcess(), helper) {
		return "", "", errPeer
	}
	return helper, filepath.Join(filepath.Dir(helper), "nexus-core.exe"), nil
}

func setDACL(h windows.Handle, kind windows.SE_OBJECT_TYPE, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, kind, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func stopService(s *mgr.Service) error {
	deadline := time.Now().Add(25 * time.Second)
	for {
		state, err := s.Query()
		if err != nil || state.State == svc.Stopped {
			return err
		}
		if state.State == svc.Running {
			_, err = s.Control(svc.Stop)
			if err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) && !errors.Is(err, windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL) {
				return err
			}
		}
		if time.Now().After(deadline) {
			return busyNetwork
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Install and removal are installer-only fixed actions. Only a matching
// service can be changed, and a failed rollback keeps the journal for retry.
func manageWindowsService(install bool) (result error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errPeer
	}
	image, core, err := installedPaths()
	if err != nil {
		return err
	}
	helper, _, err := tuntrust.InstalledDigest(image)
	if err != nil {
		return err
	}
	defer helper.Close()
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(tuntrust.ServiceName)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return err
	}
	if s != nil {
		defer s.Close()
		cfg, err := s.Config()
		if err != nil || !tuntrust.ServiceMatches(cfg, image) {
			return errPeer
		}
		if !install {
			// Close the SCM start-vs-delete window. Restore the installed
			// configuration on any failure before deletion.
			changeStart := func(kind uint32) error {
				return windows.ChangeServiceConfig(s.Handle, windows.SERVICE_NO_CHANGE, kind, windows.SERVICE_NO_CHANGE, nil, nil, nil, nil, nil, nil, nil)
			}
			if err = changeStart(windows.SERVICE_DISABLED); err != nil {
				return err
			}
			defer func() {
				if result != nil {
					result = errors.Join(result, changeStart(cfg.StartType))
				}
			}()
			if err = stopService(s); err != nil {
				return err
			}
		}
	}
	unlock, err := lockNetwork()
	if err != nil {
		return err
	}
	defer unlock()
	k, err := openJournal()
	if err != nil {
		return err
	}
	defer k.Close()
	if !install {
		r, err := loadJournal(k)
		if err != nil {
			return err
		}
		if r != nil {
			if err = recoverLocked(k, r.OwnerSID); err != nil {
				return err
			}
		}
		digest, _, err := k.GetStringValue(coreDigestName)
		if err == nil {
			defer func() {
				if result != nil {
					result = errors.Join(result, k.SetStringValue(coreDigestName, digest), flushJournal(k))
				}
			}()
			if err = k.DeleteValue(coreDigestName); err != nil {
				return err
			}
			if err = flushJournal(k); err != nil {
				return err
			}
		} else if !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		if s != nil {
			return s.Delete()
		}
		return nil
	}
	pin, digest, err := tuntrust.InstalledDigest(core)
	if err != nil {
		return err
	}
	defer pin.Close()
	if s == nil {
		s, err = m.CreateService(tuntrust.ServiceName, image, mgr.Config{StartType: mgr.StartManual, DisplayName: "Nexus Desktop TUN"}, "--service")
		if err != nil {
			return err
		}
		defer s.Close()
		defer func() {
			if result != nil {
				result = errors.Join(result, s.Delete())
			}
		}()
	}
	// Interactive logons can query configuration/status and request a start;
	// only the installer or SCM can change, stop and delete the service.
	if err = setDACL(s.Handle, windows.SE_SERVICE, "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;CCLCRP;;;IU)"); err != nil {
		return err
	}
	if err = k.SetStringValue(coreDigestName, digest); err != nil {
		return err
	}
	return flushJournal(k)
}

type tunService struct{ completed atomic.Bool }

func runWindowsService() error {
	acting, err := svc.IsWindowsService()
	if err != nil || !acting {
		return errPeer
	}
	var worker tunService
	if err := svc.Run(tuntrust.ServiceName, &worker); err != nil {
		return err
	}
	// The core waits on the worker process. svc.Run reports only dispatcher
	// failures, so propagate the operation's exit status to it as well.
	if !worker.completed.Load() {
		return staleNetwork
	}
	return nil
}

func (worker *tunService) Execute(args []string, requests <-chan svc.ChangeRequest, send chan<- svc.Status) (bool, uint32) {
	send <- svc.Status{State: svc.StartPending}
	if len(args) != 4 || !strings.EqualFold(args[0], tuntrust.ServiceName) {
		return false, 1
	}
	mode, pid, nonce, err := helperArgs(args[1:])
	if err != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	send <- current
	done := make(chan bool, 1)
	go func() { done <- serviceSession(ctx, mode, pid, nonce) }()
	for {
		select {
		case ok := <-done:
			worker.completed.Store(ok)
			if !ok {
				return false, 1
			}
			return false, 0
		case r, open := <-requests:
			if !open {
				cancel()
				requests = nil
				continue
			}
			switch r.Cmd {
			case svc.Interrogate:
				send <- current
			case svc.Stop, svc.Shutdown:
				current = svc.Status{State: svc.StopPending, WaitHint: 20000}
				send <- current
				cancel()
			}
		}
	}
}

// Session zero cannot use the service's own logon SID. WTS provides the
// authoritative interactive token for the client's actual terminal session.
func sessionOwner(proc windows.Handle, pid uint32) (user, logon string, err error) {
	var session uint32
	if err = windows.ProcessIdToSessionId(pid, &session); err != nil || session == 0 {
		return "", "", errPeer
	}
	var client, interactive windows.Token
	if err = windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &client); err != nil {
		return
	}
	defer client.Close()
	if err = windows.WTSQueryUserToken(session, &interactive); err != nil {
		return
	}
	defer interactive.Close()
	a, err := client.GetTokenUser()
	if err != nil {
		return "", "", err
	}
	b, err := interactive.GetTokenUser()
	if err != nil {
		return "", "", err
	}
	c, err := logonID(client)
	if err != nil {
		return "", "", err
	}
	d, err := logonID(interactive)
	if err != nil {
		return "", "", err
	}
	if !a.User.Sid.Equals(b.User.Sid) || !c.Equals(d) {
		return "", "", errPeer
	}
	return a.User.Sid.String(), c.String(), nil
}

func serviceSession(ctx context.Context, mode string, pid uint32, nonce string) bool {
	_, core, err := installedPaths()
	if err != nil {
		return false
	}
	k, err := openJournal()
	if err != nil {
		return false
	}
	digest, _, err := k.GetStringValue(coreDigestName)
	k.Close()
	if err != nil {
		return false
	}
	pin, actual, err := tuntrust.InstalledDigest(core)
	if err != nil {
		return false
	}
	defer pin.Close()
	if len(digest) != 64 || digest != actual {
		return false
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(proc)
	if !tuntrust.ImageMatches(proc, core) {
		return false
	}
	owner, logon, err := sessionOwner(proc, pid)
	if err != nil {
		return false
	}
	// Let only this interactive logon query and wait on the SCM worker PID,
	// so the core can verify its pipe peer and wait for kernel-state cleanup.
	if setDACL(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT, "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x101000;;;"+logon+")") != nil {
		return false
	}
	h, err := openLocalPipe(nonce)
	if err != nil {
		return false
	}
	pipe := os.NewFile(uintptr(h), "Nexus service session")
	defer pipe.Close()
	stop := context.AfterFunc(ctx, func() { pipe.Close() })
	defer stop()
	var peer uint32
	if windows.GetNamedPipeServerProcessId(h, &peer) != nil || peer != pid {
		return false
	}
	event, err := windows.WaitForSingleObject(proc, 0)
	if err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		return false
	}
	// The operation is confirmed by the authenticated core, never by another
	// caller of StartService that happened to observe a rendezvous name.
	if pipe.SetReadDeadline(time.Now().Add(8*time.Second)) != nil {
		return false
	}
	var confirm [1]byte
	if _, err = io.ReadFull(pipe, confirm[:]); err != nil || confirm[0] != tuntrust.ModeByte(mode) {
		return false
	}
	if pipe.SetReadDeadline(time.Time{}) != nil {
		return false
	}
	return serve(ctx, mode, proc, owner, pipe)
}
