//go:build !linux

package installer

import (
	"context"
	"errors"
	"io"
)

func Run(context.Context, string, Options, RenderFunc, io.Writer) error {
	return errors.New("the installer requires Linux")
}
