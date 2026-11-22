package desktop

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wwlogs/internal/instance"
)

// launcher is the part of a launch that reaches the system: the single-instance claim, the application and the message
// a launch that cannot start shows. Main uses the real ones; tests replace them.
type launcher struct {
	acquire func() *instance.Claim
	run     func(Options, *instanceActivation) error
	notify  func(instance.Outcome, Options)
}

var systemLauncher = launcher{acquire: instance.Acquire, run: run, notify: showStartupMessage}

// launch runs one launch and returns its exit code. -version and -help touch no shared state and answer at once. Every
// other launch claims the single instance of this Windows user before anything reads or writes settings, Combat History,
// the capture, the spool or a window: a launch while WWlogs runs, of either build profile and from any location, brings
// that instance's window back and exits having started nothing.
func (l launcher) launch(composition Composition, arguments []string, stdout, stderr io.Writer) int {
	for _, argument := range arguments {
		if argument == "-version" || argument == "--version" {
			composition.PrintVersion(stdout)
			return 0
		}
	}
	options, err := composition.ParseOptions(arguments, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "WWlogs desktop: %v\n", err)
		return 2
	}
	claim := l.acquire()
	switch claim.Outcome {
	case instance.Primary:
	case instance.HandedOff:
		return 0
	default:
		fmt.Fprintf(stderr, "WWlogs desktop: did not start: %s: %v\n", claim.Outcome, claim.Err)
		l.notify(claim.Outcome, options)
		return 1
	}
	// Released after the application has closed its history, capture and windows, so the next instance finds them closed.
	defer claim.Release()
	activation := newInstanceActivation()
	if err := claim.Serve(activation.answer); err != nil {
		// The lock still keeps this the only instance; later launches only cannot bring this window back.
		fmt.Fprintf(stderr, "WWlogs desktop: later launches cannot bring this window back: %v\n", err)
	}
	if err := l.run(options, activation); err != nil {
		fmt.Fprintf(stderr, "WWlogs desktop: %v\n", err)
		return 1
	}
	return 0
}

// shownCheckInterval is how often a request that arrives while the instance starts looks at its window again.
const shownCheckInterval = 50 * time.Millisecond

// instanceActivation answers a later launch of WWlogs by bringing this instance's window back. launch creates it before
// the application starts, so a launch during startup already gets an answer; run attaches the window once it exists.
type instanceActivation struct {
	// visible tells whether the window is on screen, and bring changes it on the UI thread; tests replace both.
	visible func(*application.WebviewWindow) bool
	bring   func(*application.WebviewWindow) instance.Reply
	// shown is set once the window has been seen on screen.
	shown atomic.Bool

	mu      sync.Mutex
	window  *application.WebviewWindow
	closing func() bool
	// inflight is the window change still running, if any; a later request waits for it instead of starting another.
	inflight chan instance.Reply
}

func newInstanceActivation() *instanceActivation {
	return &instanceActivation{visible: windowVisible, bring: bringWindowBack}
}

// attach sets the window to bring back and the test for an application that is closing, which a later launch waits for.
func (a *instanceActivation) attach(window *application.WebviewWindow, closing func() bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.window, a.closing = window, closing
	a.mu.Unlock()
}

// answer is the instance.Handler. A starting instance answers once its window is on screen, or Pending when that takes
// longer than the request may wait: the window then opens by itself. A closing instance answers Closing, and the later
// launch waits for it to end and then starts.
func (a *instanceActivation) answer(ctx context.Context) instance.Reply {
	if !a.waitShown(ctx) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return instance.Pending
		}
		return instance.Closing
	}
	a.mu.Lock()
	window, closing := a.window, a.closing
	a.mu.Unlock()
	if closing != nil && closing() {
		return instance.Closing
	}
	return a.bringBack(ctx, window)
}

// waitShown waits until the window is on screen. The Desktop window is created hidden and shown once it has been placed,
// so a starting instance is not asked to bring back a window that is not there yet. It looks at the window itself
// rather than waiting for a show event: a remembered maximized window, which the keeper maximizes while it is still
// hidden, came on screen without a show event reaching this host, and a missed event would leave every later launch
// waiting. The check repeats only while a later launch waits for a starting instance.
func (a *instanceActivation) waitShown(ctx context.Context) bool {
	ticker := time.NewTicker(shownCheckInterval)
	defer ticker.Stop()
	for {
		if a.shown.Load() {
			return true
		}
		a.mu.Lock()
		window := a.window
		a.mu.Unlock()
		if window != nil && a.visible(window) {
			a.shown.Store(true)
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// bringBack runs the window change once at a time. When the UI thread does not run it before the request's time is up,
// there is no answer: the later launch asks again and, if this instance stays unresponsive, says so.
func (a *instanceActivation) bringBack(ctx context.Context, window *application.WebviewWindow) instance.Reply {
	a.mu.Lock()
	if a.inflight == nil {
		result := make(chan instance.Reply, 1)
		a.inflight = result
		go func() {
			reply := a.bring(window)
			a.mu.Lock()
			a.inflight = nil
			a.mu.Unlock()
			result <- reply
		}()
	}
	inflight := a.inflight
	a.mu.Unlock()
	select {
	case reply := <-inflight:
		return reply
	case <-ctx.Done():
		return instance.NoReply
	}
}
