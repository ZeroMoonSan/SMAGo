package main

import "context"

type chatIDCtxKey struct{}

func WithChatID(ctx context.Context, chatID int64) context.Context {
	return context.WithValue(ctx, chatIDCtxKey{}, chatID)
}

func ChatIDFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(chatIDCtxKey{}).(int64)
	return id
}
