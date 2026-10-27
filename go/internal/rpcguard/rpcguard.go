// Package rpcguard keeps a panic in one gRPC handler from stopping the
// whole process. gRPC runs each call on its own goroutine and does not
// recover panics, so without this a bug that one request triggers, such
// as an unexpected input, takes down the coordinator and every job with
// it. The call fails with codes.Internal instead, and the panic and its
// stack are logged so the bug can still be found.
//
// Handlers must release their locks with defer, so that a recovered panic
// leaves no lock held; the coordinator's and the shuffle server's do.
package rpcguard

import (
	"context"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServerOptions returns the options that make a grpc.Server recover from
// panics in unary and streaming handlers.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unary),
		grpc.ChainStreamInterceptor(stream),
	}
}

func unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer recoverTo(info.FullMethod, &err)
	return handler(ctx, req)
}

func stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer recoverTo(info.FullMethod, &err)
	return handler(srv, ss)
}

// recoverTo, deferred, turns a panic into an Internal error in *err.
func recoverTo(method string, err *error) {
	if p := recover(); p != nil {
		slog.Error("panic in RPC handler", "method", method, "panic", p, "stack", string(debug.Stack()))
		*err = status.Errorf(codes.Internal, "%s: internal error: %v", method, p)
	}
}
