package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wwlogs/internal/instance"
)

// launchRecorder replaces the system parts of a launch and records what the launch reached.
type launchRecorder struct {
	acquired, ran, notified int
	outcome                 instance.Outcome
	activation              *instanceActivation
}

func (r *launchRecorder) launcher(claim *instance.Claim) launcher {
	return launcher{
		acquire: func() *instance.Claim { r.acquired++; return claim },
		run: func(_ Options, activation *instanceActivation) error {
			r.ran++
			r.activation = activation
			return nil
		},
		notify: func(outcome instance.Outcome, _ Options) { r.notified++; r.outcome = outcome },
	}
}

func testComposition() Composition {
	return Composition{ParseOptions: ParseOptions, PrintVersion: func(w io.Writer) { fmt.Fprintln(w, "WWlogs test") }}
}

// A launch while WWlogs runs hands off and exits: run, which opens settings, history, capture and windows, is never
// reached, and nothing is shown.
func TestLaterLaunchStartsNothing(t *testing.T) {
	for _, reply := range []instance.Reply{instance.Activated, instance.Flashed, instance.Pending} {
		var recorder launchRecorder
		code := recorder.launcher(&instance.Claim{Outcome: instance.HandedOff, Reply: reply}).launch(testComposition(), nil, io.Discard, io.Discard)
		if code != 0 || recorder.acquired != 1 || recorder.ran != 0 || recorder.notified != 0 {
			t.Fatalf("reply %q: exit %d, acquired %d, ran %d, notified %d", reply, code, recorder.acquired, recorder.ran, recorder.notified)
		}
	}
}

// A launch that could neither start nor hand off says why in the user's language and starts nothing.
func TestLaunchThatCannotHandOffSaysWhyAndStartsNothing(t *testing.T) {
	for _, outcome := range []instance.Outcome{instance.RunningElsewhere, instance.NotResponding, instance.CheckFailed} {
		var recorder launchRecorder
		var stderr bytes.Buffer
		code := recorder.launcher(&instance.Claim{Outcome: outcome, Err: errors.New("injected")}).launch(testComposition(), nil, io.Discard, &stderr)
		if code != 1 || recorder.ran != 0 || recorder.notified != 1 || recorder.outcome != outcome {
			t.Fatalf("%s: exit %d, ran %d, notified %d (%s)", outcome, code, recorder.ran, recorder.notified, recorder.outcome)
		}
		if !strings.Contains(stderr.String(), "did not start") || !strings.Contains(stderr.String(), "injected") {
			t.Fatalf("%s: diagnostic %q", outcome, stderr.String())
		}
	}
}

// -version, -help and a command line error touch no shared state, so they answer without claiming the instance, also
// while another WWlogs runs.
func TestVersionHelpAndUsageErrorsClaimNothing(t *testing.T) {
	for _, arguments := range [][]string{{"-version"}, {"--version"}, {"-h"}, {"-no-such-flag"}} {
		var recorder launchRecorder
		code := recorder.launcher(nil).launch(testComposition(), arguments, io.Discard, io.Discard)
		want := 0
		if arguments[0] == "-no-such-flag" {
			want = 2
		}
		if code != want || recorder.acquired != 0 || recorder.ran != 0 {
			t.Fatalf("%v: exit %d, acquired %d, ran %d", arguments, code, recorder.acquired, recorder.ran)
		}
	}
}

func TestFirstLaunchRunsWithTheActivation(t *testing.T) {
	var recorder launchRecorder
	var stderr bytes.Buffer
	code := recorder.launcher(&instance.Claim{Outcome: instance.Primary}).launch(testComposition(), nil, io.Discard, &stderr)
	if code != 0 || recorder.ran != 1 || recorder.activation == nil || recorder.notified != 0 {
		t.Fatalf("exit %d, ran %d, activation %v", code, recorder.ran, recorder.activation != nil)
	}
	// This claim holds no lock, so serving fails; the run goes on and says so.
	if !strings.Contains(stderr.String(), "cannot bring this window back") {
		t.Fatalf("diagnostic %q", stderr.String())
	}
}

func refuseToBring(t *testing.T) func(*application.WebviewWindow) instance.Reply {
	return func(*application.WebviewWindow) instance.Reply {
		t.Error("the window was changed")
		return instance.Activated
	}
}

// A request during startup waits for the Desktop window to be on screen; if that takes longer than the request may
// wait, it answers Pending without touching the window, which opens by itself. Whether the window is on screen is read
// from the window, so a window that became visible without a show event (a remembered maximized one) is not missed.
func TestStartingInstanceAnswersOnceItsWindowIsOnScreen(t *testing.T) {
	activation := newInstanceActivation()
	activation.bring = refuseToBring(t)
	var onScreen atomic.Bool
	activation.visible = func(*application.WebviewWindow) bool { return onScreen.Load() }
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if reply := activation.answer(short); reply != instance.Pending {
		t.Fatalf("before the window exists: %q", reply)
	}
	activation.attach(&application.WebviewWindow{}, func() bool { return false })
	shortAgain, cancelAgain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAgain()
	if reply := activation.answer(shortAgain); reply != instance.Pending {
		t.Fatalf("before the window is on screen: %q", reply)
	}
	var brought atomic.Int32
	activation.bring = func(*application.WebviewWindow) instance.Reply { brought.Add(1); return instance.Activated }
	go func() {
		time.Sleep(100 * time.Millisecond)
		onScreen.Store(true)
	}()
	long, cancelLong := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLong()
	start := time.Now()
	if reply := activation.answer(long); reply != instance.Activated || brought.Load() != 1 {
		t.Fatalf("once on screen: %q, brought %d", reply, brought.Load())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("answered %s after the window came on screen", elapsed)
	}
	// Once seen, later requests do not wait again.
	onScreen.Store(false)
	if reply := activation.answer(long); reply != instance.Activated || brought.Load() != 2 {
		t.Fatalf("a later request: %q, brought %d", reply, brought.Load())
	}
}

// A closing instance answers Closing and leaves its window alone, so the later launch waits for it and then starts.
func TestClosingInstanceAnswersClosing(t *testing.T) {
	closing := newInstanceActivation()
	closing.bring = refuseToBring(t)
	closing.closing = func() bool { return true }
	closing.shown.Store(true)
	if reply := closing.answer(context.Background()); reply != instance.Closing {
		t.Fatalf("while closing: %q", reply)
	}
	// Released while a request still waits for a starting window: the instance is going away.
	starting := newInstanceActivation()
	starting.bring = refuseToBring(t)
	released, cancel := context.WithCancel(context.Background())
	cancel()
	if reply := starting.answer(released); reply != instance.Closing {
		t.Fatalf("released while starting: %q", reply)
	}
}

// A window change the UI thread has not run in time gives no answer, and the next request waits for that same change
// rather than queueing another.
func TestOneWindowChangeAtATime(t *testing.T) {
	activation := newInstanceActivation()
	activation.shown.Store(true)
	release := make(chan struct{})
	var calls atomic.Int32
	activation.bring = func(*application.WebviewWindow) instance.Reply {
		calls.Add(1)
		<-release
		return instance.Flashed
	}
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if reply := activation.answer(short); reply != instance.NoReply {
		t.Fatalf("UI thread busy: %q", reply)
	}
	answered := make(chan instance.Reply, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		answered <- activation.answer(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if reply := <-answered; reply != instance.Flashed || calls.Load() != 1 {
		t.Fatalf("second request: %q after %d window changes", reply, calls.Load())
	}
}
