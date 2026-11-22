package instance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The tests use lock and pipe names of their own, so they never meet a WWlogs that runs on this machine.

var testLimits = limits{answer: 5 * time.Second, closing: 5 * time.Second, reply: 3 * time.Second, retry: 20 * time.Millisecond}

func testIdentity(t *testing.T) identity {
	t.Helper()
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	return identityFor(testPrefix(), user)
}

func testPrefix() string {
	random := make([]byte, 8)
	rand.Read(random)
	return "WWlogs.SingleInstance.Test." + hex.EncodeToString(random)
}

func primary(t *testing.T, id identity) *Claim {
	t.Helper()
	claimed := claim(id, testLimits)
	if claimed.Outcome != Primary {
		t.Fatalf("first launch: %s (%v)", claimed.Outcome, claimed.Err)
	}
	t.Cleanup(claimed.Release)
	return claimed
}

func counting(reply Reply, asked *atomic.Int32) Handler {
	return func(context.Context) Reply {
		asked.Add(1)
		return reply
	}
}

func TestObjectNamesDependOnlyOnTheUser(t *testing.T) {
	one, _ := windows.StringToSid("S-1-5-21-1000-2000-3000-1001")
	two, _ := windows.StringToSid("S-1-5-21-1000-2000-3000-1002")
	first := identityFor(objectPrefix, one)
	if first.mutex != `Global\WWlogs.SingleInstance.S-1-5-21-1000-2000-3000-1001` || first.pipe != `\\.\pipe\WWlogs.SingleInstance.S-1-5-21-1000-2000-3000-1001` {
		t.Fatalf("names %q and %q", first.mutex, first.pipe)
	}
	if again := identityFor(objectPrefix, one); again.mutex != first.mutex || again.pipe != first.pipe {
		t.Fatal("the same user got another lock")
	}
	if other := identityFor(objectPrefix, two); other.mutex == first.mutex || other.pipe == first.pipe {
		t.Fatal("two users share a lock")
	}
}

// The kernel object carries the descriptor: the user owns it, the DACL lets in the user and SYSTEM only and cannot
// inherit more, and the medium label keeps low-integrity processes from writing to it.
func TestLockIsOwnedByTheUserAndGrantedToNoOneElse(t *testing.T) {
	id := testIdentity(t)
	sa, err := securityAttributes(id.user)
	if err != nil {
		t.Fatal(err)
	}
	mutex, err := openLock(id, sa)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(mutex)
	sd, err := windows.GetSecurityInfo(mutex, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if control, _, err := sd.Control(); err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("the DACL is not protected from inheritance: %s", sd)
	}
	const mutexAllAccess = 0x1F0001
	system, _ := windows.StringToSid("S-1-5-18")
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	granted := entries(t, dacl)
	if len(granted) != 2 || !granted[0].is(0, id.user, mutexAllAccess) || !granted[1].is(0, system, mutexAllAccess) {
		t.Fatalf("DACL %s, want full access for the user and SYSTEM only", sd)
	}
	medium, _ := windows.StringToSid("S-1-16-8192")
	sacl, _, err := sd.SACL()
	if err != nil {
		t.Fatal(err)
	}
	const mandatoryLabel, noWriteUp = 0x11, 0x1
	if label := entries(t, sacl); len(label) != 1 || !label[0].is(mandatoryLabel, medium, noWriteUp) {
		t.Fatalf("label %s, want medium with no-write-up", sd)
	}
	if err := ownedBy(mutex, id.user); err != nil {
		t.Fatalf("the lock's own user: %v", err)
	}
	other, _ := windows.StringToSid("S-1-5-21-1000-2000-3000-1001")
	if err := ownedBy(mutex, other); err == nil {
		t.Fatal("another user was taken for the owner")
	}
}

type aceEntry struct {
	kind byte
	sid  *windows.SID
	mask windows.ACCESS_MASK
}

func (e aceEntry) is(kind byte, sid *windows.SID, mask windows.ACCESS_MASK) bool {
	return e.kind == kind && windows.EqualSid(e.sid, sid) && e.mask == mask
}

// entries lists an ACL's ACEs. Allowed and mandatory label ACEs share one layout: header, mask, SID.
func entries(t *testing.T, acl *windows.ACL) []aceEntry {
	t.Helper()
	if acl == nil {
		return nil
	}
	var list []aceEntry
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		list = append(list, aceEntry{kind: ace.Header.AceType, sid: (*windows.SID)(unsafe.Pointer(&ace.SidStart)), mask: ace.Mask})
	}
	return list
}

