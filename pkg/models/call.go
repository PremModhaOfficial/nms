package models

import (
	"context"
	"errors"
	"time"
)

// RPCTimeout bounds every synchronous request-reply exchange.
const RPCTimeout = 10 * time.Second

// ErrRPCTimeout is returned when a request-reply exchange exceeds RPCTimeout.
var ErrRPCTimeout = errors.New("rpc timeout")

// Call sends req to reqCh and waits for the reply on req.ReplyCh, bounded by
// ctx and RPCTimeout. A nil reply channel or stopped service returns an error
// instead of hanging the caller forever.
func Call(ctx context.Context, reqCh chan<- Request, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case reqCh <- req:
	}

	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case resp, ok := <-req.ReplyCh:
		if !ok {
			return Response{}, errors.New("reply channel closed")
		}
		return resp, nil
	}
}
