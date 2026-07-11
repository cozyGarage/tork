package runtime

import (
	"context"

	"github.com/cozyGarage/transformer"
)

type Mounter interface {
	Mount(ctx context.Context, mnt *tork.Mount) error
	Unmount(ctx context.Context, mnt *tork.Mount) error
}
