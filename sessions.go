package xeo

import (
	"context"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"sync"
	"sync/atomic"
	"time"
)

// Session Manager
type sessionManager struct {
	sessions        sync.Map
	locks           sync.Map
	ttl             time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	cleanupCallback func(int64)
	logger          zerolog.Logger
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
		logger: log.With().Str("component", "session_manager").Logger(),
	}
	sm.logger.Info().Dur("ttl", ttl).Msg("Session manager initialized")
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
			sm.logger.Debug().Int64("chat_id", chatID).Msg("Retrieved existing session")
			return s.bot, nil
		}
		sm.delete(chatID)
		sm.logger.Error().Int64("chat_id", chatID).Msg("Invalid session type found and removed")
		return nil, fmt.Errorf("invalid session type for chat ID %d", chatID)
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in newBot: %v", r)
			sm.logger.Error().Int64("chat_id", chatID).Interface("panic", r).Msg("Panic in newBot")
		}
	}()

	bot = newBot(chatID)
	newSession := &session{
		bot: bot,
	}
	newSession.lastAccessed.Store(time.Now().UnixNano())
	sm.sessions.Store(chatID, newSession)
	sm.logger.Debug().Int64("chat_id", chatID).Msg("Created new session")

	return bot, nil
}

func (sm *sessionManager) delete(chatID int64) {
	sm.sessions.Delete(chatID)
	sm.locks.Delete(chatID)
	sm.logger.Debug().Int64("chat_id", chatID).Msg("Deleted session")
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
		sm.logger.Warn().Int64("chat_id", chatID).Msg("Attempted to add existing session")
		return fmt.Errorf("session for chat ID %d already exists", chatID)
	}
	sm.logger.Debug().Int64("chat_id", chatID).Msg("Added new session")
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

func (sm *sessionManager) HasSession(chatID int64) bool {
	_, exists := sm.sessions.Load(chatID)
	return exists
}

// Modify cleanup to also handle rate limiters
func (sm *sessionManager) cleanup() {
	now := time.Now().UnixNano()
	expiredCount := 0
	sm.sessions.Range(func(key, value interface{}) bool {
		s := value.(*session)
		chatID := key.(int64)
		if now-s.lastAccessed.Load() > sm.ttl.Nanoseconds() {
			sm.delete(chatID)
			if sm.cleanupCallback != nil {
				sm.cleanupCallback(chatID)
			}
			expiredCount++
		}
		return true
	})
	if expiredCount > 0 {
		sm.logger.Info().Int("expired_count", expiredCount).Msg("Cleaned up expired sessions")
	}
}

func (sm *sessionManager) SetCleanupCallback(cb func(int64)) {
	sm.cleanupCallback = cb
}

func (sm *sessionManager) Stop(ctx context.Context) error {
	sm.logger.Info().Msg("Stopping session manager")
	sm.cancel()

	select {
	case <-sm.ctx.Done():
		sm.logger.Info().Msg("Session manager stopped successfully")
		return nil
	case <-ctx.Done():
		sm.logger.Warn().Err(ctx.Err()).Msg("Context deadline exceeded while stopping")
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
	sm.logger.Info().Dur("old_ttl", sm.ttl).Dur("new_ttl", newTTL).Msg("Updating session TTL")
	sm.ttl = newTTL
}
