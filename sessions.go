package xeo

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Session Manager
type sessionManager struct {
	sessions sync.Map
	locks    sync.Map
	ttl      time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
}

type session struct {
	bot          Bot
	lastAccessed atomic.Int64
}

func newSessionManager(ttl time.Duration) *sessionManager {
	ctx, cancel := context.WithCancel(context.Background())
	sm := &sessionManager{
		ttl:    ttl,
		ctx:    ctx,
		cancel: cancel,
	}
	go sm.periodicCleanup()
	return sm
}

// GetSession retrieves an existing session or creates a new one if it doesn't exist.
func (sm *sessionManager) getSession(chatID int64, newBot NewBotFn) (bot Bot, err error) {
	lock, _ := sm.locks.LoadOrStore(chatID, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	if value, loaded := sm.sessions.Load(chatID); loaded {
		if s, ok := value.(*session); ok {
			s.lastAccessed.Store(time.Now().UnixNano())
			return s.bot, nil
		}
		// Remove invalid session
		sm.delete(chatID)
		return nil, fmt.Errorf("invalid session type for chat ID %d", chatID)
	}

	// Use a defer to handle potential panics from newBot
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in newBot: %v", r)
		}
	}()

	bot = newBot(chatID)
	newSession := &session{
		bot: bot,
	}
	newSession.lastAccessed.Store(time.Now().UnixNano())
	sm.sessions.Store(chatID, newSession)

	return bot, nil
}

func (sm *sessionManager) delete(chatID int64) {
	sm.sessions.Delete(chatID)
	sm.locks.Delete(chatID)
}

func (sm *sessionManager) add(chatID int64, newBot NewBotFn) error {
	lock, _ := sm.locks.LoadOrStore(chatID, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	now := time.Now().UnixNano()
	newSession := &session{
		bot: newBot(chatID),
	}
	newSession.lastAccessed.Store(now)

	_, loaded := sm.sessions.LoadOrStore(chatID, newSession)
	if loaded {
		return fmt.Errorf("session for chat ID %d already exists", chatID)
	}
	return nil
}

func (sm *sessionManager) periodicCleanup() {
	ticker := time.NewTicker(sm.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.cleanup()
		case <-sm.ctx.Done():
			return
		}
	}
}

func (sm *sessionManager) cleanup() {
	now := time.Now().UnixNano()
	sm.sessions.Range(func(key, value interface{}) bool {
		s := value.(*session)
		if now-s.lastAccessed.Load() > sm.ttl.Nanoseconds() {
			sm.delete(key.(int64))
		}
		return true
	})
}

func (sm *sessionManager) Stop(ctx context.Context) error {
	sm.cancel() // Cancel the internal context to stop the cleanup goroutine

	// Wait for the cleanup goroutine to finish or for the provided context to be done
	select {
	case <-sm.ctx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sm *sessionManager) getActiveSessions() []int64 {
	var activeSessions []int64
	sm.sessions.Range(func(key, value interface{}) bool {
		activeSessions = append(activeSessions, key.(int64))
		return true
	})
	return activeSessions
}

func (sm *sessionManager) updateTTL(newTTL time.Duration) {
	sm.ttl = newTTL
}
