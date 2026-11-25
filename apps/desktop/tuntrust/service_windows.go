package tuntrust

import (
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

const ServiceName = "NexusDesktopTun"

func InstalledHelper() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFilesX64, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Nexus", "app", "nexus-tun-helper.exe"), nil
}

func ServiceMatches(config mgr.Config, image string) bool {
	return config.ServiceType == windows.SERVICE_WIN32_OWN_PROCESS && strings.EqualFold(config.ServiceStartName, "LocalSystem") && strings.EqualFold(config.BinaryPathName, syscall.EscapeArg(image)+" --service")
}

func ImageMatches(proc windows.Handle, expected string) bool {
	name := make([]uint16, 32768)
	n := uint32(len(name))
	return windows.QueryFullProcessImageName(proc, 0, &name[0], &n) == nil && strings.EqualFold(filepath.Clean(windows.UTF16ToString(name[:n])), filepath.Clean(expected))
}

func ModeByte(mode string) byte {
	switch mode {
	case "full":
		return 1
	case "test":
		return 2
	case "recover":
		return 3
	}
	return 0
}
