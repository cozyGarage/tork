package job

import (
	"context"

	"github.com/cozyGarage/transformer"
	"github.com/cozyGarage/transformer/internal/redact"
)

func Redact(redacter *redact.Redacter) MiddlewareFunc {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, et EventType, j *tork.Job) error {
			if et == Read {
				redacter.RedactJob(j)
			}
			return next(ctx, et, j)
		}
	}
}
