package xeo

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrDispatcherShutdown   = errors.New("dispatcher is shutting down")
	ErrDispatcherRunning    = errors.New("dispatcher is already running")
	ErrDispatcherNotRunning = errors.New("dispatcher is not running")
	ErrNilUpdate            = errors.New("received nil update")
	ErrNilBot               = errors.New("got nil bot instance")
	ErrInvalidParam         = errors.New("invalid parameter")
)

type Bot interface {
	Update(*Update)
}

type NewBotFn func(chatId int64) Bot

type Dispatcher struct {
	api      *API
	newBot   NewBotFn
	sessions *sessionManager
	options  dispatcherOptions

	inShutdown   atomic.Bool
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	isRunning    atomic.Bool
	updateChan   chan *Update
	wg           sync.WaitGroup
	rateLimiters sync.Map // map[int64]*rate.Limiter
	logger       zerolog.Logger
}

type dispatcherOptions struct {
	pollInterval     time.Duration
	updateBufferSize int
	sessionTTL       time.Duration
	allowedUpdates   []UpdateType
	workerSem        *semaphore.Weighted
	rateLimitEnabled bool
	rateLimit        rate.Limit
	rateLimitBurst   int
}

type DSPOption func(*dispatcherOptions) error

func WithPollInterval(interval time.Duration) DSPOption {
	return func(o *dispatcherOptions) error {
		if interval <= 0 {
			return fmt.Errorf("%w: poll interval must be positive", ErrInvalidParam)
		}
		o.pollInterval = interval
		return nil
	}
}

func WithRateLimiting(enabled bool) DSPOption {
	return func(o *dispatcherOptions) error {
		o.rateLimitEnabled = enabled
		return nil
	}
}

func WithRateLimit(limit rate.Limit, burst int) DSPOption {
	return func(o *dispatcherOptions) error {
		if burst <= 0 {
			return fmt.Errorf("%w: burst must be positive", ErrInvalidParam)
		}
		o.rateLimit = limit
		o.rateLimitBurst = burst
		return nil
	}
}

func WithUpdateBufferSize(size int) DSPOption {
	return func(o *dispatcherOptions) error {
		if size <= 0 {
			return fmt.Errorf("%w: update buffer size must be positive", ErrInvalidParam)
		}
		o.updateBufferSize = size
		return nil
	}
}

func WithSessionTTL(ttl time.Duration) DSPOption {
	return func(o *dispatcherOptions) error {
		if ttl <= 0 {
			return fmt.Errorf("%w: session TTL must be positive", ErrInvalidParam)
		}
		o.sessionTTL = ttl
		return nil
	}
}

func WithAllowedUpdates(types []UpdateType) DSPOption {
	return func(o *dispatcherOptions) error {
		o.allowedUpdates = types
		return nil
	}
}

func WithMaxConcurrentUpdates(max int64) DSPOption {
	return func(o *dispatcherOptions) error {
		if max <= 0 {
			return fmt.Errorf("%w: max concurrent updates must be positive", ErrInvalidParam)
		}
		o.workerSem = semaphore.NewWeighted(max)
		return nil
	}
}

func NewDispatcher(token string, newBot NewBotFn, opts ...DSPOption) (*Dispatcher, error) {
	logger := log.With().Str("component", "dispatcher").Logger()

	options := dispatcherOptions{
		pollInterval:     5 * time.Second,
		updateBufferSize: 1000,
		sessionTTL:       24 * time.Hour,
		allowedUpdates:   []UpdateType{},
		workerSem:        semaphore.NewWeighted(int64(runtime.NumCPU() * 2)),
		rateLimitEnabled: true,
		rateLimit:        rate.Every(time.Minute / 20),
		rateLimitBurst:   5,
	}

	for _, opt := range opts {
		if err := opt(&options); err != nil {
			logger.Error().Err(err).Msg("Failed to apply option")
			return nil, err
		}
	}

	d := &Dispatcher{
		newBot:       newBot,
		options:      options,
		sessions:     newSessionManager(options.sessionTTL),
		updateChan:   make(chan *Update, options.updateBufferSize),
		rateLimiters: sync.Map{},
		logger:       logger,
	}

	var err error
	d.api, err = NewAPI(WithToken(token))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create API")
		return nil, fmt.Errorf("failed to create API: %w", err)
	}

	d.sessions.SetCleanupCallback(func(chatID int64) {
		d.rateLimiters.Delete(chatID)
		d.logger.Debug().Int64("chat_id", chatID).Msg("Removed rate limiter for inactive user")
	})

	logger.Info().Msg("Polling!")
	return d, nil
}

