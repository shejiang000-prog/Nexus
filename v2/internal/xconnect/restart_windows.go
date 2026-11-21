package xconnect

import (
	"os"
	"os/exec"
)

func restart(exe string) error {
	c := exec.Command(exe, os.Args[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil
}
