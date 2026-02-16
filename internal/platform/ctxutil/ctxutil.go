package ctxutil

import "context"

// MustGet is for internal helpers; prefer explicit passing in usecases.
func MustGet[T any](ctx context.Context, key any) T {
	v, ok := ctx.Value(key).(T)
	if !ok {
		var zero T
		return zero
	}
	return v
}
