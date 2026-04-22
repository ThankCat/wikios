package service

import "context"

type StreamEvent struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

type StreamEmitter interface {
	Emit(StreamEvent)
}

type streamEmitterKey struct{}

func WithStreamEmitter(ctx context.Context, emitter StreamEmitter) context.Context {
	return context.WithValue(ctx, streamEmitterKey{}, emitter)
}

func emitStreamEvent(ctx context.Context, eventType string, data any) {
	emitter, ok := ctx.Value(streamEmitterKey{}).(StreamEmitter)
	if !ok || emitter == nil {
		return
	}
	emitter.Emit(StreamEvent{Type: eventType, Data: data})
}
