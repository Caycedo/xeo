package xeo

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
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
			return errors.New("poll interval must be positive")
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
		o.rateLimit = limit
		o.rateLimitBurst = burst
		return nil
	}
}

func WithUpdateBufferSize(size int) DSPOption {
	return func(o *dispatcherOptions) error {
		if size <= 0 {
			return errors.New("update buffer size must be positive")
		}
		o.updateBufferSize = size
		return nil
	}
}

func WithSessionTTL(ttl time.Duration) DSPOption {
	return func(o *dispatcherOptions) error {
		if ttl <= 0 {
			return errors.New("session TTL must be positive")
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
			return errors.New("max concurrent updates must be positive")
		}
		o.workerSem = semaphore.NewWeighted(max)
		return nil
	}
}

func NewDispatcher(token string, newBot NewBotFn, opts ...DSPOption) (*Dispatcher, error) {
	options := dispatcherOptions{
		pollInterval:     5 * time.Second,
		updateBufferSize: 1000,
		sessionTTL:       24 * time.Hour,
		allowedUpdates:   []UpdateType{},
		workerSem:        semaphore.NewWeighted(int64(runtime.NumCPU() * 2)),

		rateLimitEnabled: true,
		rateLimit:        rate.Every(time.Minute / 20), // Default: 20 per minute
		rateLimitBurst:   5,
	}

	for _, opt := range opts {
		if err := opt(&options); err != nil {
			return nil, err
		}
	}

	d := &Dispatcher{
		newBot:       newBot,
		options:      options,
		sessions:     newSessionManager(options.sessionTTL),
		updateChan:   make(chan *Update, options.updateBufferSize),
		rateLimiters: sync.Map{},
	}

	var err error
	d.api, err = NewAPI(WithToken(token))
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Dispatcher) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.inShutdown.Load() {
		return errors.New("dispatcher is shutting down")
	}

	if !d.isRunning.CompareAndSwap(false, true) {
		return errors.New("dispatcher is already running")
	}

	d.ctx, d.cancel = context.WithCancel(context.Background())

	g, ctx := errgroup.WithContext(d.ctx)

	g.Go(func() error {
		return d.pollUpdates(ctx)
	})

	g.Go(func() error {
		return d.worker(ctx)
	})

	// Start a goroutine to wait for errors
	go func() {
		err := g.Wait()
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Printf("Dispatcher error: %v\n", err)
			if stopErr := d.Stop(context.Background()); stopErr != nil {
				fmt.Printf("Error stopping dispatcher: %v\n", stopErr)
			}
		}
	}()

	return nil
}

func (d *Dispatcher) Stop(ctx context.Context) error {
	if !d.isRunning.CompareAndSwap(true, false) {
		return errors.New("dispatcher is not running")
	}

	d.mu.Lock()
	if d.inShutdown.Swap(true) {
		d.mu.Unlock()
		return errors.New("dispatcher is already shutting down")
	}
	d.cancel()
	close(d.updateChan)
	d.mu.Unlock()

	// Stop the session manager
	if err := d.sessions.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop session manager: %w", err)
	}

	// Use a channel to signal when all goroutines have finished
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) worker(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update, ok := <-d.updateChan:
			if !ok {
				return nil // Channel closed, exit gracefully
			}
			if d.inShutdown.Load() {
				continue // Discard updates during shutdown
			}

			if d.options.rateLimitEnabled {
				limiter, err := d.getLimiter(update.ChatID())
				if err != nil {
					return fmt.Errorf("getting rate limiter: %w", err)
				}

				if !limiter.Allow() {
					// Drop the update if the rate limit is exceeded
					fmt.Printf("Rate limit exceeded for chat %d, dropping update\n", update.ChatID())
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
					fmt.Printf("Error processing update: %v\n", err)
				}
			}(update)
		}
	}
}

func (d *Dispatcher) processUpdate(update *Update) error {
	bot, err := d.sessions.getSession(update.ChatID(), d.newBot)
	if err != nil {
		return fmt.Errorf("getting bot instance: %w", err)
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

	ticker := time.NewTicker(d.options.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if d.inShutdown.Load() {
				return nil
			}
			updates, err := d.api.GetUpdates(&opts)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				fmt.Printf("Failed to get updates: %v\n", err)
				continue
			}

			if len(updates.Result) > 0 {
				lastUpdateID := updates.Result[len(updates.Result)-1].ID
				opts.Offset = lastUpdateID + 1

				for _, update := range updates.Result {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case d.updateChan <- update:
					default:
						fmt.Println("Update channel is full, discarding update")
					}
				}
			}
		}
	}
}

func (d *Dispatcher) getLimiter(chatID int64) (*rate.Limiter, error) {
	limiter, _ := d.rateLimiters.LoadOrStore(chatID, rate.NewLimiter(d.options.rateLimit, d.options.rateLimitBurst))
	return limiter.(*rate.Limiter), nil
}
