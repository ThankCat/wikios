package service

import (
	"context"
	"testing"
	"time"

	"wikios/internal/config"
)

func TestCustomerChatConcurrencySlotHonorsLimit(t *testing.T) {
	svc := NewCustomerChatService(Deps{
		Config: &config.Config{
			CustomerChat: config.CustomerQueryConfig{MaxConcurrent: 1},
		},
	})
	release, err := svc.acquireCustomerChatSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := svc.acquireCustomerChatSlot(ctx); err == nil {
		t.Fatalf("expected second slot acquire to wait until context deadline")
	}

	release()
	releaseAgain, err := svc.acquireCustomerChatSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire slot after release: %v", err)
	}
	releaseAgain()
}
