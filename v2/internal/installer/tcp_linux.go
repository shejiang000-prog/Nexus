package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"
)

const tcpConfig = "/etc/sysctl.d/99-nexus-v2-bbr.conf"

var tcpKeys = [2]string{"net.ipv4.tcp_congestion_control", "net.core.default_qdisc"}
var tcpWant = [2]string{"bbr", "fq"}
var kernelName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func tcpConfigText(token string, previous [2]string) string {
	return fmt.Sprintf("# Nexus V2 %s\n# Previous defaults: %s %s\n%s = bbr\n%s = fq\n",
		token, previous[0], previous[1], tcpKeys[0], tcpKeys[1])
}

// A locally edited or masked drop-in belongs to the administrator.
func (h host) tcpConfigState(token string) (previous [2]string, present, owned bool, err error) {
	path := h.path(tcpConfig)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return previous, false, false, nil
	}
	if err != nil {
		return previous, false, false, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 512 || fi.Mode().Perm()&022 != 0 || fi.Sys().(*syscall.Stat_t).Uid != 0 {
		return previous, true, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return previous, true, false, err
	}
	_, err = fmt.Sscanf(string(b), "# Nexus V2 "+token+"\n# Previous defaults: %s %s", &previous[0], &previous[1])
	owned = err == nil && kernelName.MatchString(previous[0]) && kernelName.MatchString(previous[1]) && string(b) == tcpConfigText(token, previous)
	return previous, true, owned, nil
}

func (h host) tcpValue(ctx context.Context, key string) (string, error) {
	b, err := h.command(ctx, "sysctl", "-n", key)
	value := strings.TrimSpace(string(b))
	if err == nil && !kernelName.MatchString(value) {
		err = fmt.Errorf("invalid sysctl value for %s", key)
	}
	return value, err
}

func (h host) setTCP(ctx context.Context, key, value string) error {
	_, err := h.command(ctx, "sysctl", "-w", key+"="+value)
	return err
}

func (h host) tuneTCP(ctx context.Context, token string, tx *transaction, install bool) error {
	previous, present, owned, err := h.tcpConfigState(token)
	if err != nil {
		return err
	}
	if present && !owned {
		fmt.Fprintf(h.out, "TCP tuning skipped: administrator owns %s\n", tcpConfig)
		return nil
	}
	if !install && !owned {
		return nil
	}

	// Applying the sysctls probes the running kernel, including on-demand module
	// loading. Restricted containers and kernels lacking either facility keep their defaults.
	var before [2]string
	var changes transaction
	for i, key := range tcpKeys {
		before[i], err = h.tcpValue(ctx, key)
		if err != nil && !install && ctx.Err() == nil {
			fmt.Fprintf(h.out, "TCP restore skipped for unavailable %s: %v\n", key, err)
			err = nil
			continue
		}
		if err != nil {
			break
		}
		target := tcpWant[i]
		if !install {
			// Keep later administrator changes.
			if before[i] != target {
				continue
			}
			target = previous[i]
		}
		if before[i] == target {
			continue
		}
		if err = h.setTCP(ctx, key, target); err != nil {
			break
		}
		changes.undo(func() error { return h.setTCP(context.Background(), key, before[i]) })
		var after string
		after, err = h.tcpValue(ctx, key)
		if err == nil && after != target {
			err = fmt.Errorf("%s read back %s", key, after)
		}
		if err != nil {
			break
		}
	}
	if err != nil && install {
		fmt.Fprintf(h.out, "TCP tuning skipped: %v\n", err)
		// Restore any partial capability probe before allowing installation to continue.
		for i := len(changes) - 1; i >= 0; i-- {
			if undo := changes[i](); undo != nil {
				return errors.Join(err, undo)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !owned {
			return nil
		}
		// The kernel changed since installation. Drop stale boot settings as well.
		install = false
	} else {
		*tx = append(*tx, changes...)
		if err != nil {
			return err
		}
	}

	path := h.path(tcpConfig)
	if install {
		if !owned {
			if err := copyFile(path, strings.NewReader(tcpConfigText(token, before)), 0644); err != nil {
				return err
			}
			tx.undo(func() error { return os.Remove(path) })
		}
		fmt.Fprintln(h.out, "TCP congestion control: bbr; default qdisc: fq")
	} else {
		if err := os.Remove(path); err != nil {
			return err
		}
		tx.undo(func() error { return copyFile(path, strings.NewReader(tcpConfigText(token, previous)), 0644) })
	}
	return nil
}