func (d *Dispatcher) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.inShutdown.Load() {
		return ErrDispatcherShutdown
	}

	if !d.isRunning.CompareAndSwap(false, true) {
		return ErrDispatcherRunning
	}

	d.ctx, d.cancel = context.WithCancel(context.Background())

	g, ctx := errgroup.WithContext(d.ctx)

	g.Go(func() error {
		return d.pollUpdates(ctx)
	})

	g.Go(func() error {
		return d.worker(ctx)
	})

	go func() {
		err := g.Wait()
		if err != nil && !errors.Is(err, context.Canceled) {
			d.logger.Error().Err(err).Msg("Dispatcher error")
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer stopCancel()
			if stopErr := d.Stop(stopCtx); stopErr != nil {
				d.logger.Error().Err(stopErr).Msg("Error stopping dispatcher")
			}
		}
	}()

	return nil
}

func (d *Dispatcher) Stop(ctx context.Context) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	d.mu.Lock()
	if !d.isRunning.Load() {
		d.mu.Unlock()
		return ErrDispatcherNotRunning
	}
	if d.inShutdown.Load() {
		d.mu.Unlock()
		return ErrDispatcherShutdown
	}
	d.inShutdown.Store(true)
	d.isRunning.Store(false)
	d.cancel()
	close(d.updateChan)
	d.mu.Unlock()

	d.logger.Info().Msg("Stopping session manager")
	if err := d.sessions.Stop(ctx); err != nil {
		d.logger.Error().Err(err).Msg("Failed to stop session manager")
		return fmt.Errorf("failed to stop session manager: %w", err)
	}

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.logger.Info().Msg("Polling stopped successfully")
		return nil
	case <-ctx.Done():
		d.logger.Warn().Err(ctx.Err()).Msg("Context deadline exceeded while waiting for goroutines to finish")
		return ctx.Err()
	}
}

func (d *Dispatcher) worker(ctx context.Context) error {
	for {
		if !d.isRunning.Load() {
			return ErrDispatcherNotRunning
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case update, ok := <-d.updateChan:
			if !ok {
				return nil
			}
			if d.inShutdown.Load() {
				return ErrDispatcherShutdown
			}

			if d.options.rateLimitEnabled {
				limiter, err := d.getLimiter(update.ChatID())
				if err != nil {
					return fmt.Errorf("getting rate limiter: %w", err)
				}

				if !limiter.Allow() {
					d.logger.Warn().Int64("chat_id", update.ChatID()).Msg("Rate limit exceeded, dropping update")
					continue
				}
			}

			if err := d.options.workerSem.Acquire(ctx, 1); err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				return fmt.Errorf("failed to acquire semaphore: %w", err)
			}
			d.wg.Add(1)
			go func(update *Update) {
				defer d.options.workerSem.Release(1)
				defer d.wg.Done()
				if err := d.processUpdate(update); err != nil {
					d.logger.Error().
						Err(err).
						Int64("chat_id", update.ChatID()).
						Msg("Error processing update")
				}
			}(update)
		}
	}
}

func (d *Dispatcher) processUpdate(update *Update) error {
	if update == nil {
		return ErrNilUpdate
	}

	chatID := update.ChatID()
	bot, err := d.sessions.getSession(chatID, d.newBot)
	if err != nil {
		return fmt.Errorf("getting bot instance: %w", err)
	}

	if bot == nil {
		return ErrNilBot
	}

	bot.Update(update)
	return nil
}

// Polls Telegram for updates and queues them for processing.
func (d *Dispatcher) pollUpdates(ctx context.Context) error {
	if _, err := d.api.DeleteWebhook(true); err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}

	opts := UpdateOptions{
		Timeout:        int(d.options.pollInterval.Seconds()),
		Limit:          100,
		Offset:         0,
		AllowedUpdates: d.options.allowedUpdates,
	}

	for {
		if !d.isRunning.Load() {
			return ErrDispatcherNotRunning
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.options.pollInterval):
			if d.inShutdown.Load() {
				return ErrDispatcherShutdown
			}
			updates, err := d.api.GetUpdates(&opts)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				d.logger.Error().Err(err).Msg("Failed to get updates")
				continue
			}

			if len(updates.Result) > 0 {
				lastUpdateID := updates.Result[len(updates.Result)-1].ID
				opts.Offset = lastUpdateID + 1

				for _, update := range updates.Result {
					if update == nil {
						d.logger.Warn().Msg("Received nil update from API")
						continue
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case d.updateChan <- update:
					default:
						d.logger.Warn().
							Int("update_id", update.ID).
							Msg("Update channel is full, discarding update")
					}
				}
			}
		}
	}
}

func (d *Dispatcher) getLimiter(chatID int64) (*rate.Limiter, error) {
	value, _ := d.rateLimiters.LoadOrStore(chatID, rate.NewLimiter(d.options.rateLimit, d.options.rateLimitBurst))
	limiter, ok := value.(*rate.Limiter)
	if !ok {
		return nil, fmt.Errorf("invalid rate limiter type for chat %d", chatID)
	}
	return limiter, nil
}
