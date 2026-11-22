// Package instance keeps the WWlogs desktop application to one running instance for each Windows user, and lets a later
// launch hand its request to that instance instead of starting a second one.
//
// The lock is a named mutex in the Global namespace, named after the user's SID: every Windows session of the user shares
// it, as they share the user's settings and Combat History under %LOCALAPPDATA%, and every other user has a lock of their
// own. Its security descriptor makes the user its owner and grants it to the user and SYSTEM only, so another account can
// neither take nor wait on it, and a lock the user does not own is refused. A dedicated OS thread owns the mutex for the
// life of the process. Windows ends that ownership with the thread or the process, so a crashed or killed instance leaves
// no lock behind: the next launch receives the abandoned mutex and starts.
//
// A launch that finds the lock held asks the running instance, over a named pipe of the same user, to bring its window
// back, the only request there is. It never starts a second instance: it waits for an instance that is closing to release
// the lock, and reports NotResponding when the running instance does not answer in time.
package instance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// Outcome is what Acquire found.
type Outcome int

const (
	// Primary: this process holds the lock. It starts the application and serves later launches.
	Primary Outcome = iota
	// HandedOff: the running instance accepted the request. This process exits without starting anything.
	HandedOff
	// RunningElsewhere: the running instance is in another Windows session or on another desktop of this user. Its window
	// was left alone, since bringing it forward would cross that boundary.
	RunningElsewhere
	// NotResponding: an instance holds the lock but did not answer, or did not finish closing, in time.
	NotResponding
	// CheckFailed: the lock could not be created, verified or waited on.
	CheckFailed
)

func (o Outcome) String() string {
	switch o {
	case Primary:
		return "primary"
	case HandedOff:
		return "handed off"
	case RunningElsewhere:
		return "running in another session or desktop"
	case NotResponding:
		return "the running instance is not responding"
	case CheckFailed:
		return "the single-instance check failed"
	}
	return fmt.Sprintf("outcome %d", int(o))
}

// Reply is the running instance's answer to a later launch.
type Reply byte

const (
	// NoReply: the running instance could not answer in time. The later launch asks again until its time runs out.
	NoReply Reply = 0
	// Activated: the window is in the foreground, or already was.
	Activated Reply = 'A'
	// Flashed: the window was brought back, but Windows kept the foreground elsewhere, so its taskbar button flashes.
	Flashed Reply = 'F'
	// Pending: the instance is still starting; its window opens when it is ready.
	Pending Reply = 'P'
	// Closing: the instance is closing. The later launch waits for the lock and then starts.
	Closing Reply = 'C'
	// Elsewhere: the request came from another Windows session or desktop. Nothing was changed.
	Elsewhere Reply = 'E'
)

// Handler answers a request to bring the window back. The pipe server calls it only for launches in its own session and
// on its own desktop, one at a time, while the later launch waits for the answer. Its context ends after answerLimit, or
// earlier when the instance releases its claim.
type Handler func(ctx context.Context) Reply

// answerLimit bounds a Handler. A later launch waits for one answer a little longer than this.
const answerLimit = 12 * time.Second

// limits bound every wait of a later launch.
type limits struct {
	// answer bounds reaching the running instance and getting an answer, every attempt included.
	answer time.Duration
	// closing bounds the wait for an instance that answered Closing to release the lock.
	closing time.Duration
	// reply bounds one request's wait for its answer.
	reply time.Duration
	// retry is the pause between attempts, and the slice in which the lock thread waits for the lock.
	retry time.Duration
}

// productLimits: an instance serves its pipe within milliseconds of taking the lock, so 20 seconds without an answer
// means it is stuck; closing takes at most the capture shutdown limit of the desktop host (15 seconds) and the window
// teardown after it.
var productLimits = limits{answer: 20 * time.Second, closing: 30 * time.Second, reply: answerLimit + 3*time.Second, retry: 100 * time.Millisecond}

// Claim is the result of Acquire. A Primary claim holds the lock until Release.
type Claim struct {
	Outcome Outcome
	// Reply is the running instance's answer when Outcome is HandedOff.
	Reply Reply
	// Err describes a NotResponding or CheckFailed outcome, for diagnostics.
	Err error

	id     identity
	sa     *windows.SecurityAttributes
	holder *holder
	server *server
}

