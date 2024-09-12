package xeo

import (
	"context"
	"golang.org/x/time/rate"
	"sync"
	"testing"
	"time"
)

type test struct{}

func (t test) Update(_ *Update) {}

var token = "1713461126:AAEV5sgVo513Vz4PT33mpp0ZykJqrnSluzM"

func TestNewDispatcher(t *testing.T) {
	d, err := NewDispatcher(token, func(chatId int64) Bot { return &MockBot{} })
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}
	if d == nil {
		t.Fatal("Dispatcher is nil")
	}
}

// MockBot is a mock implementation of the Bot interface
type MockBot struct {
	mu          sync.Mutex
	updateCount int
}

func (m *MockBot) Update(u *Update) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCount++
}

func TestDispatcherStart(t *testing.T) {
	d, _ := NewDispatcher(token, func(chatId int64) Bot { return &MockBot{} })
	err := d.Start()
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Try to start again, should fail
	err = d.Start()
	if err == nil {
		t.Fatal("Expected error when starting dispatcher twice")
	}
}

func TestDispatcherStop(t *testing.T) {
	d, _ := NewDispatcher(token, func(chatId int64) Bot { return &MockBot{} })
	err := d.Start()
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = d.Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop dispatcher: %v", err)
	}

	// Try to stop again, should fail
	err = d.Stop(ctx)
	if err == nil {
		t.Fatal("Expected error when stopping dispatcher twice")
	}
}

func TestDispatcherProcessUpdate(t *testing.T) {
	// Create a context with a timeout to ensure the test doesn't run indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mockBot := &MockBot{}
	dsp, err := NewDispatcher("test_token", func(chatId int64) Bot { return mockBot })
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Start the dispatcher
	if err := dsp.Start(); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer func(dsp *Dispatcher, ctx context.Context) {
		err := dsp.Stop(ctx)
		if err != nil {
			t.Logf("Error stopping dispatcher: %v", err)
		}
	}(dsp, ctx)

	updateTypes := []Update{
		{}, // Empty update
		{ChatJoinRequest: &ChatJoinRequest{Chat: Chat{ID: 1}}},
		{ChatBoost: &ChatBoostUpdated{Chat: Chat{ID: 2}}},
		{RemovedChatBoost: &ChatBoostRemoved{Chat: Chat{ID: 3}}},
		{Message: &Message{Chat: Chat{ID: 4}}},
		{EditedMessage: &Message{Chat: Chat{ID: 5}}},
		{ChannelPost: &Message{Chat: Chat{ID: 6}}},
		{EditedChannelPost: &Message{Chat: Chat{ID: 7}}},
		{BusinessConnection: &BusinessConnection{User: User{ID: 8}}},
		{BusinessMessage: &Message{Chat: Chat{ID: 9}}},
		{EditedBusinessMessage: &Message{Chat: Chat{ID: 10}}},
		{DeletedBusinessMessages: &BusinessMessagesDeleted{Chat: Chat{ID: 11}}},
		{MessageReaction: &MessageReactionUpdated{Chat: Chat{ID: 12}}},
		{MessageReactionCount: &MessageReactionCountUpdated{Chat: Chat{ID: 13}}},
		{InlineQuery: &InlineQuery{From: &User{ID: 14}}},
		{ChosenInlineResult: &ChosenInlineResult{From: &User{ID: 15}}},
		{CallbackQuery: &CallbackQuery{Message: &Message{Chat: Chat{ID: 16}}}},
		{ShippingQuery: &ShippingQuery{From: User{ID: 17}}},
		{PreCheckoutQuery: &PreCheckoutQuery{From: User{ID: 18}}},
		{PollAnswer: &PollAnswer{User: &User{ID: 19}}},
		{MyChatMember: &ChatMemberUpdated{Chat: Chat{ID: 20}}},
		{ChatMember: &ChatMemberUpdated{Chat: Chat{ID: 21}}},
	}

	for i, update := range updateTypes {
		select {
		case dsp.updateChan <- &update:
		case <-ctx.Done():
			t.Fatalf("Timed out while sending update %d", i)
		}
	}

	// Wait for all updates to be processed
	time.Sleep(100 * time.Millisecond)

	if mockBot.updateCount != len(updateTypes) {
		t.Fatalf("Expected %d updates, but processed %d", len(updateTypes), mockBot.updateCount)
	}
}

func TestDispatcherRateLimiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockBot := &MockBot{}
	d, err := NewDispatcher(token, func(chatId int64) Bot { return mockBot },
		WithRateLimit(rate.Every(time.Minute/20), 2)) // 20 per minute, burst of 2
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer d.Stop(ctx)

	update := &Update{Message: &Message{Chat: Chat{ID: 1}}}

	start := time.Now()
	for i := 0; i < 10; i++ {
		d.updateChan <- update
		time.Sleep(50 * time.Millisecond) // Small delay to simulate incoming updates
	}

	// Wait a bit to ensure all updates have been processed
	time.Sleep(100 * time.Millisecond)

	elapsed := time.Since(start)
	t.Logf("Time elapsed: %v", elapsed)
	t.Logf("Updates processed: %d", mockBot.updateCount)

	// Expected updates: 2 (initial burst)
	expected := 2
	if mockBot.updateCount != expected {
		t.Errorf("Expected exactly %d updates processed (initial burst), got %d", expected, mockBot.updateCount)
	}

	// Now wait for 3 seconds (the rate limit period) and send one more update
	time.Sleep(3 * time.Second)
	d.updateChan <- update
	time.Sleep(100 * time.Millisecond) // Wait a bit for processing

	t.Logf("Updates processed after waiting: %d", mockBot.updateCount)

	// Expected updates: 2 (initial burst) + 1 (after waiting) = 3
	expected = 3
	if mockBot.updateCount != expected {
		t.Errorf("Expected exactly %d updates processed (initial burst + 1), got %d", expected, mockBot.updateCount)
	}
}
