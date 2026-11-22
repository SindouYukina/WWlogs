package instance

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Launches in separate processes, as WWlogs.exe runs them: each is this test binary running TestHelperLaunch.

const (
	helperVariable = "WWLOGS_INSTANCE_HELPER"
	// helperReplyVariable sets the helper instance's answer; Activated without it.
	helperReplyVariable = "WWLOGS_INSTANCE_HELPER_REPLY"
)

var helperLimits = limits{answer: 15 * time.Second, closing: 10 * time.Second, reply: 5 * time.Second, retry: 20 * time.Millisecond}

// TestHelperLaunch is one launch for the tests below: it claims the lock named by WWLOGS_INSTANCE_HELPER and prints its
// outcome. The primary instance then answers later launches until its standard input closes, and prints how many it
// answered.
func TestHelperLaunch(t *testing.T) {
	prefix := os.Getenv(helperVariable)
	if prefix == "" {
		t.Skip("helper process only")
	}
	user, err := currentUser()
	if err != nil {
		fmt.Println("helper: error", err)
		os.Exit(2)
	}
	claimed := claim(identityFor(prefix, user), helperLimits)
	fmt.Println("helper:", claimed.Outcome)
	if claimed.Outcome != Primary {
		os.Exit(0)
	}
	reply := Activated
	if value := os.Getenv(helperReplyVariable); value != "" {
		reply = Reply(value[0])
	}
	var asked atomic.Int32
	if err := claimed.Serve(func(context.Context) Reply { asked.Add(1); return reply }); err != nil {
		fmt.Println("helper: error", err)
		os.Exit(2)
	}
	fmt.Println("helper: serving")
	io.Copy(io.Discard, os.Stdin)
	claimed.Release()
	fmt.Println("helper: answered", asked.Load())
	os.Exit(0)
}

type launch struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *bufio.Scanner
}

func startLaunch(t *testing.T, prefix string, environment ...string) *launch {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperLaunch$", "-test.count=1")
	cmd.Env = append(append(os.Environ(), helperVariable+"="+prefix), environment...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return &launch{cmd: cmd, stdin: stdin, output: bufio.NewScanner(stdout)}
}

// said returns the launch's next helper line.
func (l *launch) said() string {
	for l.output.Scan() {
		if line, ok := strings.CutPrefix(l.output.Text(), "helper: "); ok {
			return line
		}
	}
	return "(no output)"
}

// A killed instance cannot release the lock; Windows abandons it, and the next launch starts at once.
func TestKilledInstanceLeavesNoLock(t *testing.T) {
	prefix := testPrefix()
	first := startLaunch(t, prefix)
	if said := first.said(); said != Primary.String() {
		t.Fatalf("first launch: %s", said)
	}
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.cmd.Wait()
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	next := claim(identityFor(prefix, user), testLimits)
	defer next.Release()
	if next.Outcome != Primary {
		t.Fatalf("after the first instance was killed: %s (%v)", next.Outcome, next.Err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the next launch waited %s", elapsed)
	}
	var asked atomic.Int32
	if err := next.Serve(counting(Activated, &asked)); err != nil {
		t.Fatalf("the next instance cannot serve after a kill: %v", err)
	}
}

// An instance that ends without releasing the lock while a later launch waits for it, as one that crashes while it
// closes: Windows abandons the lock to the waiting launch, which takes it and starts.
func TestInstanceKilledWhileALaunchWaitsPassesTheLockOn(t *testing.T) {
	prefix := testPrefix()
	first := startLaunch(t, prefix, helperReplyVariable+"="+string(rune(Closing)))
	if said := first.said(); said != Primary.String() {
		t.Fatalf("first launch: %s", said)
	}
	if said := first.said(); said != "serving" {
		t.Fatalf("first launch: %s", said)
	}
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan *Claim, 1)
	go func() { result <- claim(identityFor(prefix, user), helperLimits) }()
	select {
	case later := <-result:
		later.Release()
		t.Fatalf("the later launch returned %s while the first instance was closing", later.Outcome)
	case <-time.After(500 * time.Millisecond):
	}
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.cmd.Wait()
	select {
	case later := <-result:
		defer later.Release()
		if later.Outcome != Primary {
			t.Fatalf("after the closing instance was killed: %s (%v)", later.Outcome, later.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting launch did not take the abandoned lock")
	}
}

// Launches started at the same moment, before any of them serves: exactly one becomes the instance, and every other one
// hands off to it.
func TestSimultaneousLaunchesStartOneInstance(t *testing.T) {
	const count = 6
	prefix := testPrefix()
	launches := make([]*launch, count)
	for i := range launches {
		launches[i] = startLaunch(t, prefix)
	}
	outcomes := make([]string, count)
	done := make(chan int, count)
	for i, l := range launches {
		go func() {
			outcomes[i] = l.said()
			done <- i
		}()
	}
	var running *launch
	for range count {
		select {
		case i := <-done:
			if outcomes[i] == Primary.String() {
				if running != nil {
					t.Fatal("two launches became the instance")
				}
				running = launches[i]
			} else if outcomes[i] != HandedOff.String() {
				t.Fatalf("launch %d: %s", i, outcomes[i])
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("launches did not finish: %q", outcomes)
		}
	}
	if running == nil {
		t.Fatalf("no launch became the instance: %q", outcomes)
	}
	for _, l := range launches {
		if l != running {
			if err := l.cmd.Wait(); err != nil {
				t.Fatalf("a launch that handed off failed: %v", err)
			}
		}
	}
	if said := running.said(); said != "serving" {
		t.Fatalf("the instance: %s", said)
	}
	running.stdin.Close()
	if said := running.said(); said != fmt.Sprintf("answered %d", count-1) {
		t.Fatalf("the instance: %s, want answered %d", said, count-1)
	}
	if err := running.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