// Acquire claims the single instance of this Windows user. It returns Primary once this process holds the lock, or else
// the outcome of handing the launch to the instance that holds it. It never returns Primary while another process holds
// the lock, and every wait in it is bounded.
func Acquire() *Claim {
	user, err := currentUser()
	if err != nil {
		return &Claim{Outcome: CheckFailed, Err: err}
	}
	return claim(identityFor(objectPrefix, user), productLimits)
}

// Serve answers later launches with handler until Release. Only a Primary claim serves. The lock is held whether or not
// serving works, so a failure here only means later launches cannot bring this window back.
func (c *Claim) Serve(handler Handler) error {
	if c.Outcome != Primary || c.holder == nil {
		return errors.New("only the running instance serves later launches")
	}
	if c.server != nil {
		return errors.New("later launches are already served")
	}
	server, err := startServer(c.id, c.sa, handler)
	if err != nil {
		return err
	}
	c.server = server
	return nil
}

// Release stops serving and then releases the lock, so the next instance never finds the old pipe once it holds the
// lock. The application must have closed its shared resources before.
func (c *Claim) Release() {
	if c.server != nil {
		c.server.close()
		c.server = nil
	}
	if c.holder != nil {
		c.holder.release()
		c.holder = nil
	}
}

func claim(id identity, l limits) *Claim {
	sa, err := securityAttributes(id.user)
	if err != nil {
		return &Claim{Outcome: CheckFailed, Err: err}
	}
	h := startHolder(id, sa, l.retry)
	event := <-h.events
	if event.state != lockHeld {
		return h.claimed(event, id, sa)
	}
	result := handOff(id, h, l)
	if result.Outcome == Primary {
		result.id, result.sa, result.holder = id, sa, h
	} else {
		h.release()
	}
	return result
}

// claimed turns the lock thread's final event into a claim: the lock, or the failure to take it.
func (h *holder) claimed(event lockEvent, id identity, sa *windows.SecurityAttributes) *Claim {
	if event.state == lockAcquired {
		return &Claim{Outcome: Primary, id: id, sa: sa, holder: h}
	}
	h.release()
	return &Claim{Outcome: CheckFailed, Err: event.err}
}

// handOff asks the instance that holds the lock to bring its window back. When that instance ends meanwhile, the lock
// thread takes the lock and this launch becomes the primary instance instead.
func handOff(id identity, h *holder, l limits) *Claim {
	deadline := time.Now().Add(l.answer)
	desktop := currentDesktop()
	problem := errors.New("the running instance did not answer")
	for {
		select {
		case event := <-h.events:
			return lockOutcome(event)
		default:
		}
		if !time.Now().Before(deadline) {
			return &Claim{Outcome: NotResponding, Err: problem}
		}
		reply, err := ask(id, desktop, deadline, l.reply)
		switch {
		case err != nil:
			problem = err
		case reply == Closing:
			return waitForClose(h, l.closing)
		case reply == Elsewhere:
			return &Claim{Outcome: RunningElsewhere, Reply: reply}
		case reply == NoReply:
			problem = errors.New("the running instance could not answer in time")
		default:
			return &Claim{Outcome: HandedOff, Reply: reply}
		}
		timer := time.NewTimer(l.retry)
		select {
		case event := <-h.events:
			timer.Stop()
			return lockOutcome(event)
		case <-timer.C:
		}
	}
}

// lockOutcome is a lock event that arrives while handing off: the running instance has ended and this launch holds the
// lock, or waiting for it failed. The caller fills in the lock of a Primary claim.
func lockOutcome(event lockEvent) *Claim {
	if event.state == lockAcquired {
		return &Claim{Outcome: Primary}
	}
	return &Claim{Outcome: CheckFailed, Err: event.err}
}

// waitForClose waits for an instance that is closing to release the lock, which it does as its last step.
func waitForClose(h *holder, limit time.Duration) *Claim {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case event := <-h.events:
		return lockOutcome(event)
	case <-timer.C:
		return &Claim{Outcome: NotResponding, Err: errors.New("the running instance did not finish closing")}
	}
}
