package plugin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TokenHeader is where the token rides. Metadata rather than a request field,
// because it is about the connection rather than about the call.
const TokenHeader = "dusk-plugin-token"

func mintToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("plugin: mint a token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func presentToken(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoke(withToken(ctx, token), method, req, reply, conn, opts...)
	}
}

func presentTokenStream(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, conn *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(withToken(ctx, token), desc, conn, method, opts...)
	}
}

func withToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, TokenHeader, token)
}