func TestFirstLaunchIsPrimaryAndLaterLaunchesHandOff(t *testing.T) {
	id := testIdentity(t)
	first := primary(t, id)
	var asked atomic.Int32
	if err := first.Serve(counting(Activated, &asked)); err != nil {
		t.Fatal(err)
	}
	if err := first.Serve(counting(Activated, &asked)); err == nil {
		t.Fatal("a claim served twice")
	}
	for launch := 1; launch <= 3; launch++ {
		later := claim(id, testLimits)
		if later.Outcome != HandedOff || later.Reply != Activated {
			t.Fatalf("launch %d: %s %q (%v)", launch, later.Outcome, later.Reply, later.Err)
		}
		if err := later.Serve(counting(Activated, &asked)); err == nil {
			t.Fatal("a launch that handed off served")
		}
		later.Release()
	}
	if asked.Load() != 3 {
		t.Fatalf("the running instance answered %d of 3 launches", asked.Load())
	}
}

func TestReleasedLockGoesToTheNextLaunch(t *testing.T) {
	id := testIdentity(t)
	first := claim(id, testLimits)
	if first.Outcome != Primary {
		t.Fatalf("first launch: %s", first.Outcome)
	}
	var asked atomic.Int32
	if err := first.Serve(counting(Activated, &asked)); err != nil {
		t.Fatal(err)
	}
	first.Release()
	first.Release()
	next := primary(t, id)
	if err := next.Serve(counting(Activated, &asked)); err != nil {
		t.Fatalf("the next instance cannot serve the name the first one left: %v", err)
	}
}

// A launch while the running instance closes waits for it and then starts, instead of giving up or starting beside it.
func TestClosingInstanceIsWaitedForThenReplaced(t *testing.T) {
	id := testIdentity(t)
	first := claim(id, testLimits)
	if first.Outcome != Primary {
		t.Fatalf("first launch: %s", first.Outcome)
	}
	if err := first.Serve(func(context.Context) Reply { return Closing }); err != nil {
		t.Fatal(err)
	}
	result := make(chan *Claim, 1)
	go func() { result <- claim(id, testLimits) }()
	select {
	case later := <-result:
		first.Release()
		t.Fatalf("the later launch returned %s while the first instance was still closing", later.Outcome)
	case <-time.After(500 * time.Millisecond):
	}
	first.Release()
	select {
	case later := <-result:
		defer later.Release()
		if later.Outcome != Primary {
			t.Fatalf("after the first instance closed: %s (%v)", later.Outcome, later.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the later launch did not take over")
	}
}

func TestClosingThatNeverEndsIsReported(t *testing.T) {
	id := testIdentity(t)
	first := primary(t, id)
	if err := first.Serve(func(context.Context) Reply { return Closing }); err != nil {
		t.Fatal(err)
	}
	limited := testLimits
	limited.closing = 300 * time.Millisecond
	if later := claim(id, limited); later.Outcome != NotResponding {
		t.Fatalf("an instance that never finishes closing: %s", later.Outcome)
	}
}

// An instance that holds the lock but never answers is reported within the limit; the later launch never starts a
// second instance.
func TestSilentInstanceIsReportedNotResponding(t *testing.T) {
	for name, serve := range map[string]func(*Claim) error{
		"no pipe":  func(*Claim) error { return nil },
		"no reply": func(c *Claim) error { return c.Serve(func(context.Context) Reply { return NoReply }) },
	} {
		t.Run(name, func(t *testing.T) {
			id := testIdentity(t)
			first := primary(t, id)
			if err := serve(first); err != nil {
				t.Fatal(err)
			}
			limited := testLimits
			limited.answer = 500 * time.Millisecond
			start := time.Now()
			later := claim(id, limited)
			if later.Outcome != NotResponding || later.Err == nil {
				t.Fatalf("%s (%v)", later.Outcome, later.Err)
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("waited %s for a 500 ms limit", elapsed)
			}
		})
	}
}

func TestLaunchFromAnotherDesktopIsLeftAlone(t *testing.T) {
	id := testIdentity(t)
	first := primary(t, id)
	var asked atomic.Int32
	if err := first.Serve(counting(Activated, &asked)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	if reply, err := ask(id, `WinSta0\AnotherDesktop`, deadline, time.Second); err != nil || reply != Elsewhere {
		t.Fatalf("launch on another desktop: %q %v", reply, err)
	}
	if asked.Load() != 0 {
		t.Fatal("the window was asked to come to another desktop")
	}
	if reply, err := ask(id, strings.ToUpper(currentDesktop()), deadline, time.Second); err != nil || reply != Activated || asked.Load() != 1 {
		t.Fatalf("launch on this desktop: %q %v, asked %d", reply, err, asked.Load())
	}
}

func TestOnlyTheSameSessionAndDesktopMayBringTheWindow(t *testing.T) {
	if !samePlace(1, `WinSta0\Default`, 1, `winsta0\default`) {
		t.Fatal("the same session and desktop were refused")
	}
	if samePlace(2, `WinSta0\Default`, 1, `WinSta0\Default`) {
		t.Fatal("another session of the user was allowed")
	}
	if samePlace(1, `WinSta0\Winlogon`, 1, `WinSta0\Default`) {
		t.Fatal("another desktop was allowed")
	}
}

// Anything but a request gets no answer and changes nothing; the server then serves the next launch as usual.
func TestServerIgnoresAnythingButARequest(t *testing.T) {
	id := testIdentity(t)
	first := primary(t, id)
	var asked atomic.Int32
	if err := first.Serve(counting(Activated, &asked)); err != nil {
		t.Fatal(err)
	}
	pipe, err := dial(id.pipe, time.Now().Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	io, err := newOverlapped(pipe, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := io.write([]byte("GET / HTTP/1.1\r\n\r\n"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := io.readFull(make([]byte, 1), 2*time.Second); err == nil {
		t.Fatal("the server answered a request that is not one")
	}
	io.close()
	windows.CloseHandle(pipe)
	if reply, err := ask(id, currentDesktop(), time.Now().Add(3*time.Second), time.Second); err != nil || reply != Activated {
		t.Fatalf("the next request: %q %v", reply, err)
	}
	if asked.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", asked.Load())
	}
}

// FILE_FLAG_FIRST_PIPE_INSTANCE: when another process already serves the name, even one that would let this instance
// add a pipe instance of its own, the instance refuses to serve beside it rather than share the name with whatever
// answers there.
func TestServeRefusesANameSomeoneElseServes(t *testing.T) {
	id := testIdentity(t)
	sa, err := securityAttributes(id.user)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := windows.UTF16PtrFromString(id.pipe)
	squatter, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE, windows.PIPE_UNLIMITED_INSTANCES, 64, 64, 0, sa)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(squatter)
	first := primary(t, id)
	if err := first.Serve(func(context.Context) Reply { return Activated }); err == nil {
		t.Fatal("served a pipe name another process holds")
	}
}

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

// asAnotherAccount runs body on this thread with this process's token in which the user's SID only denies access.
// Every grant of the lock and the pipe names the user (or SYSTEM), so the token stands for any other account.
func asAnotherAccount(t *testing.T, user *windows.SID, body func()) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var process windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, &process); err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	disabled := windows.SIDAndAttributes{Sid: user}
	var restricted windows.Token
	if ok, _, err := procCreateRestrictedToken.Call(uintptr(process), 0, 1, uintptr(unsafe.Pointer(&disabled)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted))); ok == 0 {
		t.Fatalf("CreateRestrictedToken: %v", err)
	}
	defer restricted.Close()
	var impersonation windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_IMPERSONATE|windows.TOKEN_QUERY, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &impersonation); err != nil {
		t.Fatal(err)
	}
	defer impersonation.Close()
	if err := windows.SetThreadToken(nil, impersonation); err != nil {
		t.Fatal(err)
	}
	defer windows.RevertToSelf()
	body()
}

func TestAnotherAccountCannotTakeWaitOnOrReachTheLock(t *testing.T) {
	id := testIdentity(t)
	first := primary(t, id)
	var asked atomic.Int32
	if err := first.Serve(counting(Activated, &asked)); err != nil {
		t.Fatal(err)
	}
	sa, err := securityAttributes(id.user)
	if err != nil {
		t.Fatal(err)
	}
	mutexName, _ := windows.UTF16PtrFromString(id.mutex)
	pipeName, _ := windows.UTF16PtrFromString(id.pipe)
	var problems []string
	expectDenied := func(what string, err error) {
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			problems = append(problems, what+": "+errorText(err))
		}
	}
	asAnotherAccount(t, id.user, func() {
		_, err := windows.CreateMutex(nil, false, mutexName)
		expectDenied("create the lock", err)
		_, err = windows.OpenMutex(windows.SYNCHRONIZE, false, mutexName)
		expectDenied("wait on the lock", err)
		_, err = windows.CreateFile(pipeName, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		expectDenied("connect to the pipe", err)
		if _, err := openLock(id, sa); err == nil {
			problems = append(problems, "openLock succeeded")
		}
	})
	for _, problem := range problems {
		t.Error(problem)
	}
	if asked.Load() != 0 {
		t.Fatal("another account reached the handler")
	}
	if later := claim(id, testLimits); later.Outcome != HandedOff {
		t.Fatalf("the user's own launch after the attempts: %s", later.Outcome)
	}
}

func errorText(err error) string {
	if err == nil {
		return "allowed"
	}
	return err.Error()
}
